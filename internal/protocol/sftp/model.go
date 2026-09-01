package sftp

// chunk 表示文件的一个分段区间 [Begin, End]（闭区间）
type chunk struct {
	Begin      int64 `json:"begin"`
	End        int64 `json:"end"`
	Downloaded int64 `json:"downloaded"`
	// Busy 标记该 chunk 正被某个 worker 处理（不持久化）
	Busy bool `json:"-"`
}

func (c *chunk) size() int64 {
	return c.End - c.Begin + 1
}

func (c *chunk) remain() int64 {
	return c.size() - c.Downloaded
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

// config 是 sftp 协议的全局配置（DownloaderStoreConfig.ProtocolConfig 持久化）
type config struct {
	Connections int `json:"connections"`
	// PrivateKeyPath 指定私钥文件路径；设置后优先用密钥认证（密码作为兜底）
	PrivateKeyPath string `json:"privateKeyPath"`
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
	Chunks []*chunk `json:"chunks"`
}
