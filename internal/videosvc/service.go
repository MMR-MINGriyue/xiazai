package videosvc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GopeedLab/gopeed/pkg/base"
	fhttp "github.com/GopeedLab/gopeed/pkg/protocol/http"
	gonanoid "github.com/matoous/go-nanoid/v2"
)

// TaskCreator 抽象 Downloader 创建任务，便于测试。
type TaskCreator interface {
	CreateDirect(req *base.Request, opts *base.Options) (string, error)
	GetTask(id string) *TaskStatusView
	// DeleteTasks 删除任务记录；force=true 同时删文件。
	DeleteTasks(ids []string, force bool) error
}

// TaskStatusView 只含 job 需要的状态。
type TaskStatusView struct {
	ID     string
	Status base.Status
	Path   string // 完成后的文件路径
	Name   string
}

// JobStatus 视频下载 job 生命周期。
type JobStatus string

const (
	JobPending    JobStatus = "pending"
	JobDownloading JobStatus = "downloading"
	JobMerging    JobStatus = "merging"
	JobDone       JobStatus = "done"
	JobError      JobStatus = "error"
)

// Label keys 写入 Request.Labels，便于任务列表识别。
const (
	LabelJobID = "video.jobId"
	LabelRole  = "video.role" // video | audio
)

// Job 一个视频下载+合并任务。
type Job struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Status      JobStatus `json:"status"`
	Error       string    `json:"error,omitempty"`
	FormatID    string    `json:"formatId"`
	OutputPath  string    `json:"outputPath"`
	TaskIDs     []string  `json:"taskIds"`
	VideoTaskID string    `json:"videoTaskId,omitempty"`
	AudioTaskID string    `json:"audioTaskId,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Service 视频站下载服务。
type Service struct {
	bins      *Binaries
	installer *Installer
	creator   TaskCreator
	store     JobStore
	runner    Runner
	mergeFF   func(ctx context.Context, ffmpeg, videoPath, audioPath, outPath string) error

	mu   sync.Mutex
	jobs map[string]*Job
	stop chan struct{}
	wg   sync.WaitGroup

	// pollInterval 任务状态轮询间隔
	pollInterval time.Duration
	// CleanupTmpTasks 合并完成后是否删除临时下载任务记录
	CleanupTmpTasks bool
}

// NewService 创建服务。runner/mergeFF 可为 nil 用默认实现；store 为 nil 则不持久化。
func NewService(bins *Binaries, creator TaskCreator, runner Runner, store JobStore) *Service {
	if store == nil {
		store = nopStore{}
	}
	s := &Service{
		bins:            bins,
		installer:       NewInstaller(bins),
		creator:         creator,
		store:           store,
		runner:          runner,
		jobs:            make(map[string]*Job),
		stop:            make(chan struct{}),
		pollInterval:    time.Second,
		mergeFF:         defaultMerge,
		CleanupTmpTasks: true,
	}
	// 恢复历史 job
	if loaded, err := store.LoadAll(); err == nil {
		for _, j := range loaded {
			if j == nil || j.ID == "" {
				continue
			}
			// 中断的 downloading/merging 标记为 error，避免永远卡住
			if j.Status == JobDownloading || j.Status == JobMerging || j.Status == JobPending {
				j.Status = JobError
				if j.Error == "" {
					j.Error = "interrupted by restart"
				}
				j.UpdatedAt = time.Now()
				_ = store.Save(j)
			}
			s.jobs[j.ID] = j
		}
	}
	s.wg.Add(1)
	go s.pollLoop()
	return s
}

// Installer 返回受管组件安装器。
func (s *Service) Installer() *Installer { return s.installer }

// Close 停止轮询。
func (s *Service) Close() {
	close(s.stop)
	s.wg.Wait()
}

// Resolve 解析视频元数据。
func (s *Service) Resolve(ctx context.Context, pageURL string) (*VideoInfo, error) {
	bin := s.bins.YtDlpPath()
	if bin == "" {
		return nil, ErrBinariesMissing([]string{"yt-dlp"})
	}
	return Parse(ctx, bin, pageURL, s.runner)
}

// Bins 返回二进制定位器。
func (s *Service) Bins() *Binaries { return s.bins }

// DownloadRequest 创建下载 job 的入参。
type DownloadRequest struct {
	URL      string `json:"url"`
	FormatID string `json:"formatId"`
	Title    string `json:"title,omitempty"`
	Path     string `json:"path"`
	Name     string `json:"name,omitempty"`
}

// Download 创建 job：解析选中 format → 创建 1~2 个 HTTP 任务。
func (s *Service) Download(ctx context.Context, req *DownloadRequest) (*Job, error) {
	if req.URL == "" || req.FormatID == "" || req.Path == "" {
		return nil, fmt.Errorf("url, formatId and path are required")
	}
	info, err := s.Resolve(ctx, req.URL)
	if err != nil {
		return nil, err
	}
	var opt *FormatOption
	for i := range info.Formats {
		if info.Formats[i].ID == req.FormatID {
			opt = &info.Formats[i]
			break
		}
	}
	if opt == nil {
		return nil, fmt.Errorf("format %s not found", req.FormatID)
	}

	jobID, _ := gonanoid.New()
	title := req.Title
	if title == "" {
		title = info.Title
	}
	outName := req.Name
	if outName == "" {
		outName = sanitizeFilename(title) + "." + opt.Ext
	}

	job := &Job{
		ID:         jobID,
		Title:      title,
		Status:     JobPending,
		FormatID:   req.FormatID,
		OutputPath: filepath.Join(req.Path, outName),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// video 任务
	videoName := sanitizeFilename(title) + ".video.tmp"
	vid, err := s.creator.CreateDirect(
		&base.Request{
			URL:    opt.VideoURL,
			Extra:  httpExtra(opt.VideoHeaders),
			Labels: map[string]string{
				LabelJobID: jobID,
				LabelRole:  "video",
			},
		},
		&base.Options{Path: req.Path, Name: videoName},
	)
	if err != nil {
		return nil, fmt.Errorf("create video task: %w", err)
	}
	job.VideoTaskID = vid
	job.TaskIDs = append(job.TaskIDs, vid)

	// audio 任务（分离流）
	if opt.AudioURL != "" {
		audioName := sanitizeFilename(title) + ".audio.tmp"
		aid, err := s.creator.CreateDirect(
			&base.Request{
				URL:    opt.AudioURL,
				Extra:  httpExtra(opt.AudioHeaders),
				Labels: map[string]string{
					LabelJobID: jobID,
					LabelRole:  "audio",
				},
			},
			&base.Options{Path: req.Path, Name: audioName},
		)
		if err != nil {
			return nil, fmt.Errorf("create audio task: %w", err)
		}
		job.AudioTaskID = aid
		job.TaskIDs = append(job.TaskIDs, aid)
	}

	job.Status = JobDownloading
	job.UpdatedAt = time.Now()

	s.mu.Lock()
	s.jobs[jobID] = job
	s.mu.Unlock()
	_ = s.store.Save(cloneJob(job))
	return cloneJob(job), nil
}

// GetJob 查询 job。
func (s *Service) GetJob(id string) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	return cloneJob(j), true
}

// ListJobs 列出全部 job（新→旧）。
func (s *Service) ListJobs() []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, cloneJob(j))
	}
	// 简单按 CreatedAt 降序
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && out[k].CreatedAt.After(out[k-1].CreatedAt); k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out
}

func (s *Service) pollLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Service) tick() {
	s.mu.Lock()
	var pending []*Job
	for _, j := range s.jobs {
		if j.Status == JobDownloading || j.Status == JobMerging {
			pending = append(pending, j)
		}
	}
	s.mu.Unlock()

	for _, j := range pending {
		s.advance(j)
	}
}

func (s *Service) advance(j *Job) {
	s.mu.Lock()
	if j.Status != JobDownloading {
		s.mu.Unlock()
		return
	}
	videoID := j.VideoTaskID
	audioID := j.AudioTaskID
	outPath := j.OutputPath
	s.mu.Unlock()

	v := s.creator.GetTask(videoID)
	if v == nil {
		return
	}
	if v.Status == base.DownloadStatusError {
		s.setJobError(j.ID, "video task failed")
		return
	}
	if v.Status != base.DownloadStatusDone {
		return
	}

	videoPath := v.Path
	audioPath := ""
	if audioID != "" {
		a := s.creator.GetTask(audioID)
		if a == nil {
			return
		}
		if a.Status == base.DownloadStatusError {
			s.setJobError(j.ID, "audio task failed")
			return
		}
		if a.Status != base.DownloadStatusDone {
			return
		}
		audioPath = a.Path
	}

	// 两个任务都完成 → 合并
	s.mu.Lock()
	if j.Status != JobDownloading {
		s.mu.Unlock()
		return
	}
	j.Status = JobMerging
	j.UpdatedAt = time.Now()
	jobID := j.ID
	taskIDs := append([]string(nil), j.TaskIDs...)
	s.mu.Unlock()
	_ = s.store.Save(cloneJob(j))

	go func() {
		if err := s.doMerge(jobID, videoPath, audioPath, outPath); err != nil {
			s.setJobError(jobID, err.Error())
			return
		}
		s.mu.Lock()
		var snap *Job
		if jj, ok := s.jobs[jobID]; ok {
			jj.Status = JobDone
			jj.UpdatedAt = time.Now()
			snap = cloneJob(jj)
		}
		s.mu.Unlock()
		if snap != nil {
			_ = s.store.Save(snap)
		}
		// 合并成功后清理临时下载任务（保留最终输出文件）
		if s.CleanupTmpTasks && len(taskIDs) > 0 && s.creator != nil {
			_ = s.creator.DeleteTasks(taskIDs, false)
		}
	}()
}

func (s *Service) doMerge(jobID, videoPath, audioPath, outPath string) error {
	if audioPath == "" {
		// progressive：直接改名/移动
		if videoPath == outPath {
			return nil
		}
		// 复制并删除临时
		return moveFile(videoPath, outPath)
	}
	ff := s.bins.FfmpegPath()
	if ff == "" {
		return ErrBinariesMissing([]string{"ffmpeg"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := s.mergeFF(ctx, ff, videoPath, audioPath, outPath); err != nil {
		return err
	}
	// 清理临时分片
	_ = os.Remove(videoPath)
	_ = os.Remove(audioPath)
	return nil
}

func (s *Service) setJobError(id, msg string) {
	s.mu.Lock()
	var snap *Job
	if j, ok := s.jobs[id]; ok {
		j.Status = JobError
		j.Error = msg
		j.UpdatedAt = time.Now()
		snap = cloneJob(j)
	}
	s.mu.Unlock()
	if snap != nil {
		_ = s.store.Save(snap)
	}
}

func defaultMerge(ctx context.Context, ffmpeg, videoPath, audioPath, outPath string) error {
	cmd := exec.CommandContext(ctx, ffmpeg, "-y",
		"-i", videoPath,
		"-i", audioPath,
		"-c", "copy",
		"-movflags", "+faststart",
		outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg merge failed: %w: %s", err, truncate(string(out), 300))
	}
	return nil
}

func cloneJob(j *Job) *Job {
	c := *j
	c.TaskIDs = append([]string(nil), j.TaskIDs...)
	return &c
}

// httpExtra 把 yt-dlp 下载头转成 HTTP fetcher 的 ReqExtra。
func httpExtra(headers map[string]string) any {
	if len(headers) == 0 {
		return nil
	}
	// 只保留对引擎有用的头，避免 Cookie/UA 过长或冲突
	filtered := make(map[string]string, len(headers))
	for k, v := range headers {
		lk := strings.ToLower(k)
		switch lk {
		case "referer", "user-agent", "origin", "cookie":
			filtered[k] = v
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &fhttp.ReqExtra{Header: filtered}
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "video"
	}
	repl := strings.NewReplacer(
		"<", "_", ">", "_", ":", "_", "\"", "_",
		"/", "_", "\\", "_", "|", "_", "?", "_", "*", "_",
	)
	s = repl.Replace(s)
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// 跨卷 fallback：复制
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return err
	}
	return os.Remove(src)
}

// MarshalJob 便于测试。
func MarshalJob(j *Job) string {
	b, _ := json.Marshal(j)
	return string(b)
}
