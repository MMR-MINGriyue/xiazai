package sftp

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GopeedLab/gopeed/internal/controller"
	"github.com/GopeedLab/gopeed/pkg/base"
	"github.com/gliderlabs/ssh"
	gosftp "github.com/pkg/sftp"
	xssh "golang.org/x/crypto/ssh"
)

// startTestSftpServer 在本进程内启动一个 SFTP 服务器，服务 root 目录。
// 任意用户名/密码均可登录（测试专用）。
func startTestSftpServer(t *testing.T, root string) string {
	t.Helper()
	server := &ssh.Server{
		Addr: "127.0.0.1:0",
		PasswordHandler: func(ctx ssh.Context, password string) bool {
			return true
		},
		SubsystemHandlers: map[string]ssh.SubsystemHandler{
			"sftp": func(s ssh.Session) {
				srv, err := gosftp.NewServer(s, gosftp.WithServerWorkingDirectory(root))
				if err != nil {
					return
				}
				srv.Serve()
			},
		},
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(ln)
	t.Cleanup(func() { server.Close() })
	return ln.Addr().String()
}

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

// sftpURL 构造测试 URL：远端路径用真实本地绝对路径。
// pkg/sftp 服务器按 toLocalPath 解析：Windows 上 /C:/x → C:\x，Unix 上 /tmp/x 即绝对路径，
// 因此服务器可直接命中 root 下的文件（URL 裸名 /x 会因绝对路径语义解析失败，故必须用全路径）。
func sftpURL(addr, localPath string) string {
	return fmt.Sprintf("sftp://user:pass@%s/%s", addr, filepath.ToSlash(localPath))
}

func TestSftpDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 5*1024*1024)
	addr := startTestSftpServer(t, root)
	outDir := t.TempDir()

	f := newTestFetcher(t, sftpURL(addr, filepath.Join(root, name)), outDir)
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
	got := fileSum(t, filepath.Join(outDir, "out.bin"))
	if got != wantSum {
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

func TestSftpResume(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 3*1024*1024)
	addr := startTestSftpServer(t, root)
	outDir := t.TempDir()

	f := newTestFetcher(t, sftpURL(addr, filepath.Join(root, name)), outDir)
	f.config.Connections = 2
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	// 下载约 1MB 后暂停
	deadline := time.Now().Add(10 * time.Second)
	for f.Progress().TotalDownloaded() < 1024*1024 && time.Now().Before(deadline) {
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
	got := fileSum(t, filepath.Join(outDir, "out.bin"))
	if got != wantSum {
		t.Fatal("hash mismatch after resume")
	}
}

// startTestSftpServerWithKey 启动仅接受指定公钥的 SFTP 服务器（密码一律拒绝）
func startTestSftpServerWithKey(t *testing.T, root string, pub ssh.PublicKey) string {
	t.Helper()
	server := &ssh.Server{
		Addr: "127.0.0.1:0",
		PasswordHandler: func(ctx ssh.Context, password string) bool {
			return false
		},
		PublicKeyHandler: func(ctx ssh.Context, key ssh.PublicKey) bool {
			return bytes.Equal(key.Marshal(), pub.Marshal())
		},
		SubsystemHandlers: map[string]ssh.SubsystemHandler{
			"sftp": func(s ssh.Session) {
				srv, err := gosftp.NewServer(s, gosftp.WithServerWorkingDirectory(root))
				if err != nil {
					return
				}
				srv.Serve()
			},
		},
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(ln)
	t.Cleanup(func() { server.Close() })
	return ln.Addr().String()
}

func TestSftpKeyAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	// 生成 ed25519 密钥对，私钥写入临时文件
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := xssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	block, err := xssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 512*1024)
	addr := startTestSftpServerWithKey(t, root, sshPub)
	outDir := t.TempDir()

	f := newTestFetcher(t, sftpURL(addr, filepath.Join(root, name)), outDir)
	f.config.PrivateKeyPath = keyPath // 密钥优先，密码会被服务器拒绝
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
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
	if got := fileSum(t, filepath.Join(outDir, "out.bin")); got != wantSum {
		t.Fatal("hash mismatch with key auth")
	}
}

