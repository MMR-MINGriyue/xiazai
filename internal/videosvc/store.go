package videosvc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// JobStore 视频 job 持久化接口。
type JobStore interface {
	Save(job *Job) error
	LoadAll() ([]*Job, error)
	Delete(id string) error
}

// FileJobStore 将 jobs 存为 JSON 文件（storageDir/video-jobs.json）。
type FileJobStore struct {
	mu   sync.Mutex
	path string
}

// NewFileJobStore 创建文件存储；dir 为空则不落盘（仅内存行为由 Service 兜底）。
func NewFileJobStore(dir string) *FileJobStore {
	if dir == "" {
		return nil
	}
	return &FileJobStore{path: filepath.Join(dir, "video-jobs.json")}
}

func (s *FileJobStore) load() (map[string]*Job, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*Job{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]*Job{}, nil
	}
	m := map[string]*Job{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *FileJobStore) persist(m map[string]*Job) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".part"
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *FileJobStore) Save(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	m[job.ID] = job
	return s.persist(m)
}

func (s *FileJobStore) LoadAll() ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]*Job, 0, len(m))
	for _, j := range m {
		out = append(out, j)
	}
	return out, nil
}

func (s *FileJobStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	delete(m, id)
	return s.persist(m)
}

// nopStore 不持久化。
type nopStore struct{}

func (nopStore) Save(*Job) error            { return nil }
func (nopStore) LoadAll() ([]*Job, error)   { return nil, nil }
func (nopStore) Delete(string) error        { return nil }
