package mailbox

import (
	"sync/atomic"
)

// ringCell 是环形缓冲区的单个单元，包含序列号和值指针。
// 序列号用于实现无锁的入队和出队操作。
type ringCell[T any] struct {
	// seq 序列号，用于协调生产者和消费者
	seq atomic.Uint64
	// val 存储的值指针
	val atomic.Pointer[T]
}

// Ring 是一个无锁的环形缓冲区实现。
// 基于 Dmitry Vyukov 的有界 MPMC 队列算法，支持多生产者多消费者。
//
// Ring 使用序列号而非传统的锁来协调并发访问：
//   - 入队时，生产者 CAS 更新 tail 并设置值
//   - 出队时，消费者 CAS 更新 head 并读取值
//   - 序列号用于检测缓冲区是否为空或已满
//
// 这种设计避免了锁竞争，在高并发场景下具有更好的性能。
type Ring[T any] struct {
	// mask 用于快速取模的掩码（容量必须是 2 的幂）
	mask uint64
	// buf 环形缓冲区单元数组
	buf []ringCell[T]
	// head 消费者指针
	head atomic.Uint64
	// tail 生产者指针
	tail atomic.Uint64
}

// NewRing 创建一个新的环形缓冲区。
// 容量会被向上取整到最近的 2 的幂（最小为 2）。
// 初始化时，每个单元的序列号设置为其索引。
func NewRing[T any](capacity uint64) *Ring[T] {
	if capacity < 2 {
		capacity = 2
	}
	c := uint64(1)
	for c < capacity {
		c <<= 1
	}
	r := &Ring[T]{
		mask: c - 1,
		buf:  make([]ringCell[T], c),
	}
	for i := range r.buf {
		r.buf[i].seq.Store(uint64(i))
	}
	return r
}

// Capacity 返回环形缓冲区的实际容量。
func (r *Ring[T]) Capacity() uint64 { return uint64(len(r.buf)) }

// Enqueue 将值入队。
// 使用 CAS 操作实现无锁入队，如果缓冲区已满返回 false。
//
// 算法说明：
//  1. 读取 tail 指针
//  2. 计算对应的单元索引（tail & mask）
//  3. 检查序列号是否匹配 tail（表示该单元可写入）
//  4. CAS 更新 tail，成功则写入值并更新序列号
func (r *Ring[T]) Enqueue(v *T) bool {
	for {
		tail := r.tail.Load()
		cell := &r.buf[tail&r.mask]
		seq := cell.seq.Load()
		dif := int64(seq) - int64(tail)
		if dif == 0 {
			if r.tail.CompareAndSwap(tail, tail+1) {
				cell.val.Store(v)
				cell.seq.Store(tail + 1)
				return true
			}
		} else if dif < 0 {
			return false
		}
	}
}

// Dequeue 从队列中出队一个值。
// 使用 CAS 操作实现无锁出队，如果缓冲区为空返回 nil 和 false。
//
// 算法说明：
//  1. 读取 head 指针
//  2. 计算对应的单元索引（head & mask）
//  3. 检查序列号是否匹配 head+1（表示该单元可读取）
//  4. CAS 更新 head，成功则读取值并更新序列号
func (r *Ring[T]) Dequeue() (*T, bool) {
	for {
		head := r.head.Load()
		cell := &r.buf[head&r.mask]
		seq := cell.seq.Load()
		dif := int64(seq) - int64(head+1)
		if dif == 0 {
			if r.head.CompareAndSwap(head, head+1) {
				v := cell.val.Load()
				cell.val.Store(nil)
				cell.seq.Store(head + r.mask + 1)
				return v, true
			}
		} else if dif < 0 {
			return nil, false
		}
	}
}
