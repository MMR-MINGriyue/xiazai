package videosvc

import (
	"net/url"
	"strings"
)

// videoHosts 识别的视频站主机后缀（子域名一并匹配）。
var videoHosts = []string{
	"bilibili.com",
	"b23.tv",
	"youtube.com",
	"youtu.be",
	"acfun.cn",
	"v.qq.com",
	"iqiyi.com",
	"vimeo.com",
	"douyin.com",
	"ixigua.com",
}

// IsVideoURL 判断是否为支持的视频站链接。
func IsVideoURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range videoHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}
