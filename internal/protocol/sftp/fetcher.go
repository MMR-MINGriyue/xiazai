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
	"github.com/GopeedLab/gopeed/internal/ratelimit"
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

	// limiter 全局限速器（来自 ctl，可为 nil）
	limiter ratelimit.Limiter

	mu      sync.Mutex
	clients []*sftp.Client
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// fileTasks 运行期构建的下载任务列表（单文件 1 个；目录多文件各 1 个）
	fileTasks []*fileTask
}

// fileTask 描述一个文件的下载任务
type fileTask struct {
	info   *base.FileInfo
	chunks []*chunk
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
	if f.ctl != nil {
		f.limiter = f.ctl.Limiter
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

// sshAuth 构建认证方式列表：配置了私钥则优先用密钥认证，密码始终兜底
func (f *Fetcher) sshAuth(pass string) []ssh.AuthMethod {
	auths := []ssh.AuthMethod{ssh.Password(pass)}
	if f.config != nil && f.config.PrivateKeyPath != "" {
		if keyBytes, err := os.ReadFile(f.config.PrivateKeyPath); err == nil {
			if signer, err := ssh.ParsePrivateKey(keyBytes); err == nil {
				auths = append([]ssh.AuthMethod{ssh.PublicKeys(signer)}, auths...)
			}
		}
	}
	return auths
}

// dial 建立一条 SSH+SFTP 连接
func (f *Fetcher) dial() (*sftp.Client, error) {
	addr, _, user, pass, err := f.remotePath()
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            f.sshAuth(pass),
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
		files, err := sftpListDir(client, rpath)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("sftp: empty directory: %s", rpath)
		}
		var total int64
		for _, file := range files {
			total += file.Size
		}
		f.meta.Res = &base.Resource{
			Name:  path.Base(rpath),
			Range: true,
			Size:  total,
			Files: files,
		}
		return nil
	}
	f.meta.Res = &base.Resource{
		Range: true,
		Size:  fi.Size(),
		Files: []*base.FileInfo{{Name: path.Base(rpath), Size: fi.Size()}},
	}
	return nil
}

// sftpListDir 递归列出目录下所有文件（不含目录本身），FileInfo.Path 为相对目录根的路径
func sftpListDir(client *sftp.Client, dir string) ([]*base.FileInfo, error) {
	entries, err := client.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []*base.FileInfo
	for _, e := range entries {
		rel := e.Name()
		if e.IsDir() {
			sub, err := sftpListDir(client, path.Join(dir, rel))
			if err != nil {
				return nil, err
			}
			for _, sf := range sub {
				sf.Path = path.Join(rel, sf.Path)
				files = append(files, sf)
			}
		} else {
			files = append(files, &base.FileInfo{Name: e.Name(), Path: rel, Size: e.Size()})
		}
	}
	return files, nil
}

func (f *Fetcher) Start() error {
	f.mu.Lock()
	if f.cancel != nil {
		f.mu.Unlock()
		return nil // 已在运行
	}
	if f.fileTasks == nil {
		if err := f.buildFileTasks(); err != nil {
			f.mu.Unlock()
			return err
		}
	}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.mu.Unlock()

	// 预创建所有本地目录
	for _, ft := range f.fileTasks {
		if err := os.MkdirAll(filepath.Dir(f.localPath(ft.info)), 0755); err != nil {
			return err
		}
	}
	go f.download()
	return nil
}

// localPath 计算文件本地落盘路径：多文件 = RootDir/Path（Path 含相对子路径与文件名）；单文件 = SingleFilepath
func (f *Fetcher) localPath(info *base.FileInfo) string {
	if info.Path == "" {
		return f.meta.SingleFilepath()
	}
	return filepath.Join(f.meta.RootDirPath(), filepath.FromSlash(info.Path))
}

// buildFileTasks 构建下载任务列表：单文件复用 data.Chunks（断点续传兼容），多文件按文件分 chunk
func (f *Fetcher) buildFileTasks() error {
	res := f.meta.Res
	if res == nil || len(res.Files) == 0 {
		return fmt.Errorf("sftp: no files to download")
	}
	if len(res.Files) == 1 && res.Files[0].Path == "" {
		if f.data.Chunks == nil {
			f.data.Chunks = splitChunks(res.Size, f.config.Connections)
		}
		f.fileTasks = []*fileTask{{info: res.Files[0], chunks: f.data.Chunks}}
		return nil
	}
	if f.data.FilesChunks == nil {
		f.data.FilesChunks = make([][]*chunk, len(res.Files))
	}
	for i, info := range res.Files {
		if f.data.FilesChunks[i] == nil {
			f.data.FilesChunks[i] = splitChunks(info.Size, f.config.Connections)
		}
		f.fileTasks = append(f.fileTasks, &fileTask{info: info, chunks: f.data.FilesChunks[i]})
	}
	return nil
}

func (f *Fetcher) download() {
	// 捕获本次运行的 ctx，避免 Pause→Start 后收尾误读新一轮 ctx
	ctx := f.ctx
	_, basePath, _, _, err := f.remotePath()
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
	for _, ft := range f.fileTasks {
		if ctx.Err() != nil {
			return // Pause 触发，不发 done
		}
		file, err := os.OpenFile(f.localPath(ft.info), os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			f.done(err)
			return
		}
		if err := file.Truncate(ft.info.Size); err != nil {
			file.Close()
			f.done(err)
			return
		}
		rpath := basePath
		if ft.info.Path != "" {
			rpath = path.Join(basePath, filepath.ToSlash(ft.info.Path))
		}
		// 每个文件用 Connections 个 worker 并行分段下载，文件间顺序执行
		for i := 0; i < n; i++ {
			f.wg.Add(1)
			go func(idx int) {
				defer f.wg.Done()
				f.worker(idx, rpath, file, ft.chunks)
			}(i)
		}
		f.wg.Wait()
		file.Close()
		if ctx.Err() != nil {
			return
		}
		if !chunksComplete(ft.chunks, ft.info.Size) {
			f.done(fmt.Errorf("sftp: size mismatch for %s", ft.info.Name))
			return
		}
	}
	f.cleanup()
	f.done(nil)
}

// chunksComplete 校验 chunk 集合的已下载字节与目标大小一致
func chunksComplete(chunks []*chunk, size int64) bool {
	var total int64
	for _, c := range chunks {
		total += c.Downloaded
	}
	return total == size
}

// worker 循环领取本文件未完成 chunk 顺序下载
func (f *Fetcher) worker(idx int, rpath string, file *os.File, chunks []*chunk) {
	client := f.clients[idx]
	for {
		select {
		case <-f.ctx.Done():
			return
		default:
		}
		ck := f.takeChunk(chunks)
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

// takeChunk 从指定 chunk 集合领取一个未完成的 chunk（标记 Busy）
func (f *Fetcher) takeChunk(chunks []*chunk) *chunk {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range chunks {
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
			// 全局限速：按实际读取字节数消费令牌（阻塞直到允许）
			if f.limiter != nil {
				f.limiter.Acquire(int64(read))
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.fileTasks) > 1 {
		// 多文件：每文件已下载字节
		prog := make(fetcher.Progress, len(f.fileTasks))
		for i, ft := range f.fileTasks {
			var total int64
			for _, c := range ft.chunks {
				total += c.Downloaded
			}
			prog[i] = total
		}
		return prog
	}
	// 单文件：总和（向后兼容）
	var total int64
	for _, c := range f.data.Chunks {
		total += c.Downloaded
	}
	return fetcher.Progress{total}
}

func (f *Fetcher) Wait() error {
	return <-f.doneCh
}
