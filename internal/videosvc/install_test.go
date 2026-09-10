package videosvc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultURLs(t *testing.T) {
	y := DefaultYtDlpURL()
	if !strings.Contains(y, "yt-dlp") {
		t.Fatalf("yt-dlp url = %s", y)
	}
	f := DefaultFfmpegURL()
	if f == "" {
		t.Fatal("ffmpeg url empty")
	}
	if runtime.GOOS == "windows" && !strings.Contains(f, "win64") {
		t.Fatalf("windows ffmpeg url should be win64: %s", f)
	}
}

func TestInstallYtDlpFromFileServer(t *testing.T) {
	if testing.Short() {
		t.Skip("installer test")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/bin/sh\necho mock-yt-dlp\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	bins := NewBinaries(dir)
	inst := NewInstaller(bins)
	inst.ytDlpURL = srv.URL

	var logs []string
	if err := inst.InstallYtDlp(context.Background(), func(m string) {
		logs = append(logs, m)
	}); err != nil {
		t.Fatal(err)
	}
	name := "yt-dlp"
	if runtime.GOOS == "windows" {
		name = "yt-dlp.exe"
	}
	p := filepath.Join(dir, name)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "mock-yt-dlp") {
		t.Fatalf("content = %q", data)
	}
	if bins.YtDlpPath() != p {
		t.Fatalf("YtDlpPath = %s want %s", bins.YtDlpPath(), p)
	}
	if len(logs) == 0 {
		t.Fatal("expected progress logs")
	}
}

func TestInstallFfmpegFromZipWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	if testing.Short() {
		t.Skip("installer test")
	}
	// 构造含 bin/ffmpeg.exe 的 zip
	zipPath := filepath.Join(t.TempDir(), "ff.zip")
	if err := writeMockFfmpegZip(zipPath); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bins := NewBinaries(dir)
	inst := NewInstaller(bins)
	// 直接测解压路径
	if err := extractFfmpegFromZip(zipPath, filepath.Join(dir, "ffmpeg.exe")); err != nil {
		t.Fatal(err)
	}
	if inst.DataDirPath() != dir {
		t.Fatalf("DataDirPath = %s", inst.DataDirPath())
	}
	data, err := os.ReadFile(filepath.Join(dir, "ffmpeg.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake-ffmpeg" {
		t.Fatalf("content = %q", data)
	}
	_ = bins
}
