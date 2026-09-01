package sftp

import (
	"testing"
)

func TestSplitChunks(t *testing.T) {
	tests := []struct {
		name    string
		size    int64
		conn    int
		wantLen int
		wantSum int64
	}{
		{"小文件单连接", 100, 4, 1, 100},
		{"整除", 4 * 1024 * 1024, 4, 4, 4 * 1024 * 1024},
		{"不整除", 4*1024*1024 + 1, 4, 4, 4*1024*1024 + 1},
		{"单连接", 1000, 1, 1, 1000},
		{"零连接兜底", 1000, 0, 1, 1000},
		{"空文件", 0, 4, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := splitChunks(tt.size, tt.conn)
			if len(chunks) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(chunks), tt.wantLen)
			}
			var sum int64
			var lastEnd int64 = -1
			for _, c := range chunks {
				if c.Begin != lastEnd+1 {
					t.Fatalf("chunk 不连续: begin=%d lastEnd=%d", c.Begin, lastEnd)
				}
				lastEnd = c.End
				sum += c.End - c.Begin + 1
			}
			if lastEnd != tt.size-1 {
				t.Fatalf("lastEnd = %d, want %d", lastEnd, tt.size-1)
			}
			if sum != tt.wantSum {
				t.Fatalf("sum = %d, want %d", sum, tt.wantSum)
			}
		})
	}
}
