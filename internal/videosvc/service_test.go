package videosvc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GopeedLab/gopeed/pkg/base"
)

func mockYtDlpJSON() []byte {
	m := map[string]any{
		"id":         "abc123",
		"title":      "测试视频 Title",
		"thumbnail":  "https://img.example/t.jpg",
		"duration":   120.5,
		"webpage_url": "https://www.bilibili.com/video/BV1xx",
		"formats": []map[string]any{
			{
				"format_id": "16",
				"ext":       "mp4",
				"height":    360,
				"width":     640,
				"vcodec":    "avc1.64001e",
				"acodec":    "mp4a.40.2",
				"url":       "https://cdn.example/360.mp4",
				"filesize":  1000,
			},
			{
				"format_id": "32",
				"ext":       "mp4",
				"height":    720,
				"width":     1280,
				"vcodec":    "avc1.64001f",
				"acodec":    "mp4a.40.2",
				"url":       "https://cdn.example/720.mp4",
				"filesize":  5000,
			},
			{
				"format_id": "137",
				"ext":       "mp4",
				"height":    1080,
				"width":     1920,
				"vcodec":    "avc1.640028",
				"acodec":    "none",
				"url":       "https://cdn.example/1080v.mp4",
				"filesize":  8000,
			},
			{
				"format_id": "140",
				"ext":       "m4a",
				"vcodec":    "none",
				"acodec":    "mp4a.40.2",
				"url":       "https://cdn.example/audio.m4a",
				"filesize":  1000,
				"tbr":       128,
			},
		},
	}
	b, _ := json.Marshal(m)
	return b
}

func TestParseAggregateFormats(t *testing.T) {
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return mockYtDlpJSON(), nil
	}
	info, err := Parse(context.Background(), "yt-dlp", "https://www.bilibili.com/video/BV1xx", runner)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "测试视频 Title" {
		t.Fatalf("title = %s", info.Title)
	}
	// progressive: 720, 360；video-only: 1080 + audio
	if len(info.Formats) < 3 {
		t.Fatalf("formats = %d, want >= 3: %+v", len(info.Formats), info.Formats)
	}
	// 1080 分离流应带 + 号
	found1080 := false
	for _, f := range info.Formats {
		if f.Height == 1080 {
			found1080 = true
			if !strings.Contains(f.ID, "+") {
				t.Fatalf("1080 id should be video+audio, got %s", f.ID)
			}
			if f.AudioURL == "" {
				t.Fatal("1080 should have audio url")
			}
			if f.Size != 9000 {
				t.Fatalf("1080 size = %d, want 9000", f.Size)
			}
		}
		if f.Height == 720 {
			if f.AudioURL != "" {
				t.Fatal("progressive should not have audio url")
			}
		}
	}
	if !found1080 {
		t.Fatal("missing 1080 option")
	}
}

func TestParseBinariesMissing(t *testing.T) {
	_, err := Parse(context.Background(), "", "http://x", nil)
	if err == nil {
		t.Fatal("expected error when yt-dlp empty")
	}
}

func TestSplitFormatID(t *testing.T) {
	v, a := SplitFormatID("137+140")
	if v != "137" || a != "140" {
		t.Fatalf("got %s %s", v, a)
	}
	v, a = SplitFormatID("18")
	if v != "18" || a != "" {
		t.Fatalf("got %s %s", v, a)
	}
}

// mockCreator 实现 TaskCreator
type mockCreator struct {
	tasks map[string]*TaskStatusView
	seq   int
}

func newMockCreator() *mockCreator {
	return &mockCreator{tasks: map[string]*TaskStatusView{}}
}

func (m *mockCreator) CreateDirect(req *base.Request, opts *base.Options) (string, error) {
	m.seq++
	id := fmt.Sprintf("task-%d", m.seq)
	m.tasks[id] = &TaskStatusView{
		ID:     id,
		Status: base.DownloadStatusReady,
		Path:   opts.Path + "/" + opts.Name,
		Name:   opts.Name,
	}
	return id, nil
}

func (m *mockCreator) GetTask(id string) *TaskStatusView {
	return m.tasks[id]
}

