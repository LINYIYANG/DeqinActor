package actor

import (
	"sync"
	"time"
)

// RestartStrategy 定义监督者在子 Actor 失败后的重启策略。
type RestartStrategy uint8

const (
	// OneForOne 仅重启失败的子 Actor，不影响其他子 Actor。
	// 适用于子 Actor 之间相互独立的场景。
	OneForOne RestartStrategy = iota
	// OneForAll 当任意子 Actor 失败时，重启所有子 Actor。
	// 适用于子 Actor 之间紧密耦合，需要保持一致状态的场景。
	OneForAll
	// RestForOne 重启失败的子 Actor 及其之后启动的所有子 Actor。
	// 适用于子 Actor 之间存在依赖关系，后启动的依赖先启动的场景。
	RestForOne
)

// BackoffFunc 计算给定重试次数（从 0 开始）的退避延迟。
// 用于在重启失败时避免立即重试，给系统恢复的时间。
type BackoffFunc func(retry int) time.Duration

// ExponentialBackoff 返回一个指数退避函数。
// 延迟从 base 开始，每次重试翻倍，最大不超过 max。
// 当 base 或 max 为零时，使用默认值（base=50ms, max=5s）。
func ExponentialBackoff(base, max time.Duration) BackoffFunc {
	if base <= 0 {
		base = 50 * time.Millisecond
	}
	if max <= 0 {
		max = 5 * time.Second
	}
	return func(retry int) time.Duration {
		d := base
		for i := 0; i < retry; i++ {
			d *= 2
			if d >= max {
				return max
			}
		}
		return d
	}
}

// ChildFactory 是创建子 Actor 的工厂函数。
// 接收 System 作为参数，返回一个新的 BaseActor 实例。
type ChildFactory func(sys *System) *BaseActor

// childSpec 描述子 Actor 的创建规范。
type childSpec struct {
	// factory 用于创建新 Actor 实例的工厂函数
	factory ChildFactory
	// name 子 Actor 的名称
	name string
}

// childEntry 跟踪子 Actor 的运行时状态。
type childEntry struct {
	// spec 子 Actor 的创建规范
	spec childSpec
	// actor 当前运行的 Actor 实例
	actor *BaseActor
	// retries 当前的重试次数
	retries int
}

// Supervisor 实现了 Actor 监督树模式。
// 它管理一组子 Actor，在子 Actor 失败时根据配置的策略进行重启。
// 监督者订阅 System 的失败通知，自动响应子 Actor 的 panic。
type Supervisor struct {
	// sys 所属的 Actor 系统
	sys *System
	// strategy 重启策略
	strategy RestartStrategy
	// maxRetries 最大重试次数，超过后放弃重启
	maxRetries int
	// backoff 退避函数，计算重试延迟
	backoff BackoffFunc

	// mu 保护 children 的并发访问
	mu sync.Mutex
	// children 子 Actor 列表，按启动顺序排列
	children []childEntry

	// restartsMu 保护 restarts 计数
	restartsMu sync.Mutex
	// restarts 总重启次数
	restarts uint64
}

// SupervisorOptions 配置监督者的行为。
type SupervisorOptions struct {
	// Strategy 重启策略，默认为 OneForOne
	Strategy RestartStrategy
	// MaxRetries 最大重试次数，默认为 10
	MaxRetries int
	// Backoff 退避函数，默认为指数退避（50ms-5s）
	Backoff BackoffFunc
}

// NewSupervisor 创建一个新的监督者并订阅 System 的失败通知。
// 监督者会自动响应子 Actor 的 panic，根据策略进行重启。
func NewSupervisor(sys *System, opts SupervisorOptions) *Supervisor {
	b := opts.Backoff
	if b == nil {
		b = ExponentialBackoff(50*time.Millisecond, 5*time.Second)
	}
	s := &Supervisor{
		sys:        sys,
		strategy:   opts.Strategy,
		maxRetries: opts.MaxRetries,
		backoff:    b,
	}
	if s.maxRetries == 0 {
		s.maxRetries = 10
	}
	if sys != nil {
		sys.SubscribeFailures(s.onFailure)
	}
	return s
}

// Spawn 创建、注册并启动一个被监督的子 Actor。
// 子 Actor 的生命周期由监督者管理，失败时会自动重启。
// 参数 name 为子 Actor 的名称，factory 为创建函数。
func (s *Supervisor) Spawn(name string, factory ChildFactory) *BaseActor {
	a := factory(s.sys)
	if a.name == "" {
		a.name = name
	}
	s.mu.Lock()
	s.children = append(s.children, childEntry{spec: childSpec{factory: factory, name: name}, actor: a})
	s.mu.Unlock()
	a.Start()
	return a
}

// RestartCount 返回监督者执行的重启次数。
func (s *Supervisor) RestartCount() uint64 {
	s.restartsMu.Lock()
	n := s.restarts
	s.restartsMu.Unlock()
	return n
}

// onFailure 处理子 Actor 的失败通知。
// 根据配置的重启策略决定重启哪些子 Actor。
func (s *Supervisor) onFailure(actorID string, _ any) {
	s.mu.Lock()
	idx := -1
	for i := range s.children {
		if s.children[i].actor != nil && s.children[i].actor.id == actorID {
			idx = i
			break
		}
	}
	if idx == -1 {
		s.mu.Unlock()
		return
	}
	switch s.strategy {
	case OneForAll:
		for i := range s.children {
			go s.restartChild(i)
		}
	case RestForOne:
		for i := idx; i < len(s.children); i++ {
			go s.restartChild(i)
		}
	default:
		go s.restartChild(idx)
	}
	s.mu.Unlock()
}

// restartChild 重启指定索引的子 Actor。
// 使用退避策略延迟重启，超过最大重试次数后放弃。
func (s *Supervisor) restartChild(i int) {
	s.mu.Lock()
	if i < 0 || i >= len(s.children) {
		s.mu.Unlock()
		return
	}
	entry := s.children[i]
	entry.retries++
	if entry.retries > s.maxRetries {
		s.mu.Unlock()
		return
	}
	delay := s.backoff(entry.retries - 1)
	s.children[i] = entry
	s.mu.Unlock()

	time.Sleep(delay)
	if entry.actor != nil {
		entry.actor.Stop()
	}
	a := entry.spec.factory(s.sys)
	if a.name == "" {
		a.name = entry.spec.name
	}
	a.Start()

	s.mu.Lock()
	entry.actor = a
	s.children[i] = entry
	s.mu.Unlock()

	s.restartsMu.Lock()
	s.restarts++
	s.restartsMu.Unlock()
	if s.sys != nil && s.sys.metrics != nil {
		s.sys.metrics.IncRestart()
	}
}
