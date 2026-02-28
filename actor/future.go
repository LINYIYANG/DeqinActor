package actor

import (
	"sync"
	"sync/atomic"
	"time"
)

// futureResult 存储 Future 完成后的结果值。
type futureResult[T any] struct {
	v T
}

// Future 是一个最小化的、无依赖的 Future/Promise 实现。
// 它只完成一次，支持回调和阻塞等待。
// Future 用于实现异步请求-响应模式，允许调用者异步等待响应。
type Future[T any] struct {
	// ch 用于通知完成的通道
	ch chan T
	// done 标记是否已完成
	done atomic.Bool
	// result 存储完成后的结果
	result atomic.Value
	// cbMu 保护 callbacks 的并发访问
	cbMu sync.Mutex
	// callbacks 完成时调用的回调函数列表
	callbacks []func(T)
}

// newFuture 创建一个新的未完成 Future。
func newFuture[T any]() *Future[T] {
	return &Future[T]{ch: make(chan T, 1)}
}

// complete 完成 Future，设置结果值。
// 如果 Future 已经完成，此方法不执行任何操作。
// 完成后会调用所有注册的回调函数。
func (f *Future[T]) complete(v T) {
	if f.done.Swap(true) {
		return
	}
	f.result.Store(&futureResult[T]{v: v})
	f.ch <- v
	close(f.ch)
	f.cbMu.Lock()
	cbs := append([]func(T){}, f.callbacks...)
	f.callbacks = nil
	f.cbMu.Unlock()
	for _, cb := range cbs {
		cb(v)
	}
}

// OnComplete 注册一个回调函数，在 Future 完成时调用。
// 如果 Future 已经完成，回调会在调用者 goroutine 中立即执行。
func (f *Future[T]) OnComplete(cb func(T)) {
	if f.done.Load() {
		r, _ := f.result.Load().(*futureResult[T])
		if r != nil {
			cb(r.v)
		}
		return
	}
	f.cbMu.Lock()
	f.callbacks = append(f.callbacks, cb)
	f.cbMu.Unlock()
}

// Await 阻塞直到 Future 完成或超时。
// 如果 timeout <= 0，则无限期等待。
// 返回结果值和一个布尔值表示是否成功获取（未超时）。
func (f *Future[T]) Await(timeout time.Duration) (T, bool) {
	var zero T
	if f.done.Load() {
		r, _ := f.result.Load().(*futureResult[T])
		if r == nil {
			return zero, false
		}
		return r.v, true
	}
	if timeout <= 0 {
		v, ok := <-f.ch
		return v, ok
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case v, ok := <-f.ch:
		return v, ok
	case <-timer.C:
		return zero, false
	}
}

// Then 将 Future[A] 转换为 Future[B]。
// 当原始 Future 完成时，对结果应用 fn 函数，生成新的 Future。
// 这允许链式组合多个异步操作。
func Then[A any, B any](fa *Future[A], fn func(A) B) *Future[B] {
	fb := newFuture[B]()
	fa.OnComplete(func(a A) { fb.complete(fn(a)) })
	return fb
}

// All 等待所有输入 Future 完成后返回结果切片。
// 结果顺序与输入顺序一致。
// 如果输入为空，立即返回一个完成的空 Future。
func All[T any](fs ...*Future[T]) *Future[[]T] {
	out := newFuture[[]T]()
	if len(fs) == 0 {
		out.complete(nil)
		return out
	}
	var (
		mu   sync.Mutex
		left = int32(len(fs))
		vals = make([]T, len(fs))
	)
	for i, f := range fs {
		i, f := i, f
		f.OnComplete(func(v T) {
			mu.Lock()
			vals[i] = v
			mu.Unlock()
			if atomic.AddInt32(&left, -1) == 0 {
				out.complete(vals)
			}
		})
	}
	return out
}
