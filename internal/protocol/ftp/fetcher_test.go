package ftp

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GopeedLab/gopeed/internal/controller"
	"github.com/GopeedLab/gopeed/pkg/base"
)

// ---------------------------------------------------------------------------
// 最小 FTP 测试服务器：只实现本 fetcher 用到的命令子集
// USER/PASS/TYPE/SIZE/EPSV/PASV/REST/RETR/QUIT，单目录服务（root 为根）
// ---------------------------------------------------------------------------

type testFtpServer struct {
	root string
}

func (s *testFtpServer) localPath(rpath string) string {
	p := strings.TrimPrefix(rpath, "/")
	return filepath.Join(s.root, filepath.FromSlash(p))
}

// bufferedConn 让 tls.Server 优先消费 bufio 预读的字节（AUTH TLS 升级关键）
type bufferedConn struct {
	net.Conn
	br *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if c.br.Buffered() > 0 {
		return c.br.Read(p)
	}
	return c.Conn.Read(p)
}

// handle 处理一条控制连接；tlsCfg 非空时支持 AUTH TLS 升级（FTPS）
func (s *testFtpServer) handle(conn net.Conn, tlsCfg *tls.Config) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	fmt.Fprintf(conn, "220 FTP Test Server Ready\r\n")
	var offset uint64
	var dataLn net.Listener
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd, arg := line, ""
		if i := strings.Index(line, " "); i >= 0 {
			cmd, arg = line[:i], strings.TrimSpace(line[i+1:])
		}
		switch strings.ToUpper(cmd) {
		case "AUTH":
			if tlsCfg != nil && strings.EqualFold(arg, "TLS") {
				fmt.Fprintf(conn, "234 AUTH TLS OK\r\n")
				// bufio 可能已预读 ClientHello，必须从 br 缓冲续读
				conn = tls.Server(&bufferedConn{Conn: conn, br: br}, tlsCfg)
				br = bufio.NewReader(conn)
				continue
			}
			fmt.Fprintf(conn, "502 AUTH not supported\r\n")
		case "USER":
			fmt.Fprintf(conn, "331 Password required\r\n")
		case "PASS":
			fmt.Fprintf(conn, "230 User logged in\r\n")
		case "TYPE":
			fmt.Fprintf(conn, "200 Type set\r\n")
		case "SYST":
			fmt.Fprintf(conn, "215 UNIX Type: L8\r\n")
		case "FEAT":
			fmt.Fprintf(conn, "211-Features\r\n MLST\r\n MLSD\r\n REST STREAM\r\n EPSV\r\n211 End\r\n")
		case "OPTS", "NOOP":
			fmt.Fprintf(conn, "200 OK\r\n")
		case "PBSZ":
			fmt.Fprintf(conn, "200 PBSZ=0\r\n")
		case "PROT":
			fmt.Fprintf(conn, "200 Protection level set\r\n")
		case "PWD":
			fmt.Fprintf(conn, "257 \"/\"\r\n")
		case "CWD":
			fmt.Fprintf(conn, "250 OK\r\n")
		case "SIZE":
			fi, err := os.Stat(s.localPath(arg))
			if err != nil || fi.IsDir() {
				fmt.Fprintf(conn, "550 File not found\r\n")
			} else {
				fmt.Fprintf(conn, "213 %d\r\n", fi.Size())
			}
		case "REST":
			fmt.Sscanf(arg, "%d", &offset)
			fmt.Fprintf(conn, "350 Restarting at %d\r\n", offset)
		case "EPSV":
			dl, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				fmt.Fprintf(conn, "425 Can't open data connection\r\n")
				continue
			}
			dataLn = dl
			fmt.Fprintf(conn, "229 Entering Extended Passive Mode (|||%d|)\r\n", dl.Addr().(*net.TCPAddr).Port)
		case "PASV":
			dl, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				fmt.Fprintf(conn, "425 Can't open data connection\r\n")
				continue
			}
			dataLn = dl
			p := dl.Addr().(*net.TCPAddr).Port
			fmt.Fprintf(conn, "227 Entering Passive Mode (127,0,0,1,%d,%d)\r\n", p/256, p%256)
		case "MLSD":
			// RFC 3659 目录列举（数据连接输出 type=...;size=...; /name 行）
			if dataLn == nil {
				fmt.Fprintf(conn, "425 Use EPSV first\r\n")
				continue
			}
			dconn, err := dataLn.Accept()
			dataLn.Close()
			dataLn = nil
			if err != nil {
				fmt.Fprintf(conn, "425 Data connection failed\r\n")
				continue
			}
			if tlsCfg != nil {
				dconn = tls.Server(dconn, tlsCfg)
			}
			fmt.Fprintf(conn, "150 Opening data connection\r\n")
			entries, _ := os.ReadDir(s.localPath(arg))
			w := bufio.NewWriter(dconn)
			for _, e := range entries {
				info, ierr := e.Info()
				if ierr != nil {
					continue
				}
				typ := "file"
				if e.IsDir() {
					typ = "dir"
				}
				fmt.Fprintf(w, "type=%s;size=%d; /%s\r\n", typ, info.Size(), e.Name())
			}
			w.Flush()
			dconn.Close()
			fmt.Fprintf(conn, "226 Transfer complete\r\n")
		case "RETR":
			if dataLn == nil {
				fmt.Fprintf(conn, "425 Use EPSV first\r\n")
				continue
			}
			dconn, err := dataLn.Accept() // 客户端已先连接数据端口再发 RETR
			dataLn.Close()
			dataLn = nil
			if err != nil {
				fmt.Fprintf(conn, "425 Data connection failed\r\n")
				continue
			}
			if tlsCfg != nil {
				// jlaffaye 数据连接为惰性 TLS 握手（等第一次 Read/Write），
				// 此处只包装不显式握手，否则与客户端互等导致死锁
				dconn = tls.Server(dconn, tlsCfg)
			}
			fmt.Fprintf(conn, "150 Opening data connection\r\n")
			f, err := os.Open(s.localPath(arg))
			if err != nil {
				dconn.Close()
				fmt.Fprintf(conn, "550 File not found\r\n")
				continue
			}
			if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
				f.Close()
				dconn.Close()
				fmt.Fprintf(conn, "451 Seek failed\r\n")
				continue
			}
			offset = 0
			io.Copy(dconn, f)
			f.Close()
			dconn.Close()
			fmt.Fprintf(conn, "226 Transfer complete\r\n")
		case "QUIT":
			fmt.Fprintf(conn, "221 Goodbye\r\n")
			return
		default:
			fmt.Fprintf(conn, "500 Command not implemented\r\n")
		}
	}
}

