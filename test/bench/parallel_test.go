package bench

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// throttledHandler 每个连接限速 limit 字节/秒（模拟远端服务器单连接瓶颈），支持 Range。
type throttledHandler struct {
	data     []byte
	limit    int64
	mu       sync.Mutex
	conns    int
	maxConns int
	reqs     int
	served   []int64 // 每个请求实际写出的字节数
	requests []string
}

func (h *throttledHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.conns++
	if h.conns > h.maxConns {
		h.maxConns = h.conns
	}
	h.reqs++
	reqNo := h.reqs
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.conns--
		h.mu.Unlock()
	}()

	start, end := int64(0), int64(len(h.data))-1
	hasRange := r.Header.Get("Range") != ""
	if hasRange {
		start, end = parseRange(r.Header.Get("Range"), end)
	}
	if start < 0 || end < start {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	total := end - start + 1
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
	if hasRange {
		// 206 必须带精确的 Content-Range，引擎的 validateRangeResponse 强制校验
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(h.data)))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	// 探测请求（无 Range）全速返回；分段请求限速。
	if !hasRange {
		if _, err := w.Write(h.data); err != nil {
			return
		}
		return
	}

	// 每 tick 写 limit/10 字节，共 10 tick/s → 每连接固定限速
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	written := int64(0)
	startTime := time.Now()
	for written < total {
		<-tick.C
		n := h.limit / 10
		if total-written < n {
			n = total - written
		}
		if _, err := w.Write(h.data[start+written : start+written+n]); err != nil {
			break // 客户端中断
		}
		written += n
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	h.mu.Lock()
	h.served = append(h.served, written)
	dur := time.Since(startTime)
	detail := fmt.Sprintf("#%d %s %s range=[%s] wrote=%d in=%v", reqNo, r.Method, r.URL.Path, r.Header.Get("Range"), written, dur)
	h.requests = append(h.requests, detail)
	h.mu.Unlock()
}

// parseRange 解析 "bytes=start-end"（end 可省略），返回 [start, end]，超出 fileEnd 时截断。
func parseRange(cr string, fileEnd int64) (int64, int64) {
	eq := -1
	dash := -1
	for i := 0; i < len(cr); i++ {
		if cr[i] == '=' {
			eq = i
		}
		if cr[i] == '-' {
			dash = i
			break
		}
	}
	if eq < 0 || dash <= eq {
		return 0, fileEnd
	}
	start := int64(0)
	for i := eq + 1; i < dash; i++ {
		c := cr[i]
		if c < '0' || c > '9' {
			return 0, fileEnd
		}
		start = start*10 + int64(c-'0')
	}
	end := fileEnd
	if dash+1 < len(cr) {
		end = 0
		for i := dash + 1; i < len(cr); i++ {
			c := cr[i]
			if c < '0' || c > '9' {
				break
			}
			end = end*10 + int64(c-'0')
		}
		if end > fileEnd {
			end = fileEnd
		}
	}
	return start, end
}


func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func TestParallelSpeedup(t *testing.T) {
	if testing.Short() {
		t.Skip("benchmark test")
	}
	const (
		fileSize = 8 * 1024 * 1024 // 8MB：给慢启动扩连留出翻倍时间
		limit    = 512 * 1024      // 每连接 512KB/s → 单连接理论耗时 16s
	)
	bin := buildGopeedCLI(t)

	dl := func(conns int) (time.Duration, *throttledHandler, []byte) {
		data := make([]byte, fileSize)
		rand.Read(data)
		h := &throttledHandler{data: data, limit: limit}
		srv := httptest.NewServer(h)
		defer srv.Close()

		dir := t.TempDir()
		start := time.Now()
		// gopeed 的 flag 解析：标志须在位置参数（URL）之前
		out, err := runCmd(t, bin, "-C", strconv.Itoa(conns), "-D", dir, srv.URL+"/file.bin")
		if err != nil {
			t.Fatalf("download failed: %v\n%s", err, out)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "file.bin"))
		if len(got) != len(data) {
			t.Fatalf("size = %d want %d", len(got), len(data))
		}
		return time.Since(start), h, got
	}

	t1, h1, got1 := dl(1)
	t8, h8, got8 := dl(8)
	h1.mu.Lock()
	h8.mu.Lock()
	t.Logf("1 conn: %v reqs=%d maxConns=%d\n  requests: %v\n8 conns: %v reqs=%d maxConns=%d\n  requests: %v", t1, h1.reqs, h1.maxConns, h1.requests, t8, h8.reqs, h8.maxConns, h8.requests)
	h8.mu.Unlock()
	h1.mu.Unlock()
	// 内容校验：8 连接下引擎只服务了少量字节，必须确认落盘内容与源数据一致
	if !bytesEqual(got1, h1.data) {
		d := firstDiff(got1, h1.data)
		t.Fatalf("1-conn file content mismatch at %d\ngot [%d:%d]=%x\nsrc [%d:%d]=%x", d, d, d+32, got1[d:d+32], d, d+32, h1.data[d:d+32])
	}
	if !bytesEqual(got8, h8.data) {
		d := firstDiff(got8, h8.data)
		t.Fatalf("8-conn file content mismatch at %d\ngot [%d:%d]=%x\nsrc [%d:%d]=%x", d, d, d+32, got8[d:d+32], d, d+32, h8.data[d:d+32])
	}
	if t8 > t1/2 {
		t.Fatalf("no speedup: 1conn=%v 8conns=%v", t1, t8)
	}
}

func buildGopeedCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gopeed-cli.exe")
	// 在仓库根构建（测试工作目录为 test/bench，向上两级）
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/gopeed")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

func runCmd(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

var _ = fmt.Sprintf
