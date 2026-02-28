package actor

import (
	"sync/atomic"
	"time"
)

// breakerState 定义断路器的三种状态。
type breakerState uint32

const (
	// breakerClosed 关闭状态：请求正常通过
	breakerClosed breakerState = iota
	// breakerOpen 打开状态：请求被拒绝，等待超时后进入半开状态
	breakerOpen
	// breakerHalfOpen 半开状态：允许一个探测请求通过，成功则关闭，失败则重新打开
	breakerHalfOpen
)

// CircuitBreaker 实现了一个基于失败计数的断路器。
// 断路器用于防止对失败服务的持续请求，实现快速失败和故障隔离。
//
// 状态转换：
//   - closed -> open: 当连续失败次数达到阈值
//   - open -> half-open: 当打开持续时间超过 openFor
//   - half-open -> closed: 探测请求成功
//   - half-open -> open: 探测请求失败
type CircuitBreaker struct {
	// failures 连续失败计数
	failures atomic.Uint64
	// state 当前状态
	state atomic.Uint32
	// openedAtUnix 断路器打开的时间（纳秒）
	openedAtUnix atomic.Int64
	// halfOpenProbe 半开状态下是否已有探测请求
	halfOpenProbe atomic.Bool

	// threshold 触发打开的失败次数阈值
	threshold uint64
	// openFor 打开状态的持续时间
	openFor time.Duration
}

// NewCircuitBreaker 创建一个新的断路器。
// 当 threshold 或 openFor 为零时，使用默认值（threshold=50, openFor=30s）。
func NewCircuitBreaker(threshold uint64, openFor time.Duration) *CircuitBreaker {
	if threshold == 0 {
		threshold = 50
	}
	if openFor == 0 {
		openFor = 30 * time.Second
	}
	cb := &CircuitBreaker{threshold: threshold, openFor: openFor}
	cb.state.Store(uint32(breakerClosed))
	return cb
}

// Allow 检查在给定时间是否允许请求通过。
// 在关闭状态下总是允许；在打开状态下拒绝直到超时；
// 在半开状态下只允许一个探测请求。
func (b *CircuitBreaker) Allow(now time.Time) bool {
	st := breakerState(b.state.Load())
	switch st {
	case breakerClosed:
		return true
	case breakerOpen:
		opened := time.Unix(0, b.openedAtUnix.Load())
		if now.Sub(opened) >= b.openFor {
			if b.state.CompareAndSwap(uint32(breakerOpen), uint32(breakerHalfOpen)) {
				b.halfOpenProbe.Store(false)
			}
			st = breakerHalfOpen
		} else {
			return false
		}
		fallthrough
	case breakerHalfOpen:
		return b.halfOpenProbe.CompareAndSwap(false, true)
	default:
		return false
	}
}

// OnSuccess 记录一次成功，将断路器重置为关闭状态。
func (b *CircuitBreaker) OnSuccess() {
	b.failures.Store(0)
	b.state.Store(uint32(breakerClosed))
	b.halfOpenProbe.Store(false)
}

// OnFailure 记录一次失败，可能导致断路器打开。
// 在半开状态下失败会立即重新打开断路器。
func (b *CircuitBreaker) OnFailure(now time.Time) {
	if breakerState(b.state.Load()) == breakerHalfOpen {
		b.open(now)
		return
	}
	if b.failures.Add(1) >= b.threshold {
		b.open(now)
	}
}

// open 将断路器切换到打开状态。
func (b *CircuitBreaker) open(now time.Time) {
	b.openedAtUnix.Store(now.UnixNano())
	b.state.Store(uint32(breakerOpen))
	b.halfOpenProbe.Store(false)
}