func startTestFtpServer(t *testing.T, root string) string {
	t.Helper()
	s := &testFtpServer{root: root}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn, nil)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

// selfSignedCert 生成 127.0.0.1 的自签证书（FTPS 测试服务器用）
func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}

// startTestFtpsServer 启动支持 AUTH TLS（显式 FTPS）的 FTP 服务器
func startTestFtpsServer(t *testing.T, root string) string {
	t.Helper()
	s := &testFtpServer{root: root}
	cert := selfSignedCert(t)
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn, tlsCfg)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

// ---------------------------------------------------------------------------
// 辅助：与 sftp 测试同构
// ---------------------------------------------------------------------------

func writeTestFile(t *testing.T, dir string, size int64) (name string, sum string) {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	name = "testfile.bin"
	if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	return name, hex.EncodeToString(h[:])
}

func fileSum(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func newTestFetcher(t *testing.T, url string, path string) *Fetcher {
	t.Helper()
	f := &Fetcher{}
	f.Setup(nil) // 测试无 ctl，用默认 config
	f.meta.Req = &base.Request{URL: url}
	f.meta.Opts = &base.Options{Path: path, Name: "out.bin"}
	return f
}

func TestFtpDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 5*1024*1024)
	addr := startTestFtpServer(t, root)
	outDir := t.TempDir()

	f := newTestFetcher(t, fmt.Sprintf("ftp://user:pass@%s/%s", addr, name), outDir)
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	if f.meta.Res.Size != 5*1024*1024 {
		t.Fatalf("size = %d", f.meta.Res.Size)
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-f.doneCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timeout")
	}
	if fileSum(t, filepath.Join(outDir, "out.bin")) != wantSum {
		t.Fatal("hash mismatch")
	}
	// 4 个默认连接，5MB 应切成多段；work stealing 可能再拆，允许 >=4
	f.mu.Lock()
	nchunks := len(f.data.Chunks)
	f.mu.Unlock()
	if nchunks < 4 {
		t.Fatalf("chunks = %d, want >= 4", nchunks)
	}
}

