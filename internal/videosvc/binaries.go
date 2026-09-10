package videosvc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Binaries 受管外部组件路径。
type Binaries struct {
	YtDlp   string
	Ffmpeg  string
	DataDir string // 应用数据目录，优先于 PATH
}

// NewBinaries 创建定位器；dataDir 可为空。
func NewBinaries(dataDir string) *Binaries {
	return &Binaries{DataDir: dataDir}
}

// Ready 报告缺失组件；两者都在则 ready=true。
func (b *Binaries) Ready() (ready bool, missing []string) {
	if b.findYtDlp() == "" {
		missing = append(missing, "yt-dlp")
	}
	if b.findFfmpeg() == "" {
		missing = append(missing, "ffmpeg")
	}
	return len(missing) == 0, missing
}

// YtDlp 返回 yt-dlp 可执行文件路径（空=未找到）。
func (b *Binaries) YtDlpPath() string { return b.findYtDlp() }

// Ffmpeg 返回 ffmpeg 可执行文件路径（空=未找到）。
func (b *Binaries) FfmpegPath() string { return b.findFfmpeg() }

func (b *Binaries) findYtDlp() string {
	if b.YtDlp != "" {
		return b.YtDlp
	}
	name := "yt-dlp"
	if runtime.GOOS == "windows" {
		name = "yt-dlp.exe"
	}
	return b.lookup(name)
}

func (b *Binaries) findFfmpeg() string {
	if b.Ffmpeg != "" {
		return b.Ffmpeg
	}
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	return b.lookup(name)
}

func (b *Binaries) lookup(name string) string {
	if b.DataDir != "" {
		p := filepath.Join(b.DataDir, name)
		if fileExists(p) {
			return p
		}
	}
	// 受管组件默认目录（Installer 落盘位置）
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".gopeed", "tools", name)
		if fileExists(p) {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		for _, extra := range windowsExtraDirs() {
			p := filepath.Join(extra, name)
			if fileExists(p) {
				return p
			}
		}
	}
	return ""
}

func windowsExtraDirs() []string {
	var dirs []string
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs,
			filepath.Join(local, "Microsoft", "WinGet", "Links"),
			filepath.Join(local, "Programs"),
		)
	}
	if user := os.Getenv("USERPROFILE"); user != "" {
		dirs = append(dirs, filepath.Join(user, "scoop", "shims"))
	}
	return dirs
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ErrBinariesMissing 二进制缺失错误。
func ErrBinariesMissing(missing []string) error {
	return fmt.Errorf("video binaries missing: %v; install yt-dlp and ffmpeg and ensure they are on PATH", missing)
}
