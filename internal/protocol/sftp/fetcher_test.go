package sftp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GopeedLab/gopeed/pkg/base"
	"github.com/gliderlabs/ssh"
	gosftp "github.com/pkg/sftp"
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
	// 4 个默认连接，5MB 应切成多段
	f.mu.Lock()
	nchunks := len(f.data.Chunks)
	f.mu.Unlock()
	if nchunks != 4 {
		t.Fatalf("chunks = %d, want 4", nchunks)
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
