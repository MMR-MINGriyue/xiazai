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
		// SIZE 失败（如目录）→ 按目录递归列举
		files, lerr := ftpListDir(conn, rpath)
		if lerr != nil {
			return fmt.Errorf("ftp: resolve %s: %w", rpath, lerr)
		}
		if len(files) == 0 {
			return fmt.Errorf("ftp: empty directory: %s", rpath)
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
		Size:  size,
		Files: []*base.FileInfo{{Name: path.Base(rpath), Size: size}},
	}
	return nil
}

// ftpListDir 递归列出目录下所有文件（不含目录本身），FileInfo.Path 为相对目录根的路径（含文件名）
func ftpListDir(conn *ftpclient.ServerConn, dir string) ([]*base.FileInfo, error) {
	entries, err := conn.List(dir)
	if err != nil {
		return nil, err
	}
	var files []*base.FileInfo
	for _, e := range entries {
		switch e.Type {
		case ftpclient.EntryTypeFolder:
			sub, err := ftpListDir(conn, path.Join(dir, e.Name))
			if err != nil {
				return nil, err
			}
			for _, sf := range sub {
				sf.Path = path.Join(e.Name, sf.Path)
				files = append(files, sf)
			}
		case ftpclient.EntryTypeFile:
			files = append(files, &base.FileInfo{Name: e.Name, Path: e.Name, Size: int64(e.Size)})
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
		return fmt.Errorf("ftp: no files to download")
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
			f.done(fmt.Errorf("ftp: size mismatch for %s", ft.info.Name))
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

func (f *Fetcher) worker(idx int, rpath string, file *os.File, chunks []*chunk) {
	conn := f.clients[idx]
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
		if err := f.downloadChunk(conn, rpath, ck, file); err != nil {
			if f.ctx.Err() == nil {
				f.done(err)
			}
			return
		}
	}
}

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
