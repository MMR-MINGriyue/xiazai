//go:build !windows

package videosvc

import "errors"

func writeMockFfmpegZip(path string) error {
	return errors.New("not used on non-windows")
}
