package sftp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/GopeedLab/gopeed/internal/controller"
	"github.com/GopeedLab/gopeed/internal/fetcher"
	"github.com/GopeedLab/gopeed/pkg/base"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/proxy"
)

const (
	dialTimeout = 15 * time.Second
	readBufSize = 256 * 1024
)

var _ fetcher.Fetcher = (*Fetcher)(nil)

type Fetcher struct {
	ctl    *controller.Controller
	config *config
	meta   *fetcher.FetcherMeta
	data   *fetcherData
	doneCh chan error

	mu      sync.Mutex
	clients []*sftp.Client
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

// remotePath 从任务 URL 解析远端路径与目标地址
func (f *Fetcher) remotePath() (addr, rpath string, user, pass string, err error) {
	u, err := url.Parse(f.meta.Req.URL)
	if err != nil {
		return
	}
	port := u.Port()
	if port == "" {
		port = "22"
	}
	addr = net.JoinHostPort(u.Hostname(), port)
	rpath = u.Path
	user = u.User.Username()
	pass, _ = u.User.Password()
	return
}

// proxyDialer 返回带代理的拨号器；未配置代理或代理协议不受支持时回退直连
func (f *Fetcher) proxyDialer() proxy.Dialer {
	direct := &net.Dialer{Timeout: dialTimeout}
	if f.ctl == nil || f.ctl.GetProxy == nil {
		return direct
	}
	proxyFn := f.ctl.GetProxy(f.meta.Req.Proxy)
	if proxyFn == nil {
		return direct
	}
	reqURL, err := url.Parse(f.meta.Req.URL)
	if err != nil {
		return direct
	}
	pu, err := proxyFn(&http.Request{URL: reqURL})
	if err != nil || pu == nil {
		return direct
	}
	pd, err := proxy.FromURL(pu, direct)
	if err != nil {
		// 代理协议不受支持（如 http 代理）时回退直连
		return direct
	}
	return pd
}

// dial 建立一条 SSH+SFTP 连接
func (f *Fetcher) dial() (*sftp.Client, error) {
	addr, _, user, pass, err := f.remotePath()
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         dialTimeout,
	}
	conn, err := f.proxyDialer().Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	// MaxPacketUnchecked: 大包提升吞吐；pkg/sftp 的 MaxPacket 上限 32KB 不够用
	sc, err := sftp.NewClient(ssh.NewClient(sshConn, chans, reqs), sftp.MaxPacketUnchecked(512*1024))
	if err != nil {
		sshConn.Close()
		return nil, err
	}
	return sc, nil
}

func (f *Fetcher) Resolve(req *base.Request, opts *base.Options) error {
	f.meta.Req = req
	f.meta.Opts = opts
	if f.meta.Opts == nil {
		f.meta.Opts = &base.Options{}
	}
	client, err := f.dial()
	if err != nil {
		return err
	}
	defer client.Close()
	_, rpath, _, _, err := f.remotePath()
	if err != nil {
		return err
	}
	fi, err := client.Stat(rpath)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("sftp: directory download not supported: %s", rpath)
	}
	f.meta.Res = &base.Resource{
		Range: true,
		Size:  fi.Size(),
		Files: []*base.FileInfo{{Name: path.Base(rpath), Size: fi.Size()}},
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
	if err := file.Truncate(f.meta.Res.Size); err != nil {
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
	// 建立连接池
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
	// 校验总进度
	f.mu.Lock()
	var total int64
	for _, c := range f.data.Chunks {
		total += c.Downloaded
	}
	f.mu.Unlock()
	if total != f.meta.Res.Size {
		f.done(fmt.Errorf("sftp: size mismatch after download: %d != %d", total, f.meta.Res.Size))
		return
	}
	f.cleanup()
	f.done(nil)
}

// worker 循环领取未完成 chunk 顺序下载
func (f *Fetcher) worker(idx int, rpath string, file *os.File) {
	client := f.clients[idx]
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
		if err := f.downloadChunk(client, rpath, ck, file); err != nil {
			if f.ctx.Err() == nil {
				f.done(err)
			}
			return
		}
	}
}

// takeChunk 领取一个未完成的 chunk（标记 Busy）
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

func (f *Fetcher) downloadChunk(client *sftp.Client, rpath string, ck *chunk, file *os.File) error {
	defer func() {
		f.mu.Lock()
		ck.Busy = false
		f.mu.Unlock()
	}()
	remote, err := client.Open(rpath)
	if err != nil {
		return err
	}
	defer remote.Close()
	buf := make([]byte, readBufSize)
	offset := ck.Begin + ck.Downloaded
	for ck.remain() > 0 {
		select {
		case <-f.ctx.Done():
			return nil
		default:
		}
		n := int64(len(buf))
		if ck.remain() < n {
			n = ck.remain()
		}
		read, err := remote.ReadAt(buf[:n], offset)
		if read > 0 {
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
		c.Close()
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
