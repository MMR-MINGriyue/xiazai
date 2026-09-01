package sftp

import (
	"net/url"
	"path"

	"github.com/GopeedLab/gopeed/internal/fetcher"
)

type Manager struct{}

func (m *Manager) Name() string {
	return "sftp"
}

func (m *Manager) Filters() []*fetcher.SchemeFilter {
	return []*fetcher.SchemeFilter{
		{Type: fetcher.FilterTypeUrl, Pattern: "sftp"},
	}
}

func (m *Manager) Build() fetcher.Fetcher {
	return &Fetcher{}
}

func (m *Manager) ParseName(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return path.Base(pu.Path)
}

func (m *Manager) AutoRename() bool {
	return false
}

func (m *Manager) DefaultConfig() any {
	return &config{Connections: 4}
}

func (m *Manager) Store(f fetcher.Fetcher) (any, error) {
	return f.(*Fetcher).data, nil
}

func (m *Manager) Restore() (v any, f func(meta *fetcher.FetcherMeta, v any) fetcher.Fetcher) {
	return &fetcherData{}, func(meta *fetcher.FetcherMeta, v any) fetcher.Fetcher {
		return &Fetcher{
			meta: meta,
			data: v.(*fetcherData),
		}
	}
}

func (m *Manager) Close() error {
	return nil
}
