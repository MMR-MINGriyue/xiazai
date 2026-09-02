package ftp

import "sync/atomic"

// stealMinChunkSize 动态分段的最小切分粒度（IDM 式尾部接管的下限，避免碎片化）
const stealMinChunkSize = 512 * 1024

// chunk 表示文件的一个分段区间 [Begin, End]（闭区间）。
// End/Downloaded 可能被并发修改（work stealing 切分 / 进度更新），一律走 atomic 访问。
type chunk struct {
	Begin      int64 `json:"begin"`
	End        int64 `json:"end"`
	Downloaded int64 `json:"downloaded"`
	// Busy 标记该 chunk 正被某个 worker 处理（不持久化；读写都在 f.mu 保护下）
	Busy bool `json:"-"`
}

func (c *chunk) end() int64            { return atomic.LoadInt64(&c.End) }
func (c *chunk) setEnd(v int64)        { atomic.StoreInt64(&c.End, v) }
func (c *chunk) downloaded() int64     { return atomic.LoadInt64(&c.Downloaded) }
func (c *chunk) addDownloaded(v int64) { atomic.AddInt64(&c.Downloaded, v) }
func (c *chunk) size() int64 {
	return c.end() - c.Begin + 1
}

func (c *chunk) remain() int64 {
	return c.size() - c.downloaded()
}

// splitChunks 把 size 字节切成 n 个连续分段；小文件（<1MB）或 n<=1 时返回单段
func splitChunks(size int64, n int) []*chunk {
	if size <= 0 {
		return []*chunk{{Begin: 0, End: -1}}
	}
	if n <= 1 || size < 1024*1024 {
		return []*chunk{{Begin: 0, End: size - 1}}
	}
	if n > 16 {
		n = 16
	}
	base := size / int64(n)
	chunks := make([]*chunk, 0, n)
	var begin int64
	for i := 0; i < n; i++ {
		end := begin + base - 1
		if i == n-1 {
			end = size - 1 // 余数归最后一段
		}
		chunks = append(chunks, &chunk{Begin: begin, End: end})
		begin = end + 1
	}
	return chunks
}

// config 是 ftp 协议的全局配置（DownloaderStoreConfig.ProtocolConfig 持久化）
type config struct {
	Connections int `json:"connections"`
	// InsecureSkipVerify 跳过 TLS 证书校验（内网自签证书服务器用；默认 false）
	InsecureSkipVerify bool `json:"insecureSkipVerify"`
}

func (c *config) init() {
	if c.Connections <= 0 {
		c.Connections = 4
	}
	if c.Connections > 16 {
		c.Connections = 16
	}
}

// fetcherData 是 Fetcher 的持久化状态（断点续传数据，Manager.Store/Restore 序列化）
type fetcherData struct {
	// 单文件模式：chunk 进度（向后兼容）
	Chunks []*chunk `json:"chunks"`
	// 多文件（目录）模式：每文件的 chunk 进度，顺序与 meta.Res.Files 一致
	FilesChunks [][]*chunk `json:"filesChunks,omitempty"`
}
