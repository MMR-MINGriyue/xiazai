//go:build !windows

package videosvc

import (
	"fmt"
	"os"
	"path/filepath"
)

// extractFfmpegFromZip 非 Windows 不支持 zip 包体（使用静态 tar 需外部工具）。
func extractFfmpegFromZip(zipPath, dest string) error {
	return fmt.Errorf("zip extract not supported on this platform; place ffmpeg at %s", dest)
}

var _ = os.Remove
var _ = filepath.Dir
