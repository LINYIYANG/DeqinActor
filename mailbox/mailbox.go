package mailbox

import (
	"errors"
	"runtime"
	"sync/atomic"
	"time"
)

// ErrMailboxClosed 当向已关闭的邮箱推送消息时返回此错误。
var ErrMailboxClosed = errors.New("mailbox closed")

// BackpressurePolicy 定义邮箱满时的背压策略。
type BackpressurePolicy uint8

const (
	// BackpressureExpand 扩展策略：入队直到达到容量，然后返回错误。
	// 这是最常用的策略，提供明确的背压信号。
	BackpressureExpand BackpressurePolicy = iota
	// BackpressureBlock 阻塞策略：阻塞发送者直到有空间或邮箱关闭。
	// 适用于需要保证消息不丢失的场景，但可能导致发送者阻塞。
	BackpressureBlock
	// BackpressureDropNewest 丢弃策略：邮箱满时丢弃新消息。
	// 适用于允许消息丢失的场景，如日志收集、监控数据等。
	BackpressureDropNewest
)

// PersistHook 是持久化钩子函数，当 Envelope.Persist 为 true 时调用。
// 参数为编码后的消息负载，返回可能的错误。
type PersistHook func([]byte) error

// Envelope 是邮箱内部的消息包装器，携带负载、元数据和选项。
type Envelope struct {
	// Priority 消息优先级（0=普通，非0=紧急）
	Priority uint8
	// Payload 实际消息内容
	Payload any
	// Meta 消息元数据（如发送者信息、关联 ID 等）
	Meta any
	// Persist 是否持久化此消息
	Persist bool
}

// Mailbox 是一个双队列邮箱，包含紧急通道和普通通道。
// 紧急通道用于高优先级消息（如响应），普通通道用于常规消息。
// 邮箱支持多种背压策略和可选的消息持久化。
type Mailbox struct {
	// urgent 紧急消息队列
	urgent *SegmentedQueue[Envelope]
	// normal 普通消息队列
	normal *SegmentedQueue[Envelope]
	// policy 背压策略
	policy BackpressurePolicy
	// closed 关闭信号通道
	closed chan struct{}
	// notify 新消息通知通道
	notify chan struct{}
	// size 当前队列中的消息总数
	size atomic.Int64
	// persist 持久化钩子
	persist PersistHook
	// encode 消息编码函数
	encode func(any) ([]byte, bool)
}

// Options 配置邮箱的容量、背压策略和可选的持久化。
type Options struct {
	// Capacity 普通队列的初始容量，默认 65536
	Capacity uint64
	// UrgentCapacity 紧急队列的初始容量，默认 1024
	UrgentCapacity uint64
	// MaxSegments 最大分段数，默认 8
	MaxSegments uint64
	// Policy 背压策略，默认 BackpressureExpand
	Policy BackpressurePolicy
	// Persist 持久化钩子函数
	Persist PersistHook
	// EncodeForPersist 消息编码函数，用于持久化前编码
	EncodeForPersist func(any) ([]byte, bool)
}

// New 创建一个新的邮箱，使用默认配置（普通=65536，紧急=1024，最大分段=8）。
func New(opts Options) *Mailbox {
	capacity := opts.Capacity
	if capacity == 0 {
		capacity = 65536
	}
	uc := opts.UrgentCapacity
	if uc == 0 {
		uc = 1024
	}
	ms := opts.MaxSegments
	if ms == 0 {
		ms = 8
	}
	p := opts.Policy
	if p == 0 {
		p = BackpressureExpand
	}
	return &Mailbox{
		urgent:  NewSegmentedQueue[Envelope](uc, ms),
		normal:  NewSegmentedQueue[Envelope](capacity, ms),
		policy:  p,
		closed:  make(chan struct{}),
		notify:  make(chan struct{}, 1),
		persist: opts.Persist,
		encode:  opts.EncodeForPersist,
	}
}

// Closed 返回一个通道，在邮箱关闭时该通道会被关闭。
// 可用于 select 语句中检测邮箱是否已关闭。
func (m *Mailbox) Closed() <-chan struct{} { return m.closed }

// Close 关闭邮箱并解除等待者的阻塞。
// 关闭后不能再推送消息，但可以继续弹出已入队的消息。
func (m *Mailbox) Close() {
	select {
	case <-m.closed:
	default:
		close(m.closed)
	}
}

// Push 根据优先级和背压策略将信封入队。
// 如果设置了 Persist 标志且配置了持久化钩子，会先持久化消息。
// 返回可能的错误（如邮箱已关闭或已满）。
func (m *Mailbox) Push(env Envelope) error {
	select {
	case <-m.closed:
		return ErrMailboxClosed
	default:
	}
	if env.Persist && m.persist != nil && m.encode != nil {
		if b, ok := m.encode(env.Payload); ok {
			_ = m.persist(b)
		}
	}
	q := m.normal
	if env.Priority != 0 {
		q = m.urgent
	}
	switch m.policy {
	case BackpressureExpand:
		if q.Enqueue(&env) {
			m.size.Add(1)
			select {
			case m.notify <- struct{}{}:
			default:
			}
			return nil
		}
		return errors.New("mailbox full")
	case BackpressureDropNewest:
		if q.Enqueue(&env) {
			m.size.Add(1)
			select {
			case m.notify <- struct{}{}:
			default:
			}
			return nil
		}
		return nil
	case BackpressureBlock:
		backoff := time.Microsecond
		for {
			if q.Enqueue(&env) {
				m.size.Add(1)
				select {
				case m.notify <- struct{}{}:
				default:
				}
				return nil
			}
			select {
			case <-m.closed:
				return ErrMailboxClosed
			default:
			}
			runtime.Gosched()
			time.Sleep(backoff)
			if backoff < 2*time.Millisecond {
				backoff *= 2
			}
		}
	default:
		return errors.New("unknown backpressure policy")
	}
}

// Pop 弹出一个信封，优先处理紧急消息。
// 返回信封和一个布尔值表示是否成功弹出。
func (m *Mailbox) Pop() (Envelope, bool) {
	if v, ok := m.urgent.Dequeue(); ok && v != nil {
		m.size.Add(-1)
		return *v, true
	}
	if v, ok := m.normal.Dequeue(); ok && v != nil {
		m.size.Add(-1)
		return *v, true
	}
	return Envelope{}, false
}

// Len 返回队列中消息的近似数量。
func (m *Mailbox) Len() int64 { return m.size.Load() }

// Wait 阻塞直到至少有一条消息入队或邮箱关闭。
// 返回 true 表示有消息可处理，false 表示邮箱已关闭。
func (m *Mailbox) Wait() bool {
	select {
	case <-m.notify:
		return true
	case <-m.closed:
		return false
	}
}
