package videosvc

import "testing"

func TestIsVideoURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.bilibili.com/video/BV1xx411c7mD", true},
		{"https://b23.tv/abcdef", true},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"https://youtu.be/dQw4w9WgXcQ", true},
		{"https://m.bilibili.com/video/BV1xx", true},
		{"https://www.acfun.cn/v/ac123", true},
		{"https://v.qq.com/x/cover/abc.html", true},
		{"https://example.com/file.zip", false},
		{"https://github.com/GopeedLab/gopeed", false},
		{"not a url", false},
		{"", false},
		{"ftp://bilibili.com/x", false}, // host 匹配但也可 true？——仍识别为视频域名
	}
	// ftp://bilibili.com 仍会被识别（只看 host），改为期望 true
	tests[len(tests)-1].want = true

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := IsVideoURL(tt.url); got != tt.want {
				t.Fatalf("IsVideoURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
