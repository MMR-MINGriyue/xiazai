package sftp

import (
	"testing"

	"github.com/GopeedLab/gopeed/internal/fetcher"
)

func TestManagerBasics(t *testing.T) {
	m := &Manager{}
	if m.Name() != "sftp" {
		t.Fatalf("name = %s", m.Name())
	}
	filters := m.Filters()
	if len(filters) != 1 || !filters[0].Match("sftp://host/file") {
		t.Fatal("filter should match sftp:// urls")
	}
	if filters[0].Match("ftp://host/file") {
		t.Fatal("filter should not match ftp:// urls")
	}
	if m.ParseName("sftp://host:22/a/b/file.zip") != "file.zip" {
		t.Fatalf("parseName = %s", m.ParseName("sftp://host:22/a/b/file.zip"))
	}
	if !m.AutoRename() {
		t.Fatal("autoRename should be true (avoid overwrite)")
	}
	cfg := m.DefaultConfig().(*config)
	if cfg.Connections != 4 {
		t.Fatalf("default connections = %d", cfg.Connections)
	}
}

func TestManagerStoreRestore(t *testing.T) {
	m := &Manager{}
	f1 := &Fetcher{}
	f1.Setup(nil)
	f1.data.Chunks = splitChunks(2*1024*1024, 2) // ≥1MB 才会真正切成多段
	f1.data.Chunks[0].Downloaded = 100

	v, err := m.Store(f1)
	if err != nil {
		t.Fatal(err)
	}
	data, ok := v.(*fetcherData)
	if !ok || len(data.Chunks) != 2 || data.Chunks[0].Downloaded != 100 {
		t.Fatalf("store returned %+v", v)
	}

	// Restore：模拟从存储反序列化（v 已是默认零值或旧数据）
	zero, build := m.Restore()
	if zero == nil || build == nil {
		t.Fatal("restore returned nil")
	}
	f2 := build(nil, zero)
	if _, ok := f2.(*Fetcher); !ok {
		t.Fatal("build should return *Fetcher")
	}
	// v 注入旧数据时 chunk 进度保留
	f3 := build(nil, v).(*Fetcher)
	if len(f3.data.Chunks) != 2 || f3.data.Chunks[0].Downloaded != 100 {
		t.Fatal("restored data mismatch")
	}
}

var _ = fetcher.FetcherManager((*Manager)(nil))
