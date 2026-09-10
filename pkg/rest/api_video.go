package rest

import (
	"context"
	"net/http"
	"sync"

	"github.com/GopeedLab/gopeed/internal/videosvc"
	"github.com/GopeedLab/gopeed/pkg/base"
	"github.com/GopeedLab/gopeed/pkg/download"
	"github.com/GopeedLab/gopeed/pkg/rest/model"
	"github.com/gorilla/mux"
)

var (
	videoOnce    sync.Once
	videoSvc     *videosvc.Service
	videoSvcInit func()
)

// downloaderTaskCreator 适配 Downloader → videosvc.TaskCreator
type downloaderTaskCreator struct {
	d *download.Downloader
}

func (c *downloaderTaskCreator) CreateDirect(req *base.Request, opts *base.Options) (string, error) {
	return c.d.CreateDirect(req, opts)
}

func (c *downloaderTaskCreator) GetTask(id string) *videosvc.TaskStatusView {
	t := c.d.GetTask(id)
	if t == nil {
		return nil
	}
	view := &videosvc.TaskStatusView{
		ID:     t.ID,
		Status: t.Status,
		Name:   t.Name(),
	}
	if t.Meta != nil && t.Meta.Res != nil && len(t.Meta.Res.Files) > 0 {
		view.Path = t.Meta.SingleFilepath()
	}
	return view
}

func (c *downloaderTaskCreator) DeleteTasks(ids []string, force bool) error {
	if len(ids) == 0 {
		return nil
	}
	return c.d.Delete(&download.TaskFilter{IDs: ids}, force)
}

// videoStorageDir 由 BuildServer 注入，用于 job 持久化。
var videoStorageDir string

// SetVideoStorageDir 设置视频 job 存储目录（BuildServer 启动时调用）。
func SetVideoStorageDir(dir string) {
	videoStorageDir = dir
}

func getVideoService() *videosvc.Service {
	videoOnce.Do(func() {
		bins := videosvc.NewBinaries("")
		store := videosvc.NewFileJobStore(videoStorageDir)
		videoSvc = videosvc.NewService(bins, &downloaderTaskCreator{d: Downloader}, nil, store)
	})
	return videoSvc
}

// VideoResolveRequest POST /api/v1/video/resolve
type VideoResolveRequest struct {
	URL string `json:"url"`
}

// VideoResolve 解析视频站链接，返回元数据与清晰度列表。
func VideoResolve(w http.ResponseWriter, r *http.Request) {
	var req VideoResolveRequest
	if !ReadJson(r, w, &req) {
		return
	}
	if req.URL == "" {
		WriteJson(w, model.NewErrorResult("param invalid: url", model.CodeInvalidParam))
		return
	}
	if !videosvc.IsVideoURL(req.URL) {
		WriteJson(w, model.NewErrorResult("not a recognized video site url"))
		return
	}
	info, err := getVideoService().Resolve(r.Context(), req.URL)
	if err != nil {
		WriteJson(w, model.NewErrorResult(err.Error()))
		return
	}
	WriteJson(w, model.NewOkResult(info))
}

// VideoDownloadRequest POST /api/v1/video/download
type VideoDownloadRequest struct {
	URL      string `json:"url"`
	FormatID string `json:"formatId"`
	Title    string `json:"title,omitempty"`
	Path     string `json:"path"`
	Name     string `json:"name,omitempty"`
}

// VideoDownload 创建视频下载+合并 job。
func VideoDownload(w http.ResponseWriter, r *http.Request) {
	var req VideoDownloadRequest
	if !ReadJson(r, w, &req) {
		return
	}
	job, err := getVideoService().Download(context.Background(), &videosvc.DownloadRequest{
		URL:      req.URL,
		FormatID: req.FormatID,
		Title:    req.Title,
		Path:     req.Path,
		Name:     req.Name,
	})
	if err != nil {
		WriteJson(w, model.NewErrorResult(err.Error()))
		return
	}
	WriteJson(w, model.NewOkResult(job))
}

// VideoJobs 列出 job。
func VideoJobs(w http.ResponseWriter, r *http.Request) {
	WriteJson(w, model.NewOkResult(getVideoService().ListJobs()))
}

// VideoJob 查询单个 job。
func VideoJob(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	job, ok := getVideoService().GetJob(id)
	if !ok {
		WriteJson(w, model.NewErrorResult("job not found", model.CodeTaskNotFound))
		return
	}
	WriteJson(w, model.NewOkResult(job))
}

// VideoBinaries 诊断受管组件。
func VideoBinaries(w http.ResponseWriter, r *http.Request) {
	svc := getVideoService()
	ready, missing := svc.Bins().Ready()
	data := map[string]any{
		"ready":   ready,
		"missing": missing,
		"ytDlp":   svc.Bins().YtDlpPath(),
		"ffmpeg":  svc.Bins().FfmpegPath(),
		"dataDir": svc.Installer().DataDirPath(),
	}
	WriteJson(w, model.NewOkResult(data))
}

// VideoBinInstallRequest POST /api/v1/video/bins/install
type VideoBinInstallRequest struct {
	// Components 为空则安装全部缺失组件；可选 ["yt-dlp","ffmpeg"]
	Components []string `json:"components,omitempty"`
}

// VideoBinInstall 下载安装 yt-dlp / ffmpeg 到应用数据目录。
func VideoBinInstall(w http.ResponseWriter, r *http.Request) {
	var req VideoBinInstallRequest
	// body 可为空：EOF 视为默认安装缺失组件
	if r.ContentLength > 0 {
		_ = ReadJson(r, w, &req)
	}

	svc := getVideoService()
	inst := svc.Installer()
	ctx := r.Context()

	var msgs []string
	progress := func(m string) { msgs = append(msgs, m) }

	want := map[string]bool{}
	if len(req.Components) == 0 {
		if svc.Bins().YtDlpPath() == "" {
			want["yt-dlp"] = true
		}
		if svc.Bins().FfmpegPath() == "" {
			want["ffmpeg"] = true
		}
	} else {
		for _, c := range req.Components {
			want[c] = true
		}
	}

	var lastErr error
	if want["yt-dlp"] {
		if err := inst.InstallYtDlp(ctx, progress); err != nil {
			lastErr = err
		}
	}
	if want["ffmpeg"] {
		if err := inst.InstallFfmpeg(ctx, progress); err != nil {
			lastErr = err
		}
	}

	ready, missing := svc.Bins().Ready()
	data := map[string]any{
		"ready":   ready,
		"missing": missing,
		"ytDlp":   svc.Bins().YtDlpPath(),
		"ffmpeg":  svc.Bins().FfmpegPath(),
		"logs":    msgs,
	}
	if lastErr != nil && !ready {
		WriteJson(w, model.NewErrorResult(lastErr.Error()))
		return
	}
	WriteJson(w, model.NewOkResult(data))
}
