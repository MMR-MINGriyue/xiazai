package ftp

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GopeedLab/gopeed/internal/controller"
	"github.com/GopeedLab/gopeed/internal/fetcher"
	"github.com/GopeedLab/gopeed/pkg/base"
	ftpclient "github.com/jlaffaye/ftp"
)

const (
	ftpTimeout = 15 * time.Second
	ftpBufSize = 64 * 1024
)

var _ fetcher.Fetcher = (*Fetcher)(nil)

type Fetcher struct {
	ctl    *controller.Controller
	config *config
	meta   *fetcher.FetcherMeta
	data   *fetcherData
	doneCh chan error

	mu      sync.Mutex
	clients []*ftpclient.ServerConn
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func (f *Fetcher) Setup(ctl *controller.Controller) {
	f.ctl = ctl
	f.doneCh = make(chan error, 1)
	if f.meta == nil {
		f.meta = &fetcher.FetcherMeta{}
	}
	if f.data == nil {
		f.data = &fetcherData{}
	}
	if f.config == nil {
		f.config = &config{}
	}
	// 测试场景 ctl 可能为 nil；生产环境由 Downloader 注入配置
	if f.ctl != nil && f.ctl.GetConfig != nil {
		f.ctl.GetConfig(f.config)
	}
	f.config.init()
}

func (f *Fetcher) remotePath() (addr, rpath, user, pass string, err error) {
	u, err := url.Parse(f.meta.Req.URL)
	if err != nil {
		return
	}
	port := u.Port()
	if port == "" {
		port = "21"
	}
	addr = net.JoinHostPort(u.Hostname(), port)
	rpath = u.Path
	user = u.User.Username()
	pass, _ = u.User.Password()
	return
}

// dial 建立一条 FTP 控制连接（二进制模式）；ftps:// 走显式 TLS（AUTH TLS）
func (f *Fetcher) dial() (*ftpclient.ServerConn, error) {
	addr, _, user, pass, err := f.remotePath()
	if err != nil {
		return nil, err
	}
	opts := []ftpclient.DialOption{ftpclient.DialWithTimeout(ftpTimeout)}
	if u, _ := url.Parse(f.meta.Req.URL); u != nil && strings.EqualFold(u.Scheme, "ftps") {
		opts = append(opts, ftpclient.DialWithExplicitTLS(&tls.Config{
			ServerName:         u.Hostname(),
			InsecureSkipVerify: f.config != nil && f.config.InsecureSkipVerify, //nolint:gosec // 内网自签服务器场景可配
		}))
	}
	conn, err := ftpclient.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}
	if err = conn.Login(user, pass); err != nil {
		conn.Quit()
		return nil, err
	}
	if err = conn.Type(ftpclient.TransferTypeBinary); err != nil {
		conn.Quit()
		return nil, err
	}
	return conn, nil
}

func (f *Fetcher) Resolve(req *base.Request, opts *base.Options) error {
	f.meta.Req = req
	f.meta.Opts = opts
	if f.meta.Opts == nil {
		f.meta.Opts = &base.Options{}
	}
	conn, err := f.dial()
	if err != nil {
		return err
	}
	defer conn.Quit()
	_, rpath, _, _, err := f.remotePath()
	if err != nil {
		return err
	}
	size, err := conn.FileSize(rpath)
	if err != nil {
		return err
	}
	f.meta.Res = &base.Resource{
		Range: true, // 我们用 REST 实现分段
		Size:  size,
		Files: []*base.FileInfo{{Name: path.Base(rpath), Size: size}},
	}
	return nil
}

func (f *Fetcher) Start() error {
	f.mu.Lock()
	if f.cancel != nil {
		f.mu.Unlock()
		return nil // 已在运行
	}
	if f.data.Chunks == nil {
		f.data.Chunks = splitChunks(f.meta.Res.Size, f.config.Connections)
	}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.mu.Unlock()

	filePath := f.meta.SingleFilepath()
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	if err = file.Truncate(f.meta.Res.Size); err != nil {
		file.Close()
		return err
	}
	go f.download(file)
	return nil
}

