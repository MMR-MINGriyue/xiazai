package cmd

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/GopeedLab/gopeed/pkg/base"
	"github.com/GopeedLab/gopeed/pkg/rest"
	"github.com/GopeedLab/gopeed/pkg/rest/model"
)

//go:embed banner.txt
var banner string

func Start(cfg *model.StartConfig) {
	fmt.Println(banner)
	srv, listener, err := rest.BuildServer(cfg)
	if err != nil {
		panic(err)
	}
	downloadCfg, err := rest.Downloader.GetConfig()
	if err != nil {
		panic(err)
	}
	if downloadCfg.FirstLoad {
		// Set default download config
		if cfg.DownloadConfig != nil {
			cfg.DownloadConfig.Merge(downloadCfg)
			// TODO Use PatchConfig
			rest.Downloader.PutConfig(cfg.DownloadConfig)
			downloadCfg = cfg.DownloadConfig
		}

		downloadDir := downloadCfg.DownloadDir
		// Set default download dir, in docker, it will be ${exe}/Downloads, else it will be ${user}/Downloads
		if downloadDir == "" {
			if base.InDocker == "true" {
				downloadDir = filepath.Join(filepath.Dir(cfg.StorageDir), "Downloads")
			} else {
				userDir, err := os.UserHomeDir()
				if err == nil {
					downloadDir = filepath.Join(userDir, "Downloads")
				}
			}
			if downloadDir != "" {
				downloadCfg.DownloadDir = downloadDir
				rest.Downloader.PutConfig(downloadCfg)
			}
		}
	}
	watchExit()

	addr := listener.Addr().String()
	writeAPIEndpointFile(addr)

	fmt.Printf("Server start success on http://%s\n", addr)
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

// writeAPIEndpointFile 写出 REST 地址，供浏览器 host 直连引擎（不经 Flutter RPC）。
func writeAPIEndpointFile(addr string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".gopeed")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	// 规范化为 host:port
	hostPort := addr
	if strings.HasPrefix(hostPort, "[") || strings.Contains(hostPort, ":") {
		// listener.Addr() 已是 host:port
	} else {
		hostPort = "127.0.0.1:" + hostPort
	}
	if !strings.Contains(hostPort, ":") {
		hostPort = "127.0.0.1:" + hostPort
	}
	payload := map[string]string{
		"address": hostPort,
		"url":     "http://" + hostPort,
	}
	data, _ := json.Marshal(payload)
	_ = os.WriteFile(filepath.Join(dir, "api-endpoint.json"), data, 0644)
}

func watchExit() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		fmt.Printf("Server is shutting down due to signal: %s\n", sig)
		rest.Downloader.Close()
		os.Exit(0)
	}()
}
