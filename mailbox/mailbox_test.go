package mailbox

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRingBasic(t *testing.T) {
	r := NewRing[int](1)
	_ = r.Capacity()
	a := 1
	b := 2
	if !r.Enqueue(&a) || !r.Enqueue(&b) {
		t.Fatalf("enqueue failed")
	}
	if v, ok := r.Dequeue(); !ok || *v != 1 {
		t.Fatalf("deq1: %v %v", v, ok)
	}
	if v, ok := r.Dequeue(); !ok || *v != 2 {
		t.Fatalf("deq2: %v %v", v, ok)
	}
	if _, ok := r.Dequeue(); ok {
		t.Fatalf("should empty")
	}
}

func TestSegmentedQueueGrow(t *testing.T) {
	q := NewSegmentedQueue[int](2, 2)
	_ = q.Capacity()
	a := 1
	b := 2
	c := 3
	if !q.Enqueue(&a) || !q.Enqueue(&b) || !q.Enqueue(&c) {
		t.Fatalf("enqueue")
	}
	if q.LenSegments() < 2 {
		t.Fatalf("expected grow")
	}
	if v, _ := q.Dequeue(); *v != 1 {
		t.Fatalf("v1")
	}
	if v, _ := q.Dequeue(); *v != 2 {
		t.Fatalf("v2")
	}
	if v, _ := q.Dequeue(); *v != 3 {
		t.Fatalf("v3")
	}
}

func TestSegmentedQueueMaxSegmentsDefault(t *testing.T) {
	q := NewSegmentedQueue[int](2, 0)
	if q.LenSegments() != 1 {
		t.Fatalf("expected 1 segment")
	}
}

func TestSegmentedQueueAdvanceToExistingNext(t *testing.T) {
	q := NewSegmentedQueue[int](1, 2)
	a := 1
	_ = q.Enqueue(&a)
	done := make(chan struct{})
	go func() {
		b := 2
		_ = q.Enqueue(&b)
		close(done)
	}()
	<-done
	c := 3
	_ = q.Enqueue(&c)
}

func TestSegmentedQueueFullNoGrow(t *testing.T) {
	q := NewSegmentedQueue[int](1, 1)
	a := 1
	b := 2
	c := 3
	if !q.Enqueue(&a) {
		t.Fatalf("enqueue a")
	}
	if !q.Enqueue(&b) {
		t.Fatalf("enqueue b")
	}
	if q.Enqueue(&c) {
		t.Fatalf("expected full")
	}
}

func TestSegmentedQueueEnqueueElseBranch(t *testing.T) {
	q := NewSegmentedQueue[int](1, 2)
	a := 1
	b := 2
	c := 3
	_ = q.Enqueue(&a)
	_ = q.Enqueue(&b)
	tail := q.tail.Load()
	ns := &segment[int]{q: NewRing[int](1)}
	tail.next.Store(ns)
	if !q.Enqueue(&c) {
		t.Fatalf("expected enqueue")
	}
}

func TestMailboxPriority(t *testing.T) {
	m := New(Options{Capacity: 4, UrgentCapacity: 4, MaxSegments: 1, Policy: BackpressureExpand})
	defer m.Close()
	_ = m.Push(Envelope{Priority: 0, Payload: "n1"})
	_ = m.Push(Envelope{Priority: 1, Payload: "u1"})
	_ = m.Push(Envelope{Priority: 0, Payload: "n2"})
	if env, ok := m.Pop(); !ok || env.Payload.(string) != "u1" {
		t.Fatalf("expected urgent first: %#v %v", env, ok)
	}
}

func TestMailboxClose(t *testing.T) {
	m := New(Options{Capacity: 2, UrgentCapacity: 2, MaxSegments: 1, Policy: BackpressureDropNewest})
	_ = m.Closed()
	m.Close()
	if err := m.Push(Envelope{Payload: "x"}); err == nil {
		t.Fatalf("expected closed err")
	}
	if m.Wait() {
		t.Fatalf("wait should stop")
	}
}

func TestMailboxBlockPolicy(t *testing.T) {
	m := New(Options{Capacity: 2, UrgentCapacity: 2, MaxSegments: 1, Policy: BackpressureBlock})
	defer m.Close()
	_ = m.Push(Envelope{Payload: 1})
	_ = m.Push(Envelope{Payload: 2})
	done := make(chan struct{})
	go func() {
		_ = m.Push(Envelope{Payload: 3})
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	m.Pop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("blocked too long")
	}
}

