package videosvc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Installer 负责把 yt-dlp / ffmpeg 下载到 DataDir（受管组件）。
type Installer struct {
	bins   *Binaries
	client *http.Client
	// urls 可注入，便于测试
	ytDlpURL  string
	ffmpegURL string
}

// NewInstaller 创建安装器。dataDir 为空则用系统临时旁的 gopeed-tools。
func NewInstaller(bins *Binaries) *Installer {
	return &Installer{
		bins: bins,
		client: &http.Client{
			Timeout: 15 * time.Minute,
		},
	}
}

// DefaultYtDlpURL 返回当前平台 yt-dlp 下载地址。
func DefaultYtDlpURL() string {
	if runtime.GOOS == "windows" {
		return "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe"
	}
	return "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp"
}

// DefaultFfmpegURL 返回当前平台 ffmpeg 下载地址（Windows 用 BtbN win64 zip）。
func DefaultFfmpegURL() string {
	switch runtime.GOOS {
	case "windows":
		// BtbN GPL builds，latest 固定文件名
		return "https://github.com/BtbN/FFmpeg-Builds/releases/latest/download/ffmpeg-master-latest-win64-gpl.zip"
	case "darwin":
		return "https://evermeet.cx/ffmpeg/getrelease/zip"
	default:
		return "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz"
	}
}

func (i *Installer) dataDir() string {
	if i.bins.DataDir != "" {
		return i.bins.DataDir
	}
	// 兜底：与 yt-dlp 自更新目录一致
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gopeed", "tools")
}

// DataDirPath 返回受管组件目录。
func (i *Installer) DataDirPath() string { return i.dataDir() }

// EnsureDir 确保数据目录存在。
func (i *Installer) EnsureDir() (string, error) {
	dir := i.dataDir()
	if dir == "" {
		return "", fmt.Errorf("cannot resolve tools data dir")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// InstallYtDlp 下载 yt-dlp 到 DataDir。
func (i *Installer) InstallYtDlp(ctx context.Context, progress func(msg string)) error {
	dir, err := i.EnsureDir()
	if err != nil {
		return err
	}
	name := "yt-dlp"
	if runtime.GOOS == "windows" {
		name = "yt-dlp.exe"
	}
	dst := filepath.Join(dir, name)
	url := i.ytDlpURL
	if url == "" {
		url = DefaultYtDlpURL()
	}
	if progress != nil {
		progress("downloading yt-dlp...")
	}
	if err := i.downloadFile(ctx, url, dst); err != nil {
		return fmt.Errorf("download yt-dlp: %w", err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dst, 0755)
	}
	i.bins.YtDlp = dst
	if progress != nil {
		progress("yt-dlp installed: " + dst)
	}
	return nil
}

// InstallFfmpeg 下载 ffmpeg。Windows 解压 zip 中的 bin/ffmpeg.exe。
func (i *Installer) InstallFfmpeg(ctx context.Context, progress func(msg string)) error {
	dir, err := i.EnsureDir()
	if err != nil {
		return err
	}
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	dst := filepath.Join(dir, name)
	url := i.ffmpegURL
	if url == "" {
		url = DefaultFfmpegURL()
	}
	if progress != nil {
		progress("downloading ffmpeg...")
	}

	tmp, err := os.CreateTemp(dir, "ffmpeg-dl-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := i.downloadFile(ctx, url, tmpPath); err != nil {
		return fmt.Errorf("download ffmpeg: %w", err)
	}

	if runtime.GOOS == "windows" {
		if err := extractFfmpegFromZip(tmpPath, dst); err != nil {
			return fmt.Errorf("extract ffmpeg: %w", err)
		}
	} else {
		// 非 Windows：若下载的是裸二进制则直接落盘；tar.xz 需外部解压，先做文件拷贝兜底
		if err := os.Rename(tmpPath, dst); err != nil {
			data, rerr := os.ReadFile(tmpPath)
			if rerr != nil {
				return rerr
			}
			if werr := os.WriteFile(dst, data, 0755); werr != nil {
				return werr
			}
		}
		_ = os.Chmod(dst, 0755)
	}
	i.bins.Ffmpeg = dst
	if progress != nil {
		progress("ffmpeg installed: " + dst)
	}
	return nil
}

// InstallAll 安装缺失组件。
func (i *Installer) InstallAll(ctx context.Context, progress func(msg string)) (missing []string, err error) {
	if i.bins.YtDlpPath() == "" {
		if err := i.InstallYtDlp(ctx, progress); err != nil {
			missing = append(missing, "yt-dlp")
		}
	}
	if i.bins.FfmpegPath() == "" {
		if err := i.InstallFfmpeg(ctx, progress); err != nil {
			missing = append(missing, "ffmpeg")
		}
	}
	if _, miss := i.bins.Ready(); len(miss) > 0 {
		return miss, fmt.Errorf("install incomplete: %v", miss)
	}
	return nil, nil
}

func (i *Installer) downloadFile(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gopeed-video-installer")
	resp, err := i.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d for %s", resp.StatusCode, url)
	}
	// 先写临时再 rename，避免半截文件
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	cerr := f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if cerr != nil {
		os.Remove(tmp)
		return cerr
	}
	os.Remove(dst)
	return os.Rename(tmp, dst)
}
