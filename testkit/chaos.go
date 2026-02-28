package testkit

import (
	"math/rand"
	"time"
)

// Chaos 是一个混沌测试工具，用于模拟网络故障和延迟。
// 它可以随机丢弃操作或添加延迟，帮助测试系统在异常情况下的行为。
// 这对于测试重试逻辑、超时处理和容错机制非常有用。
type Chaos struct {
	// DropProbability 操作被丢弃的概率（0.0-1.0）
	DropProbability float64
	// MaxDelay 最大随机延迟
	MaxDelay time.Duration
	// Rand 随机数生成器（可选，默认使用时间种子）
	Rand *rand.Rand
}

// Apply 应用混沌效果到给定的函数。
// 根据配置的概率和延迟，可能：
//   - 丢弃操作（返回 false）
//   - 添加随机延迟后执行（返回 true）
//   - 直接执行（返回 true）
//
// 返回值表示操作是否被执行。
func (c Chaos) Apply(fn func()) bool {
	r := c.Rand
	if r == nil {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if c.DropProbability > 0 && r.Float64() < c.DropProbability {
		return false
	}
	if c.MaxDelay > 0 {
		time.Sleep(time.Duration(r.Int63n(int64(c.MaxDelay))))
	}
	fn()
	return true
}
