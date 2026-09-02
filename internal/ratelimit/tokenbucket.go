// Package ratelimit 提供字节级令牌桶限速器，用于全局限速（多个下载任务共享）。
package ratelimit

import (
	"sync"
	"time"
)

// Limiter 是所有限速器的公共接口；nil 值表示不限速。
type Limiter interface {
	// Acquire 阻塞等待 n 字节的令牌可用（n<=0 或未限速时立即返回）。
	Acquire(n int64)
	// SetRate 动态调整速率（字节/秒；<=0 关闭限速）。
	SetRate(bytesPerSec int64)
	// Rate 返回当前速率（字节/秒；0 表示不限速）。
	Rate() int64
}

// TokenBucket 令牌桶：按 rate 字节/秒匀速补充，容量 capacity 支持突发。
// 并发安全（Acquire 内部短临界区 + 时间差 refill）。
type TokenBucket struct {
	mu       sync.Mutex
	rate     int64
	capacity float64
	tokens   float64
	last     time.Time
}

var _ Limiter = (*TokenBucket)(nil)

// NewTokenBucket 创建速率为 bytesPerSec 的桶；突发容量默认为 1 秒的令牌量，初始令牌为满。
func NewTokenBucket(bytesPerSec int64) *TokenBucket {
	t := &TokenBucket{last: time.Now()}
	t.SetRate(bytesPerSec)
	t.mu.Lock()
	t.tokens = t.capacity
	t.mu.Unlock()
	return t
}

// SetRate 设置速率；<=0 表示不限速（Acquire 立即返回）。
func (t *TokenBucket) SetRate(bytesPerSec int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rate = bytesPerSec
	if t.rate > 0 {
		if t.capacity <= 0 {
			t.capacity = float64(t.rate)
		}
		if t.tokens > t.capacity {
			t.tokens = t.capacity
		}
	} else {
		t.capacity = 0
		t.tokens = 0
	}
}

// Rate 返回当前速率。
func (t *TokenBucket) Rate() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rate
}

// refill 按流逝时间补充令牌（调用方需持有锁）。
func (t *TokenBucket) refill(now time.Time) {
	if t.rate <= 0 {
		return
	}
	elapsed := now.Sub(t.last).Seconds()
	if elapsed <= 0 {
		return
	}
	t.last = now
	t.tokens += float64(t.rate) * elapsed
	if t.tokens > t.capacity {
		t.tokens = t.capacity
	}
}

// Acquire 阻塞等待 n 字节令牌。未限速或 n<=0 时立即返回。
func (t *TokenBucket) Acquire(n int64) {
	if t == nil || n <= 0 {
		return
	}
	for {
		t.mu.Lock()
		if t.rate <= 0 {
			t.mu.Unlock()
			return
		}
		now := time.Now()
		t.refill(now)
		if t.tokens >= float64(n) {
			t.tokens -= float64(n)
			t.mu.Unlock()
			return
		}
		// 计算还需等待的时间：剩余令牌缺口 / 速率
		need := float64(n) - t.tokens
		wait := time.Duration(need / float64(t.rate) * float64(time.Second))
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		t.mu.Unlock()
		time.Sleep(wait)
	}
}
