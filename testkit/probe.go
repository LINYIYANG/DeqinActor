package testkit

import (
	"testing"
	"time"
)

// Probe 是一个测试探针，用于在测试中接收和验证消息。
// 它提供了一个通道来接收消息，以及便捷的方法来等待和验证消息。
// Probe 常用于测试 Actor 之间的消息传递。
type Probe struct {
	// t 测试上下文，用于报告失败
	t testing.TB
	// ch 接收消息的通道
	ch chan any
	// fail 失败处理函数
	fail func(string, ...any)
}

// NewProbe 创建一个新的测试探针。
// t 为测试上下文，buffer 为通道缓冲区大小（默认 1024）。
func NewProbe(t testing.TB, buffer int) *Probe {
	if buffer <= 0 {
		buffer = 1024
	}
	p := &Probe{t: t, ch: make(chan any, buffer)}
	p.fail = t.Fatalf
	return p
}

// Chan 返回消息接收通道。
// 可以直接用于 select 语句或与其他通道操作。
func (p *Probe) Chan() <-chan any { return p.ch }

// Put 向探针发送一条消息。
// 通常在 Actor 的消息处理函数中调用，将消息转发到探针。
func (p *Probe) Put(v any) { p.ch <- v }

// Expect 等待并返回一条消息。
// 如果在超时时间内没有收到消息，测试会失败。
// 默认超时为 1 秒。
func (p *Probe) Expect(timeout time.Duration) any {
	p.t.Helper()
	if timeout <= 0 {
		timeout = time.Second
	}
	select {
	case v := <-p.ch:
		return v
	case <-time.After(timeout):
		p.fail("timeout waiting message")
		return nil
	}
}

// ExpectNoMessage 验证在指定时间内没有收到消息。
// 如果收到消息，测试会失败。
// 默认超时为 50 毫秒。
func (p *Probe) ExpectNoMessage(timeout time.Duration) {
	p.t.Helper()
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	select {
	case v := <-p.ch:
		p.fail("unexpected message: %#v", v)
	case <-time.After(timeout):
	}
}
