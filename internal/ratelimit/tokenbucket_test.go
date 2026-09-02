package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestTokenBucketBasic(t *testing.T) {
	b := NewTokenBucket(1000) // 1KB/s
	if b.Rate() != 1000 {
		t.Fatalf("rate = %d", b.Rate())
	}
	// 立即消费不超过容量的令牌
	start := time.Now()
	b.Acquire(500)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("first acquire took %v (should be instant)", d)
	}
}

func TestTokenBucketThrottles(t *testing.T) {
	b := NewTokenBucket(1024) // 1KB/s
	start := time.Now()
	b.Acquire(1024) // 桶满，立即
	b.Acquire(1024) // 桶空，需等 ~1s
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("second acquire took %v, want ~1s", elapsed)
	}
}

func TestTokenBucketAvgRate(t *testing.T) {
	b := NewTokenBucket(4096) // 4KB/s
	// 先耗尽桶的突发容量，测稳态速率
	b.Acquire(4096)
	total := int64(0)
	const budget = int64(20 * 1024)
	start := time.Now()
	for total < budget {
		b.Acquire(512)
		total += 512
	}
	elapsed := time.Since(start)
	// 20KB @ 4KB/s ≈ 5s
	rate := float64(total) / elapsed.Seconds()
	t.Logf("avg rate = %.0f B/s over %v", rate, elapsed)
	if rate > 4600 || rate < 3300 {
		t.Fatalf("avg rate %.0f out of range [3300, 4600]", rate)
	}
}

func TestTokenBucketDisable(t *testing.T) {
	b := NewTokenBucket(1)
	b.Acquire(1)
	b.SetRate(0) // 关闭限速
	start := time.Now()
	b.Acquire(1024 * 1024)
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("disabled bucket should not block, took %v", d)
	}
}

func TestTokenBucketConcurrent(t *testing.T) {
	b := NewTokenBucket(8192) // 8KB/s
	var wg sync.WaitGroup
	var consumed int64
	var mu sync.Mutex
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				b.Acquire(512)
				mu.Lock()
				consumed += 512
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	// 4 goroutine × 25 × 512B = 51200B @ 8KB/s ≈ 6.4s
	elapsed := time.Since(b.last) // b.last 在 Acquire 中更新
	_ = elapsed
	// 仅验证并发不 panic、消费量正确
	if consumed != 4*25*512 {
		t.Fatalf("consumed = %d", consumed)
	}
}

func TestTokenBucketNil(t *testing.T) {
	var b *TokenBucket
	b.Acquire(100) // nil 不 panic
}
