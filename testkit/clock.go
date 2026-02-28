package testkit

import (
	"sync"
	"time"
)

// FakeClock 是一个模拟时钟，用于测试时间相关的逻辑。
// 它允许测试代码控制时间的流逝，而不需要实际等待。
// 这对于测试超时、重试、定时任务等场景非常有用。
type FakeClock struct {
	// mu 保护并发访问
	mu sync.Mutex
	// now 当前模拟时间
	now time.Time
	// tmrs 待触发的定时器列表
	tmrs []*fakeTimer
}

// fakeTimer 是一个模拟定时器。
type fakeTimer struct {
	// at 定时器触发时间
	at time.Time
	// ch 触发时发送时间的通道
	ch chan time.Time
}

// NewFakeClock 创建一个新的模拟时钟。
// start 为初始时间，如果为零值则使用 Unix 纪元。
func NewFakeClock(start time.Time) *FakeClock {
	if start.IsZero() {
		start = time.Unix(0, 0)
	}
	return &FakeClock{now: start}
}

// Now 返回当前模拟时间。
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After 返回一个通道，在指定持续时间后接收当前时间。
// 与 time.After 类似，但使用模拟时间。
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	t := &fakeTimer{at: c.now.Add(d), ch: ch}
	c.tmrs = append(c.tmrs, t)
	return ch
}

// Advance 推进模拟时间。
// 会触发所有到期的定时器，向它们的通道发送当前时间。
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var left []*fakeTimer
	var fire []*fakeTimer
	for _, t := range c.tmrs {
		if !t.at.After(now) {
			fire = append(fire, t)
		} else {
			left = append(left, t)
		}
	}
	c.tmrs = left
	c.mu.Unlock()
	for _, t := range fire {
		select {
		case t.ch <- now:
		default:
		}
		close(t.ch)
	}
}
