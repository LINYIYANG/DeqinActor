package actor

import (
	"sync/atomic"
	"time"
)

// TokenBucket 实现了一个无锁的令牌桶限流器。
// 令牌桶算法允许一定程度的突发流量，同时限制长期平均速率。
// QPS 可以通过 SetQPS 动态调整，无需重新创建限流器。
//
// 工作原理：
//   - 令牌以固定速率（QPS）生成，放入桶中
//   - 桶有最大容量（burst），超出部分丢弃
//   - 每个请求消耗一个或多个令牌
//   - 令牌不足时请求被拒绝或等待
type TokenBucket struct {
	// rate 每秒生成的令牌数（QPS）
	rate atomic.Int64
	// burst 桶的最大容量，允许的突发流量大小
	burst int64
	// tokens 当前桶中的令牌数
	tokens atomic.Int64
	// lastNS 上次填充令牌的时间（纳秒）
	lastNS atomic.Int64
}

// NewTokenBucket 创建一个新的令牌桶限流器。
// qps 为每秒生成的令牌数，burst 为桶的最大容量。
// 当 burst <= 0 时，默认使用 qps 作为 burst（或 1 当 qps 也 <= 0）。
func NewTokenBucket(qps int64, burst int64) *TokenBucket {
	if burst <= 0 {
		burst = qps
		if burst <= 0 {
			burst = 1
		}
	}
	tb := &TokenBucket{burst: burst}
	tb.rate.Store(qps)
	tb.tokens.Store(burst)
	tb.lastNS.Store(time.Now().UnixNano())
	return tb
}

// SetQPS 动态更新限流器的速率。
// 当 qps <= 0 时，限流实际上被禁用（总是允许）。
func (tb *TokenBucket) SetQPS(qps int64) {
	tb.refill(time.Now().UnixNano())
	tb.rate.Store(qps)
	if qps <= 0 {
		tb.tokens.Store(tb.burst)
	}
}

// Allow 尝试消耗 n 个令牌，返回是否成功。
// 如果桶中令牌不足，立即返回 false，不阻塞。
func (tb *TokenBucket) Allow(n int64) bool {
	if n <= 0 {
		return true
	}
	now := time.Now().UnixNano()
	tb.refill(now)
	for {
		cur := tb.tokens.Load()
		if cur < n {
			return false
		}
		if tb.tokens.CompareAndSwap(cur, cur-n) {
			return true
		}
	}
}

// Wait 阻塞直到可以消耗 n 个令牌。
// 通过自旋和短暂休眠实现等待。
func (tb *TokenBucket) Wait(n int64) {
	if n <= 0 {
		return
	}
	for {
		if tb.Allow(n) {
			return
		}
		time.Sleep(200 * time.Microsecond)
	}
}

// refill 根据经过的时间补充令牌。
// 使用 CAS 操作确保并发安全，无锁实现。
func (tb *TokenBucket) refill(nowNS int64) {
	r := tb.rate.Load()
	if r <= 0 {
		tb.lastNS.Store(nowNS)
		tb.tokens.Store(tb.burst)
		return
	}
	last := tb.lastNS.Load()
	if nowNS <= last {
		return
	}
	elapsed := nowNS - last
	add := (elapsed * r) / int64(time.Second)
	if add <= 0 {
		return
	}
	if !tb.lastNS.CompareAndSwap(last, nowNS) {
		return
	}
	for {
		cur := tb.tokens.Load()
		next := cur + add
		if next > tb.burst {
			next = tb.burst
		}
		if tb.tokens.CompareAndSwap(cur, next) {
			return
		}
	}
}