func TestSftpKeyAuthMissingKeyFails(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := xssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	name, _ := writeTestFile(t, root, 1024)
	addr := startTestSftpServerWithKey(t, root, sshPub)
	outDir := t.TempDir()

	// 未配置私钥，仅密码 → 服务器拒绝认证，Resolve 必须失败
	f := newTestFetcher(t, sftpURL(addr, filepath.Join(root, name)), outDir)
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err == nil {
		t.Fatal("expected auth failure without key")
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

func TestSftpDownloadDir(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	want := buildTestDir(t, root)
	addr := startTestSftpServer(t, root)
	outDir := t.TempDir()

	f := &Fetcher{}
	f.Setup(nil)
	f.config.Connections = 2
	f.meta.Req = &base.Request{URL: sftpURL(addr, root)}
	f.meta.Opts = &base.Options{Path: outDir}
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	if f.meta.Res.Name == "" || len(f.meta.Res.Files) != len(want) {
		t.Fatalf("res name=%q files=%d want %d", f.meta.Res.Name, len(f.meta.Res.Files), len(want))
	}
	if f.meta.Res.Size <= 0 {
		t.Fatal("total size should be positive")
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
	// 校验落盘：outDir/<目录名>/<相对路径>
	for rel, sum := range want {
		got := fileSum(t, filepath.Join(outDir, f.meta.Res.Name, filepath.FromSlash(rel)))
		if got != sum {
			t.Fatalf("hash mismatch for %s", rel)
		}
	}
	// 多文件进度：每文件一个元素
	prog := f.Progress()
	if len(prog) != len(want) {
		t.Fatalf("progress len = %d, want %d", len(prog), len(want))
	}
	if prog.TotalDownloaded() != f.meta.Res.Size {
		t.Fatalf("total downloaded = %d, want %d", prog.TotalDownloaded(), f.meta.Res.Size)
	}
}

func TestSftpRateLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 2*1024*1024)
	addr := startTestSftpServer(t, root)
	outDir := t.TempDir()

	// 全局限速 512KB/s（桶突发容量=1s 令牌）
	ctl := controller.NewController()
	ctl.SetGlobalRateLimit(512 * 1024)
	f := &Fetcher{}
	f.Setup(ctl)
	f.meta.Req = &base.Request{URL: sftpURL(addr, filepath.Join(root, name))}
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
	// 2MB @ 512KB/s，1s 突发免费 → 理论 ~3s
	if elapsed < 2*time.Second || elapsed > 10*time.Second {
		t.Fatalf("elapsed %v out of range [2s, 10s] for 512KB/s limit", elapsed)
	}
	if got := fileSum(t, filepath.Join(outDir, "out.bin")); got != wantSum {
		t.Fatal("hash mismatch with rate limit")
	}
}

func TestStealChunkSplit(t *testing.T) {
	f := &Fetcher{}
	// 剩余不足 2×最小粒度 → 不可切
	f.activeChunks = []*chunk{{Begin: 0, End: stealMinChunkSize, Downloaded: 0, Busy: true}}
	if f.stealChunk() != nil {
		t.Fatal("should not steal small chunk")
	}
	// 2MB chunk（剩余 2MB > 1MB）→ 从剩余中点切开
	big := &chunk{Begin: 0, End: 2*1024*1024 - 1, Downloaded: 0, Busy: true}
	f.activeChunks = []*chunk{big}
	if f.stealChunk() == nil {
		t.Fatal("should steal big chunk")
	}
	if len(f.activeChunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(f.activeChunks))
	}
	// 原 chunk 保留前半，新 chunk 从 split+1 开始，两段拼接覆盖 [0, 2MB-1]
	half := f.activeChunks[0]
	steal := f.activeChunks[1]
	if half.end() >= steal.Begin || steal.end() != 2*1024*1024-1 {
		t.Fatalf("split invalid: half=[0,%d] steal=[%d,%d]", half.end(), steal.Begin, steal.end())
	}
	if half.end()+1 != steal.Begin {
		t.Fatalf("gap at split: half.end=%d steal.begin=%d", half.end(), steal.Begin)
	}
	// 切出的段必须 >= 最小粒度
	if steal.size() < stealMinChunkSize || half.size() < stealMinChunkSize {
		t.Fatalf("fragmented: half=%d steal=%d", half.size(), steal.size())
	}
}

func TestSftpWorkStealing(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root := t.TempDir()
	name, wantSum := writeTestFile(t, root, 8*1024*1024)
	addr := startTestSftpServer(t, root)
	outDir := t.TempDir()

	f := &Fetcher{}
	f.Setup(nil)
	f.config.Connections = 4
	f.meta.Req = &base.Request{URL: sftpURL(addr, filepath.Join(root, name))}
	f.meta.Opts = &base.Options{Path: outDir, Name: "out.bin"}
	if err := f.Resolve(f.meta.Req, f.meta.Opts); err != nil {
		t.Fatal(err)
	}
	// 手动构造不均 chunk：1 个 6MB 大段 + 4 个 512KB 小段（覆盖 8MB）
	// 大段拖住一个 worker，其余 worker 秒下小段后触发 steal 切大段
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
	if got := fileSum(t, filepath.Join(outDir, "out.bin")); got != wantSum {
		t.Fatal("hash mismatch")
	}
	f.mu.Lock()
	nchunks := len(f.data.Chunks)
	f.mu.Unlock()
	if nchunks <= 5 {
		t.Fatalf("expected work stealing to split chunks, got %d chunks", nchunks)
	}
}