func TestMailboxBlockCloseDuringWait(t *testing.T) {
	m := New(Options{Capacity: 2, UrgentCapacity: 2, MaxSegments: 1, Policy: BackpressureBlock})
	_ = m.Push(Envelope{Payload: 1})
	_ = m.Push(Envelope{Payload: 2})
	errCh := make(chan error, 1)
	go func() { errCh <- m.Push(Envelope{Payload: 3}) }()
	time.Sleep(10 * time.Millisecond)
	m.Close()
	select {
	case err := <-errCh:
		if err != ErrMailboxClosed {
			t.Fatalf("unexpected: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout")
	}
}

func TestMailboxDefaultsAndLen(t *testing.T) {
	m := New(Options{})
	defer m.Close()
	if m.Len() != 0 {
		t.Fatalf("expected 0 len")
	}
	_ = m.Push(Envelope{Payload: "x"})
	if m.Len() != 1 {
		t.Fatalf("expected len 1")
	}
	m.Pop()
	if m.Len() != 0 {
		t.Fatalf("expected len 0")
	}
	if _, ok := m.Pop(); ok {
		t.Fatalf("expected empty pop")
	}
}

func TestMailboxPersistHook(t *testing.T) {
	var called bool
	m := New(Options{
		Capacity:       2,
		UrgentCapacity: 2,
		MaxSegments:    1,
		Policy:         BackpressureExpand,
		Persist: func(b []byte) error {
			if string(b) != "p" {
				t.Fatalf("unexpected bytes: %q", string(b))
			}
			called = true
			return nil
		},
		EncodeForPersist: func(v any) ([]byte, bool) { return []byte(v.(string)), true },
	})
	defer m.Close()
	_ = m.Push(Envelope{Payload: "p", Persist: true})
	if !called {
		t.Fatalf("expected persist called")
	}
}

func TestMailboxErrors(t *testing.T) {
	m := New(Options{Capacity: 1, UrgentCapacity: 1, MaxSegments: 1, Policy: BackpressureExpand})
	defer m.Close()
	_ = m.Push(Envelope{Payload: 1})
	_ = m.Push(Envelope{Payload: 2})
	if err := m.Push(Envelope{Payload: 3}); err == nil {
		t.Fatalf("expected full error")
	}
	m2 := New(Options{Capacity: 1, UrgentCapacity: 1, MaxSegments: 1, Policy: 99})
	defer m2.Close()
	if err := m2.Push(Envelope{Payload: 1}); err == nil {
		t.Fatalf("expected policy error")
	}
}

func TestMailboxWaitNotify(t *testing.T) {
	m := New(Options{Capacity: 2, UrgentCapacity: 2, MaxSegments: 1, Policy: BackpressureExpand})
	defer m.Close()
	done := make(chan bool, 1)
	go func() { done <- m.Wait() }()
	_ = m.Push(Envelope{Payload: "x"})
	select {
	case ok := <-done:
		if !ok {
			t.Fatalf("expected ok")
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout")
	}
}

func TestMailboxDropNewestWhenFull(t *testing.T) {
	m := New(Options{Capacity: 1, UrgentCapacity: 1, MaxSegments: 1, Policy: BackpressureDropNewest})
	defer m.Close()
	_ = m.Push(Envelope{Payload: 1})
	_ = m.Push(Envelope{Payload: 2})
	_ = m.Push(Envelope{Payload: 3})
	if m.Len() != 2 {
		t.Fatalf("expected len 2")
	}
}

func TestMailboxPersistEncodeFalse(t *testing.T) {
	var called bool
	m := New(Options{
		Capacity:         2,
		UrgentCapacity:   2,
		MaxSegments:      1,
		Policy:           BackpressureExpand,
		Persist:          func([]byte) error { called = true; return nil },
		EncodeForPersist: func(any) ([]byte, bool) { return nil, false },
	})
	defer m.Close()
	_ = m.Push(Envelope{Payload: "x", Persist: true})
	if called {
		t.Fatalf("should not call persist")
	}
}

func TestRingConcurrent(t *testing.T) {
	r := NewRing[int](1024)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				v := j
				for !r.Enqueue(&v) {
				}
			}
		}()
	}
	var cons int64
	for k := 0; k < 2; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atomic.LoadInt64(&cons) < 4000 {
				if _, ok := r.Dequeue(); ok {
					atomic.AddInt64(&cons, 1)
				}
			}
		}()
	}
	wg.Wait()
}
