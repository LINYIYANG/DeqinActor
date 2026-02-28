package actor

import (
	"context"
	"testing"
	"time"
)

type dummyActor struct {
	id  string
	got chan any
}

func (d *dummyActor) ID() string { return d.id }
func (d *dummyActor) Start()     {}
func (d *dummyActor) Stop()      {}
func (d *dummyActor) Receive(msg any) {
	if d.got != nil {
		d.got <- msg
	}
}
func (d *dummyActor) Send(target IActor, msg any) {}

func TestCoverageRemainingBranches(t *testing.T) {
	sys := NewSystem()
	sys.EnablePersistence(t.TempDir())
	_ = sys.actorWALPath("")
	_ = sys.actorWALPath("x")

	a := NewBaseActor(sys, BaseActorOptions{Name: "a", Receive: func(ctx *Context, msg any) {
		ctx.Respond(nil, nil)
	}})
	a.Start()
	defer a.Stop()

	a.Receive("x")
	a.Send(nil, "x")
	NewBaseActor(nil, BaseActorOptions{Name: "n"}).Send(a, "x")

	d := &dummyActor{id: "dummy", got: make(chan any, 1)}
	sys.registry.Register(d.ID(), "dn", d)
	defer sys.registry.Unregister(d.ID(), "dn")
	if err := sys.deliverToID(d.ID(), envelopeMeta{kind: envelopeKindUser, fromID: a.ID()}, PriorityNormal, "m", false); err != nil {
		t.Fatalf("deliver dummy: %v", err)
	}
	select {
	case <-d.got:
	case <-time.After(time.Second):
		t.Fatalf("timeout")
	}
	if err := sys.sendResponse(a.ID(), "", &Response{CorrelationID: "c"}); err == nil {
		t.Fatalf("expected not found")
	}

	got := make(chan any, 1)
	recv := &dummyActor{got: got}
	sys.Tell(a, recv, PriorityMessage{Priority: PriorityUrgent, Msg: "p"}, SendOptions{})
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatalf("timeout")
	}

	d2 := &dummyActor{id: "id2", got: make(chan any, 1)}
	sys.SetLocation("id2", "127.0.0.1:1")
	_ = sys.deliverToID(d2.ID(), envelopeMeta{kind: envelopeKindUser}, PriorityNormal, "x", false)

	_ = sys.breakerFor(nil)
	_ = sys.breakerFor(&dummyActor{})
	_ = sys.breakerFor(&dummyActor{id: ""})
	_ = sys.breakerFor(a)

	sys.tryAcquireWaitToken()
	sys.waitTokens = make(chan struct{}, 0)
	_ = sys.tryAcquireWaitToken()

	gs := &GobSerializer{}
	_, _ = gs.Unmarshal([]byte{1, 2, 3})

	f := newFuture[int]()
	f.complete(1)
	f.complete(2)
	_, _ = f.Await(0)
	_, _ = f.Await(1 * time.Nanosecond)
	_ = All[int]()

	cb := NewCircuitBreaker(0, 0)
	cb.OnFailure(time.Now())
	cb.state.Store(uint32(breakerHalfOpen))
	cb.OnFailure(time.Now())

	tb := NewTokenBucket(-1, 0)
	tb.Allow(0)
	tb.Wait(1)

	if err := sys.EnableRemote("bad:addr"); err == nil {
		t.Fatalf("expected remote enable error")
	}
	sys.StopRemote()

	if _, err := (gobCodec{}).Marshal(func() {}); err == nil {
		t.Fatalf("expected codec error")
	}

	rt := &remoteTransport{sys: sys}
	_, _ = rt.Deliver(context.Background(), &remoteEnvelope{Payload: []byte{1}})
	_, _ = rt.Deliver(context.Background(), &remoteEnvelope{ToID: "missing", Payload: mustMarshal(sys, "x")})
	sys.registry.Register("dummy2", "", &dummyActor{got: make(chan any, 1)})
	defer sys.registry.Unregister("dummy2", "")
	_, _ = rt.Deliver(context.Background(), &remoteEnvelope{ToID: "dummy2", Payload: mustMarshal(sys, "x")})

	sup := NewSupervisor(nil, SupervisorOptions{})
	_ = sup.RestartCount()

	sys2 := NewSystem()
	sup2 := NewSupervisor(sys2, SupervisorOptions{Strategy: OneForAll, MaxRetries: 1, Backoff: func(int) time.Duration { return 0 }})
	c1 := sup2.Spawn("c1", func(sys *System) *BaseActor {
		return NewBaseActor(sys, BaseActorOptions{Name: "c1", Receive: func(_ *Context, msg any) {
			if msg == "boom" {
				panic("b")
			}
		}})
	})
	c2 := sup2.Spawn("c2", func(sys *System) *BaseActor {
		return NewBaseActor(sys, BaseActorOptions{Name: "c2", Receive: func(_ *Context, msg any) {
			if msg == "boom" {
				panic("b")
			}
		}})
	})
	_ = sys2.Tell(nil, c1, "boom", SendOptions{})
	_ = sys2.Tell(nil, c2, "boom", SendOptions{})
	time.Sleep(10 * time.Millisecond)

	sup3 := NewSupervisor(sys2, SupervisorOptions{Strategy: RestForOne, MaxRetries: 1, Backoff: func(int) time.Duration { return 0 }})
	r1 := sup3.Spawn("r1", func(sys *System) *BaseActor {
		return NewBaseActor(sys, BaseActorOptions{Name: "r1", Receive: func(_ *Context, msg any) {
			if msg == "boom" {
				panic("b")
			}
		}})
	})
	_ = sys2.Tell(nil, r1, "boom", SendOptions{})
	time.Sleep(10 * time.Millisecond)
	sup3.onFailure("missing", nil)
	sup3.restartChild(-1)
	sup3.restartChild(999)
	sup3.mu.Lock()
	if len(sup3.children) > 0 {
		sup3.children[0].retries = sup3.maxRetries + 1
	}
	sup3.mu.Unlock()
	sup3.restartChild(0)

	_ = a.system.SetQPS
}

func mustMarshal(sys *System, v any) []byte {
	b, _ := sys.serializer.Marshal(v)
	return b
}