func TestFtpResume(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 3*1024*1024)
	addr := startTestFtpServer(t, root)
	outDir := t.TempDir()

	f := newTestFetcher(t, fmt.Sprintf("ftp://user:pass@%s/%s", addr, name), outDir)
	f.config.Connections = 2
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	// 下载约 512KB 后暂停
	deadline := time.Now().Add(10 * time.Second)
	for f.Progress().TotalDownloaded() < 512*1024 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := f.Pause(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	var saved int64
	for _, c := range f.data.Chunks {
		saved += c.Downloaded
	}
	f.mu.Unlock()
	if saved == 0 {
		t.Fatal("no progress saved before pause")
	}
	// 重新 Start，从 chunk 进度续传
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-f.doneCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timeout")
	}
	if fileSum(t, filepath.Join(outDir, "out.bin")) != wantSum {
		t.Fatal("hash mismatch after resume")
	}
}

func TestFtpsDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 2*1024*1024)
	addr := startTestFtpsServer(t, root)
	outDir := t.TempDir()

	f := newTestFetcher(t, fmt.Sprintf("ftps://user:pass@%s/%s", addr, name), outDir)
	f.config.InsecureSkipVerify = true // 自签证书
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	if f.meta.Res.Size != 2*1024*1024 {
		t.Fatalf("size = %d", f.meta.Res.Size)
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-f.doneCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timeout")
	}
	if fileSum(t, filepath.Join(outDir, "out.bin")) != wantSum {
		t.Fatal("hash mismatch over TLS")
	}
}

func TestFtpsResume(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 3*1024*1024)
	addr := startTestFtpsServer(t, root)
	outDir := t.TempDir()

	f := newTestFetcher(t, fmt.Sprintf("ftps://user:pass@%s/%s", addr, name), outDir)
	f.config.Connections = 2
	f.config.InsecureSkipVerify = true
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for f.Progress().TotalDownloaded() < 512*1024 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := f.Pause(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	var saved int64
	for _, c := range f.data.Chunks {
		saved += c.Downloaded
	}
	f.mu.Unlock()
	if saved == 0 {
		t.Fatal("no progress saved before pause")
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-f.doneCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timeout")
	}
	if fileSum(t, filepath.Join(outDir, "out.bin")) != wantSum {
		t.Fatal("hash mismatch after resume over TLS")
	}
}

// buildTestDir 构造测试目录树，返回 相对路径 -> sha256
func buildTestDir(t *testing.T, root string) map[string]string {
	t.Helper()
	want := map[string]string{}
	write := func(rel string, size int64) {
		dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(rel)))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, size)
		if _, err := rand.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), data, 0644); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(data)
		want[rel] = hex.EncodeToString(h[:])
	}
	write("top.bin", 1024)
	write("sub1/a.bin", 2*1024*1024)
	write("sub1/sub2/b.bin", 512*1024)
	return want
}

