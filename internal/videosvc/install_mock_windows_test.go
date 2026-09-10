//go:build windows

package videosvc

import (
	"archive/zip"
	"os"
)

func writeMockFfmpegZip(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create("ffmpeg-master/bin/ffmpeg.exe")
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("fake-ffmpeg")); err != nil {
		return err
	}
	return zw.Close()
}