func (m *mockCreator) DeleteTasks(ids []string, force bool) error {
	for _, id := range ids {
		delete(m.tasks, id)
	}
	return nil
}

func (m *mockCreator) finish(id string) {
	if t, ok := m.tasks[id]; ok {
		t.Status = base.DownloadStatusDone
	}
}

func TestServiceDownloadAndMerge(t *testing.T) {
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return mockYtDlpJSON(), nil
	}
	creator := newMockCreator()
	bins := NewBinaries(t.TempDir())
	bins.YtDlp = "yt-dlp-mock"
	bins.Ffmpeg = "ffmpeg-mock" // 不会真正执行，用 mock merge

	svc := NewService(bins, creator, runner, nil)
	defer svc.Close()
	svc.pollInterval = 20 * time.Millisecond

	merged := make(chan struct{}, 1)
	svc.mergeFF = func(ctx context.Context, ffmpeg, v, a, out string) error {
		select {
		case merged <- struct{}{}:
		default:
		}
		return nil
	}

	job, err := svc.Download(context.Background(), &DownloadRequest{
		URL:      "https://www.bilibili.com/video/BV1xx",
		FormatID: "137+140",
		Path:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobDownloading {
		t.Fatalf("status = %s", job.Status)
	}
	if len(job.TaskIDs) != 2 {
		t.Fatalf("tasks = %v", job.TaskIDs)
	}

	// 完成两个任务
	creator.finish(job.VideoTaskID)
	creator.finish(job.AudioTaskID)

	select {
	case <-merged:
	case <-time.After(3 * time.Second):
		t.Fatal("merge not called")
	}

	// 等 job 变 done
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := svc.GetJob(job.ID)
		if j != nil && j.Status == JobDone {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	j, _ := svc.GetJob(job.ID)
	t.Fatalf("job not done, status=%v err=%v", j.Status, j.Error)
}

func TestServiceProgressiveMove(t *testing.T) {
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return mockYtDlpJSON(), nil
	}
	creator := newMockCreator()
	bins := NewBinaries("")
	bins.YtDlp = "yt-dlp-mock"
	svc := NewService(bins, creator, runner, nil)
	defer svc.Close()
	svc.pollInterval = 20 * time.Millisecond

	dir := t.TempDir()
	job, err := svc.Download(context.Background(), &DownloadRequest{
		URL:      "https://www.bilibili.com/video/BV1xx",
		FormatID: "32",
		Path:     dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(job.TaskIDs) != 1 {
		t.Fatalf("progressive should have 1 task, got %v", job.TaskIDs)
	}

	// 写临时文件再标记完成，验证 move
	tmp := creator.tasks[job.VideoTaskID].Path
	if err := writeFile(tmp, "video-bytes"); err != nil {
		t.Fatal(err)
	}
	creator.finish(job.VideoTaskID)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := svc.GetJob(job.ID)
		if j != nil && j.Status == JobDone {
			if !fileExists(j.OutputPath) {
				t.Fatalf("output missing: %s", j.OutputPath)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	j, _ := svc.GetJob(job.ID)
	t.Fatalf("job not done: %+v", j)
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func TestFileJobStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewFileJobStore(dir)
	j := &Job{
		ID:         "j1",
		Title:      "t",
		Status:     JobDone,
		OutputPath: filepath.Join(dir, "out.mp4"),
		TaskIDs:    []string{"a", "b"},
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := store.Save(j); err != nil {
		t.Fatal(err)
	}
	list, err := store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "j1" || list[0].Status != JobDone {
		t.Fatalf("load = %+v", list)
	}
	// 重启恢复：interrupted 状态应标 error
	bins := NewBinaries("")
	creator := newMockCreator()
	pending := &Job{ID: "j2", Status: JobDownloading, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Save(pending); err != nil {
		t.Fatal(err)
	}
	svc := NewService(bins, creator, nil, store)
	defer svc.Close()
	got, ok := svc.GetJob("j2")
	if !ok || got.Status != JobError {
		t.Fatalf("restored pending job = %+v ok=%v", got, ok)
	}
	got1, ok1 := svc.GetJob("j1")
	if !ok1 || got1.Status != JobDone {
		t.Fatalf("restored done job = %+v ok=%v", got1, ok1)
	}
}