func TestFtpDownloadDir(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	want := buildTestDir(t, root)
	addr := startTestFtpServer(t, root)
	outDir := t.TempDir()

	f := &Fetcher{}
	f.Setup(nil)
	f.config.Connections = 2
	f.meta.Req = &base.Request{URL: fmt.Sprintf("ftp://user:pass@%s/%s", addr, "")}
	f.meta.Opts = &base.Options{Path: outDir}
	// 直接解析目录路径（URL 末尾不带文件名，指向 root）
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	if f.meta.Res.Name == "" || len(f.meta.Res.Files) != len(want) {
		t.Fatalf("res name=%q files=%d want %d", f.meta.Res.Name, len(f.meta.Res.Files), len(want))
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-f.doneCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timeout")
	}
	for rel, sum := range want {
		got := fileSum(t, filepath.Join(outDir, f.meta.Res.Name, filepath.FromSlash(rel)))
		if got != sum {
			t.Fatalf("hash mismatch for %s", rel)
		}
	}
	prog := f.Progress()
	if len(prog) != len(want) {
		t.Fatalf("progress len = %d, want %d", len(prog), len(want))
	}
}

func TestFtpRateLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 2*1024*1024)
	addr := startTestFtpServer(t, root)
	outDir := t.TempDir()

	ctl := controller.NewController()
	ctl.SetGlobalRateLimit(512 * 1024)
	f := &Fetcher{}
	f.Setup(ctl)
	f.meta.Req = &base.Request{URL: fmt.Sprintf("ftp://user:pass@%s/%s", addr, name)}
	f.meta.Opts = &base.Options{Path: outDir, Name: "out.bin"}
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-f.doneCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timeout")
	}
	elapsed := time.Since(start)
	if elapsed < 2*time.Second || elapsed > 10*time.Second {
		t.Fatalf("elapsed %v out of range [2s, 10s] for 512KB/s limit", elapsed)
	}
	if fileSum(t, filepath.Join(outDir, "out.bin")) != wantSum {
		t.Fatal("hash mismatch with rate limit")
	}
}

func TestStealChunkSplit(t *testing.T) {
	f := &Fetcher{}
	f.activeChunks = []*chunk{{Begin: 0, End: stealMinChunkSize, Downloaded: 0, Busy: true}}
	if f.stealChunk() != nil {
		t.Fatal("should not steal small chunk")
	}
	big := &chunk{Begin: 0, End: 2*1024*1024 - 1, Downloaded: 0, Busy: true}
	f.activeChunks = []*chunk{big}
	if f.stealChunk() == nil {
		t.Fatal("should steal big chunk")
	}
	if len(f.activeChunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(f.activeChunks))
	}
	half, steal := f.activeChunks[0], f.activeChunks[1]
	if half.end()+1 != steal.Begin || steal.end() != 2*1024*1024-1 {
		t.Fatalf("split invalid: half=[0,%d] steal=[%d,%d]", half.end(), steal.Begin, steal.end())
	}
	if steal.size() < stealMinChunkSize || half.size() < stealMinChunkSize {
		t.Fatalf("fragmented: half=%d steal=%d", half.size(), steal.size())
	}
}

func TestFtpWorkStealing(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 8*1024*1024)
	addr := startTestFtpServer(t, root)
	outDir := t.TempDir()

	f := &Fetcher{}
	f.Setup(nil)
	f.config.Connections = 4
	f.meta.Req = &base.Request{URL: fmt.Sprintf("ftp://user:pass@%s/%s", addr, name)}
	f.meta.Opts = &base.Options{Path: outDir, Name: "out.bin"}
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	f.data.Chunks = []*chunk{{Begin: 0, End: 6*1024*1024 - 1}}
	pos := int64(6 * 1024 * 1024)
	for i := 0; i < 4; i++ {
		f.data.Chunks = append(f.data.Chunks, &chunk{Begin: pos, End: pos + 512*1024 - 1})
		pos += 512 * 1024
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-f.doneCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timeout")
	}
	if fileSum(t, filepath.Join(outDir, "out.bin")) != wantSum {
		t.Fatal("hash mismatch")
	}
	f.mu.Lock()
	nchunks := len(f.data.Chunks)
	f.mu.Unlock()
	if nchunks <= 5 {
		t.Fatalf("expected work stealing to split chunks, got %d chunks", nchunks)
	}
}
