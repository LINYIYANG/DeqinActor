package mailbox

import (
	"sync/atomic"
)

// segment 是分段队列的一个段，包含一个环形缓冲区和指向下一个段的指针。
type segment[T any] struct {
	// q 环形缓冲区
	q *Ring[T]
	// next 指向下一个段的原子指针
	next atomic.Pointer[segment[T]]
}

// SegmentedQueue 是一个分段无锁队列，支持动态扩容。
// 当一个段满时，会自动创建新段并链接到队列尾部。
// 这种设计避免了预分配大块内存，同时保持高吞吐量。
//
// SegmentedQueue 使用无锁算法实现，支持多生产者多消费者场景。
// 它基于 MPSC（多生产者单消费者）模式，但实际也支持多消费者。
type SegmentedQueue[T any] struct {
	// head 队列头指针，消费者从这里出队
	head atomic.Pointer[segment[T]]
	// tail 队列尾指针，生产者向这里入队
	tail atomic.Pointer[segment[T]]
	// segCap 每个段的容量
	segCap uint64
	// segs 当前段数
	segs atomic.Uint64
	// maxSeg 最大段数限制
	maxSeg uint64
}

// NewSegmentedQueue 创建一个新的分段队列。
// segmentCapacity 为每个段的容量，maxSegments 为最大段数。
// 当 maxSegments 为 0 时，默认为 1。
func NewSegmentedQueue[T any](segmentCapacity, maxSegments uint64) *SegmentedQueue[T] {
	if maxSegments == 0 {
		maxSegments = 1
	}
	s := &segment[T]{q: NewRing[T](segmentCapacity)}
	q := &SegmentedQueue[T]{segCap: segmentCapacity, maxSeg: maxSegments}
	q.head.Store(s)
	q.tail.Store(s)
	q.segs.Store(1)
	return q
}

// Capacity 返回队列的总容量（段容量 × 最大段数）。
func (q *SegmentedQueue[T]) Capacity() uint64 { return q.segCap * q.maxSeg }

// LenSegments 返回当前的段数。
func (q *SegmentedQueue[T]) LenSegments() uint64 { return q.segs.Load() }

// Enqueue 将值入队。
// 如果当前段已满且未达到最大段数，会创建新段。
// 返回 true 表示入队成功，false 表示队列已满。
func (q *SegmentedQueue[T]) Enqueue(v *T) bool {
	for {
		t := q.tail.Load()
		if t.q.Enqueue(v) {
			return true
		}
		if q.segs.Load() >= q.maxSeg {
			return false
		}
		n := t.next.Load()
		if n == nil {
			ns := &segment[T]{q: NewRing[T](q.segCap)}
			if t.next.CompareAndSwap(nil, ns) {
				q.tail.CompareAndSwap(t, ns)
				q.segs.Add(1)
			}
		} else {
			q.tail.CompareAndSwap(t, n)
		}
	}
}

// Dequeue 从队列中出队一个值。
// 如果当前段为空且有下一个段，会切换到下一个段。
// 返回值和一个布尔值表示是否成功出队。
func (q *SegmentedQueue[T]) Dequeue() (*T, bool) {
	h := q.head.Load()
	v, ok := h.q.Dequeue()
	if ok {
		return v, true
	}
	n := h.next.Load()
	if n == nil {
		return nil, false
	}
	q.head.Store(n)
	q.segs.Add(^uint64(0))
	return q.Dequeue()
}
