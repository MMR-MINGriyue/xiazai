//go:build windows

package videosvc

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractFfmpegFromZip 从 BtbN zip 中提取 ffmpeg.exe 到 dest。
func extractFfmpegFromZip(zipPath, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	var target *zip.File
	for _, f := range zr.File {
		// 形如 ffmpeg-master-latest-win64-gpl/bin/ffmpeg.exe
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasSuffix(name, "/bin/ffmpeg.exe") || name == "bin/ffmpeg.exe" ||
			strings.HasSuffix(name, "/ffmpeg.exe") {
			if target == nil || len(f.Name) < len(target.Name) {
				target = f
			}
		}
	}
	if target == nil {
		return fmt.Errorf("ffmpeg.exe not found in archive")
	}
	rc, err := target.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	tmp := dest + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, rc)
	cerr := out.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if cerr != nil {
		os.Remove(tmp)
		return cerr
	}
	_ = os.MkdirAll(filepath.Dir(dest), 0755)
	os.Remove(dest)
	return os.Rename(tmp, dest)
}