func (f *Fetcher) download(file *os.File) {
	defer file.Close()
	// 捕获本次运行的 ctx，避免 Pause→Start 后收尾误读新一轮 ctx
	ctx := f.ctx
	_, rpath, _, _, err := f.remotePath()
	if err != nil {
		f.done(err)
		return
	}
	n := f.config.Connections
	for i := 0; i < n; i++ {
		c, err := f.dial()
		if err != nil {
			f.cleanup()
			f.done(err)
			return
		}
		f.mu.Lock()
		f.clients = append(f.clients, c)
		f.mu.Unlock()
	}
	for i := 0; i < n; i++ {
		f.wg.Add(1)
		go func(idx int) {
			defer f.wg.Done()
			f.worker(idx, rpath, file)
		}(i)
	}
	f.wg.Wait()
	if ctx.Err() != nil {
		return // Pause 触发，不发 done
	}
	f.mu.Lock()
	var total int64
	for _, c := range f.data.Chunks {
		total += c.Downloaded
	}
	f.mu.Unlock()
	if total != f.meta.Res.Size {
		f.done(fmt.Errorf("ftp: size mismatch after download: %d != %d", total, f.meta.Res.Size))
		return
	}
	f.cleanup()
	f.done(nil)
}

func (f *Fetcher) worker(idx int, rpath string, file *os.File) {
	conn := f.clients[idx]
	for {
		select {
		case <-f.ctx.Done():
			return
		default:
		}
		ck := f.takeChunk()
		if ck == nil {
			return
		}
		if err := f.downloadChunk(conn, rpath, ck, file); err != nil {
			if f.ctx.Err() == nil {
				f.done(err)
			}
			return
		}
	}
}

func (f *Fetcher) takeChunk() *chunk {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.data.Chunks {
		if !c.Busy && c.remain() > 0 {
			c.Busy = true
			return c
		}
	}
	return nil
}

// downloadChunk 用 REST+RETR 从 offset 拉取该 chunk 剩余部分
func (f *Fetcher) downloadChunk(conn *ftpclient.ServerConn, rpath string, ck *chunk, file *os.File) error {
	defer func() {
		f.mu.Lock()
		ck.Busy = false
		f.mu.Unlock()
	}()
	offset := ck.Begin + ck.Downloaded
	// RetrFrom = REST <offset> + RETR <path>（服务器返回 125/150 后开始传输）
	resp, err := conn.RetrFrom(rpath, uint64(offset))
	if err != nil {
		return err
	}
	defer resp.Close()
	buf := make([]byte, ftpBufSize)
	for ck.remain() > 0 {
		select {
		case <-f.ctx.Done():
			return nil
		default:
		}
		read, err := resp.Read(buf)
		if read > 0 {
			if int64(read) > ck.remain() {
				read = int(ck.remain()) // 服务器可能多给，截断
			}
			if _, werr := file.WriteAt(buf[:read], offset); werr != nil {
				return werr
			}
			offset += int64(read)
			f.mu.Lock()
			ck.Downloaded += int64(read)
			f.mu.Unlock()
		}
		if err != nil {
			if err == io.EOF {
				if ck.remain() > 0 {
					// 远端文件提前结束，避免 worker 无限重取该 chunk
					return io.ErrUnexpectedEOF
				}
				return nil
			}
			return err
		}
	}
	return nil
}

func (f *Fetcher) Pause() error {
	f.mu.Lock()
	cancel := f.cancel
	f.mu.Unlock()
	if cancel != nil {
		cancel()
		f.wg.Wait()
		f.cleanup()
	}
	return nil
}

func (f *Fetcher) Close() error {
	return f.Pause()
}

func (f *Fetcher) cleanup() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.clients {
		c.Quit()
	}
	f.clients = nil
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
}

func (f *Fetcher) done(err error) {
	select {
	case f.doneCh <- err:
	default:
	}
}

func (f *Fetcher) Patch(req *base.Request, opts *base.Options) error { return nil }

func (f *Fetcher) Stats() any {
	return f.data.Chunks
}

func (f *Fetcher) Meta() *fetcher.FetcherMeta {
	return f.meta
}

func (f *Fetcher) Progress() fetcher.Progress {
	var total int64
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.data.Chunks {
		total += c.Downloaded
	}
	return fetcher.Progress{total}
}

func (f *Fetcher) Wait() error {
	return <-f.doneCh
}
