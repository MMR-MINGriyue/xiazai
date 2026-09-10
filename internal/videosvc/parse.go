package videosvc

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// YtDlpFormat 是 yt-dlp -J 中 formats 数组的子集字段。
type YtDlpFormat struct {
	FormatID   string `json:"format_id"`
	Ext        string `json:"ext"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Vcodec     string `json:"vcodec"`
	Acodec     string `json:"acodec"`
	URL        string `json:"url"`
	Filesize   int64  `json:"filesize"`
	FilesizeA  int64  `json:"filesize_approx"`
	Protocol   string `json:"protocol"`
	FormatNote string `json:"format_note"`
	Tbr        float64 `json:"tbr"`
}

func (f *YtDlpFormat) Size() int64 {
	if f.Filesize > 0 {
		return f.Filesize
	}
	return f.FilesizeA
}

func (f *YtDlpFormat) HasVideo() bool {
	c := strings.ToLower(f.Vcodec)
	return c != "" && c != "none"
}

func (f *YtDlpFormat) HasAudio() bool {
	c := strings.ToLower(f.Acodec)
	return c != "" && c != "none"
}

// VideoInfo 是解析后的视频元数据（UI 可直接消费）。
type VideoInfo struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Thumbnail string          `json:"thumbnail"`
	Duration  float64         `json:"duration"`
	Webpage   string          `json:"webpageUrl"`
	Formats   []FormatOption  `json:"formats"`
	RawFormats []*YtDlpFormat `json:"-"`
}

// FormatOption 是 UI 展示的一档清晰度。
type FormatOption struct {
	// ID 用于 Download 请求；progressive 为 format_id；分离流为 "videoID+audioID"
	ID       string `json:"id"`
	Label    string `json:"label"`    // e.g. "1080p mp4 (video+audio)"
	Height   int    `json:"height"`
	Width    int    `json:"width"`
	Ext      string `json:"ext"`
	VideoURL string `json:"videoUrl"`
	AudioURL string `json:"audioUrl"` // 空=progressive 单流
	Size     int64  `json:"size"`
	// Vcodec/Acodec 仅诊断用
	Vcodec string `json:"vcodec,omitempty"`
	Acodec string `json:"acodec,omitempty"`
}

// Runner 执行外部命令，便于测试注入 mock。
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// Parse 调用 yt-dlp -J 解析视频元数据并聚合清晰度。
func Parse(ctx context.Context, binPath string, pageURL string, runner Runner) (*VideoInfo, error) {
	if binPath == "" {
		return nil, fmt.Errorf("yt-dlp not found")
	}
	if runner == nil {
		runner = defaultRunner
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	out, err := runner(ctx, binPath, "-J", "--no-playlist", "--no-warnings", pageURL)
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w: %s", err, truncate(string(out), 400))
	}

	var raw struct {
		ID        string         `json:"id"`
		Title     string         `json:"title"`
		Thumbnail string         `json:"thumbnail"`
		Duration  float64        `json:"duration"`
		Webpage   string         `json:"webpage_url"`
		Formats   []*YtDlpFormat `json:"formats"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse yt-dlp json: %w", err)
	}
	if raw.Title == "" {
		return nil, fmt.Errorf("yt-dlp returned empty title")
	}

	info := &VideoInfo{
		ID:         raw.ID,
		Title:      raw.Title,
		Thumbnail:  raw.Thumbnail,
		Duration:   raw.Duration,
		Webpage:    raw.Webpage,
		RawFormats: raw.Formats,
		Formats:    aggregateFormats(raw.Formats),
	}
	return info, nil
}

// aggregateFormats 把 yt-dlp 原始 formats 聚合成 UI 选项：
// 1) progressive（有视频+音频）直接一档
// 2) video-only 按高度去重，配上最高码率 audio-only
func aggregateFormats(formats []*YtDlpFormat) []FormatOption {
	var progressive []*YtDlpFormat
	var videoOnly []*YtDlpFormat
	var bestAudio *YtDlpFormat

	for _, f := range formats {
		if f == nil || f.URL == "" {
			continue
		}
		// 跳过 storyboard / m3u8 以外的非 http(s)（hls 可后续支持）
		proto := strings.ToLower(f.Protocol)
		if strings.Contains(proto, "m3u8") || strings.Contains(proto, "dash") {
			// 仍允许 http(s) m3u8，交给引擎可能失败——M3 先跳过非 https 直链
		}
		if !strings.HasPrefix(strings.ToLower(f.URL), "http") {
			continue
		}
		switch {
		case f.HasVideo() && f.HasAudio():
			progressive = append(progressive, f)
		case f.HasVideo() && !f.HasAudio():
			videoOnly = append(videoOnly, f)
		case !f.HasVideo() && f.HasAudio():
			if bestAudio == nil || f.Tbr > bestAudio.Tbr {
				bestAudio = f
			}
		}
	}

	seen := map[int]bool{}
	var opts []FormatOption

	// progressive：按高度倒序
	sortFormatsDesc(progressive)
	for _, f := range progressive {
		h := f.Height
		if h == 0 {
			continue
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		opts = append(opts, FormatOption{
			ID:       f.FormatID,
			Label:    fmt.Sprintf("%dp %s (视频+音频)", h, f.Ext),
			Height:   h,
			Width:    f.Width,
			Ext:      f.Ext,
			VideoURL: f.URL,
			Size:     f.Size(),
			Vcodec:   f.Vcodec,
			Acodec:   f.Acodec,
		})
	}

	// video-only + best audio
	sortFormatsDesc(videoOnly)
	seenV := map[int]bool{}
	for _, f := range videoOnly {
		h := f.Height
		if h == 0 || seenV[h] {
			continue
		}
		seenV[h] = true
		id := f.FormatID
		audioURL := ""
		size := f.Size()
		label := fmt.Sprintf("%dp %s (仅视频)", h, f.Ext)
		if bestAudio != nil {
			id = f.FormatID + "+" + bestAudio.FormatID
			audioURL = bestAudio.URL
			size += bestAudio.Size()
			label = fmt.Sprintf("%dp %s (视频+音频分离流)", h, f.Ext)
		}
		opts = append(opts, FormatOption{
			ID:       id,
			Label:    label,
			Height:   h,
			Width:    f.Width,
			Ext:      f.Ext,
			VideoURL: f.URL,
			AudioURL: audioURL,
			Size:     size,
			Vcodec:   f.Vcodec,
			Acodec:   "aac",
		})
	}

	return opts
}

func sortFormatsDesc(fs []*YtDlpFormat) {
	// 简单插入排序（数量小）
	for i := 1; i < len(fs); i++ {
		j := i
		for j > 0 && formatKey(fs[j]) > formatKey(fs[j-1]) {
			fs[j], fs[j-1] = fs[j-1], fs[j]
			j--
		}
	}
}

func formatKey(f *YtDlpFormat) int64 {
	return int64(f.Height)*1_000_000 + int64(f.Tbr)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// SplitFormatID 拆分 "video+audio" 或单 format id。
func SplitFormatID(id string) (videoID, audioID string) {
	parts := strings.SplitN(id, "+", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

// FormatInt 便于测试 strconv 使用保持 import。
var _ = strconv.Itoa
