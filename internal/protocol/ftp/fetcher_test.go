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
			fmt.Fprintf(conn, "211-Features\r\n REST STREAM\r\n EPSV\r\n211 End\r\n")
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
			if err != nil {
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
	// 4 个默认连接，5MB 应切成多段
	f.mu.Lock()
	nchunks := len(f.data.Chunks)
	f.mu.Unlock()
	if nchunks != 4 {
		t.Fatalf("chunks = %d, want 4", nchunks)
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
