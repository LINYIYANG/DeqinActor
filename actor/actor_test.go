package actor

import (
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFuture(t *testing.T) {
	f := newFuture[int]()
	done := make(chan struct{})
	f.OnComplete(func(v int) {
		if v != 42 {
			t.Fatalf("unexpected: %v", v)
		}
		close(done)
	})
	f.complete(42)
	<-done
	if v, ok := f.Await(10 * time.Millisecond); !ok || v != 42 {
		t.Fatalf("await: %v %v", v, ok)
	}
	g := Then(f, func(v int) string { return "x" })
	if v, ok := g.Await(10 * time.Millisecond); !ok || v != "x" {
		t.Fatalf("then: %v %v", v, ok)
	}
	a := newFuture[int]()
	b := newFuture[int]()
	all := All(a, b)
	a.complete(1)
	b.complete(2)
	if v, ok := all.Await(10 * time.Millisecond); !ok || len(v) != 2 || v[0] != 1 || v[1] != 2 {
		t.Fatalf("all: %#v %v", v, ok)
	}
}

func TestCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	now := time.Now()
	if !cb.Allow(now) {
		t.Fatalf("should allow")
	}
	cb.OnFailure(now)
	cb.OnFailure(now)
	if cb.Allow(now) {
		t.Fatalf("should open")
	}
	time.Sleep(60 * time.Millisecond)
	if !cb.Allow(time.Now()) {
		t.Fatalf("should half-open allow probe")
	}
	if cb.Allow(time.Now()) {
		t.Fatalf("should only allow one probe")
	}
	cb.OnSuccess()
	if !cb.Allow(time.Now()) {
		t.Fatalf("should close")
	}
}

func TestAskAndResponse(t *testing.T) {
	sys := NewSystem()
	a := NewBaseActor(sys, BaseActorOptions{Name: "a"})
	b := NewBaseActor(sys, BaseActorOptions{Name: "b", Receive: func(ctx *Context, msg any) {
		ctx.Respond(msg.(string)+"-ok", nil)
	}})
	a.Start()
	b.Start()
	defer a.Stop()
	defer b.Stop()

	resp, err := a.SyncAsk(b, "ping", SendOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Value.(string) != "ping-ok" {
		t.Fatalf("resp: %#v", resp)
	}
}

func TestAskDegradeToAsync(t *testing.T) {
	sys := NewSystem()
	sys.waitTokens = make(chan struct{}, 0)
	a := NewBaseActor(sys, BaseActorOptions{Name: "a"})
	b := NewBaseActor(sys, BaseActorOptions{Name: "b", Receive: func(ctx *Context, msg any) { ctx.Respond("ok", nil) }})
	a.Start()
	b.Start()
	defer a.Stop()
	defer b.Stop()

	_, f, err := a.Ask(b, "x", SendOptions{Timeout: time.Second, AllowDegrade: true})
	if err != ErrDegradedToAsync || f == nil {
		t.Fatalf("expected degrade, got %v %v", err, f)
	}
	resp, ok := f.Await(time.Second)
	if !ok || resp.Value.(string) != "ok" {
		t.Fatalf("future resp: %#v %v", resp, ok)
	}
}

func TestPersistenceReplay(t *testing.T) {
	dir := t.TempDir()
	sys1 := NewSystem()
	sys1.EnablePersistence(dir)
	got1 := make(chan any, 1)
	a1 := NewBaseActor(sys1, BaseActorOptions{ID: "actorA", Receive: func(_ *Context, msg any) { got1 <- msg }})
	a1.Start()
	_ = sys1.Tell(nil, a1, "p1", SendOptions{Persist: true})
	if v := <-got1; v.(string) != "p1" {
		t.Fatalf("first: %#v", v)
	}
	a1.Stop()

	sys2 := NewSystem()
	sys2.EnablePersistence(dir)
	got2 := make(chan any, 1)
	a2 := NewBaseActor(sys2, BaseActorOptions{ID: "actorA", Receive: func(_ *Context, msg any) { got2 <- msg }})
	a2.Start()
	defer a2.Stop()
	select {
	case v := <-got2:
		if v.(string) != "p1" {
			t.Fatalf("replay: %#v", v)
		}
	case <-time.After(time.Second):
		t.Fatalf("no replay")
	}
}

func TestSupervisorRestart(t *testing.T) {
	sys := NewSystem()
	sup := NewSupervisor(sys, SupervisorOptions{Strategy: OneForOne, MaxRetries: 3, Backoff: func(int) time.Duration { return 10 * time.Millisecond }})
	seen := make(chan any, 10)
	child := sup.Spawn("c", func(sys *System) *BaseActor {
		return NewBaseActor(sys, BaseActorOptions{Name: "c", Receive: func(_ *Context, msg any) {
			if msg == "boom" {
				panic("boom")
			}
			seen <- msg
		}})
	})
	_ = sys.Tell(nil, child, "ok1", SendOptions{})
	if v := <-seen; v != "ok1" {
		t.Fatalf("unexpected: %#v", v)
	}
	_ = sys.Tell(nil, child, "boom", SendOptions{})
	time.Sleep(50 * time.Millisecond)
	if sup.RestartCount() == 0 {
		t.Fatalf("expected restart")
	}
	sys.registry.mu.RLock()
	cur := sys.registry.byName["c"]
	sys.registry.mu.RUnlock()
	a, ok := sys.registry.Get(cur)
	if !ok {
		t.Fatalf("missing child")
	}
	_ = sys.Tell(nil, a, "ok2", SendOptions{})
	if v := <-seen; v != "ok2" {
		t.Fatalf("unexpected2: %#v", v)
	}
}

func TestMetricsWrite(t *testing.T) {
	sys := NewSystem()
	_ = sys.EnableMetrics(":0")
	a := NewBaseActor(sys, BaseActorOptions{Name: "a", Receive: func(ctx *Context, msg any) { ctx.Respond("x", nil) }})
	b := NewBaseActor(sys, BaseActorOptions{Name: "b", Receive: func(ctx *Context, msg any) { ctx.Respond("y", nil) }})
	a.Start()
	b.Start()
	defer a.Stop()
	defer b.Stop()
	_, _ = a.SyncAsk(b, "m", SendOptions{Timeout: time.Second})
	rr := httptest.NewRecorder()
	sys.writeMetrics(rr)
	body := rr.Body.String()
	if !strings.Contains(body, "deqinactor_messages_out_total") || !strings.Contains(body, "deqinactor_latency_seconds_bucket") {
		t.Fatalf("unexpected metrics: %s", body)
	}
}

func TestTokenBucket(t *testing.T) {
	tb := NewTokenBucket(1000, 10)
	tb.SetQPS(0)
	if !tb.Allow(10) {
		t.Fatalf("should allow when disabled")
	}
	tb.SetQPS(1000)
	if !tb.Allow(1) {
		t.Fatalf("should allow")
	}
}

func TestRemoteDeliver(t *testing.T) {
	sysA := NewSystem()
	sysB := NewSystem()
	if err := sysA.EnableRemote("127.0.0.1:0"); err != nil {
		t.Fatalf("remote A: %v", err)
	}
	if err := sysB.EnableRemote("127.0.0.1:0"); err != nil {
		t.Fatalf("remote B: %v", err)
	}
	defer sysA.StopRemote()
	defer sysB.StopRemote()

	got := make(chan any, 1)
	b := NewBaseActor(sysB, BaseActorOptions{ID: "remoteB", Receive: func(_ *Context, msg any) { got <- msg }})
	b.Start()
	defer b.Stop()

	sysA.SetLocation("remoteB", sysB.RemoteAddr())
	refB := sysA.Ref("remoteB")
	a := NewBaseActor(sysA, BaseActorOptions{Name: "a"})
	a.Start()
	defer a.Stop()

	if err := sysA.Tell(a, refB, "hi", SendOptions{}); err != nil {
		t.Fatalf("tell: %v", err)
	}
	select {
	case v := <-got:
		if v.(string) != "hi" {
			t.Fatalf("got: %#v", v)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout")
	}
}

func TestCompleteFutureTimeout(t *testing.T) {
	sys := NewSystem()
	f := newFuture[*Response]()
	sys.TrackPending("c", f, 10*time.Millisecond)
	resp, ok := f.Await(50 * time.Millisecond)
	if !ok || resp == nil || resp.Err != ErrAskTimeout {
		t.Fatalf("expected timeout response, got: %#v %v", resp, ok)
	}
}

func TestSystemDeliverNotFound(t *testing.T) {
	sys := NewSystem()
	if err := sys.deliverToID("missing", envelopeMeta{}, PriorityNormal, "x", false); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSystemSendMetrics(t *testing.T) {
	sys := NewSystem()
	sys.metrics = NewMetrics()
	var c atomic.Uint64
	a := NewBaseActor(sys, BaseActorOptions{Name: "a", Receive: func(_ *Context, _ any) { c.Add(1) }})
	a.Start()
	defer a.Stop()
	_ = sys.Tell(nil, a, "x", SendOptions{})
	time.Sleep(10 * time.Millisecond)
	if sys.metrics.msgOut.Load() == 0 || sys.metrics.msgIn.Load() == 0 || c.Load() == 0 {
		t.Fatalf("metrics not updated")
	}
}

func TestBaseActorAccessorsAndSend(t *testing.T) {
	sys := NewSystem()
	recv := make(chan any, 1)
	b := NewBaseActor(sys, BaseActorOptions{Name: "b", Receive: func(ctx *Context, msg any) {
		_ = ctx.Self().ID()
		_ = ctx.SenderID()
		_ = ctx.CorrelationID()
		ctx.Respond("ignored", nil)
		recv <- msg
	}})
	a := NewBaseActor(sys, BaseActorOptions{Name: "a"})
	a.Start()
	b.Start()
	defer a.Stop()
	defer b.Stop()
	if a.ID() == "" || a.Name() != "a" || b.Name() != "b" {
		t.Fatalf("bad ids/names")
	}
	a.Receive("direct")
	a.Send(b, "viaSend")
	select {
	case v := <-recv:
		if v.(string) != "viaSend" {
			t.Fatalf("unexpected: %#v", v)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout")
	}
}

func TestRegistryAndFinders(t *testing.T) {
	sys := NewSystem()
	a := NewBaseActor(sys, BaseActorOptions{Name: "n"})
	a.Start()
	defer a.Stop()
	if _, ok := sys.Registry().GetByName("n"); !ok {
		t.Fatalf("expected get by name")
	}
	if _, ok := sys.FindByID(a.ID()); !ok {
		t.Fatalf("expected find by id")
	}
	if _, ok := sys.FindByName("n"); !ok {
		t.Fatalf("expected find by name")
	}
	if _, ok := sys.FindByName("missing"); ok {
		t.Fatalf("unexpected find")
	}
}

type stubSerializer struct{}

func (stubSerializer) Marshal(any) ([]byte, error)   { return []byte("x"), nil }
func (stubSerializer) Unmarshal([]byte) (any, error) { return "y", nil }

func TestSystemSetSerializerAndRateLimit(t *testing.T) {
	sys := NewSystem()
	sys.SetSerializer(nil)
	sys.SetSerializer(stubSerializer{})
	if _, err := sys.Serializer().Marshal(1); err != nil {
		t.Fatalf("marshal err")
	}
	sys.EnableRateLimit(1000, 10)
	sys.SetQPS(0)
	sys.SetQPS(1000)
}

func TestGobSerializerError(t *testing.T) {
	s := &GobSerializer{}
	if _, err := s.Marshal(func() {}); err == nil {
		t.Fatalf("expected marshal error")
	}
}

func TestSendAsyncSystemNil(t *testing.T) {
	a := NewBaseActor(nil, BaseActorOptions{})
	f := a.SendAsync(a, "x")
	resp, _ := f.Await(10 * time.Millisecond)
	if resp.Err != ErrActorNotFound {
		t.Fatalf("expected not found")
	}
}

func TestAskTimeoutAndErrResponse(t *testing.T) {
	sys := NewSystem()
	a := NewBaseActor(sys, BaseActorOptions{Name: "a"})
	slow := NewBaseActor(sys, BaseActorOptions{Name: "slow", Receive: func(_ *Context, _ any) {}})
	errActor := NewBaseActor(sys, BaseActorOptions{Name: "err", Receive: func(ctx *Context, _ any) { ctx.Respond(nil, errors.New("x")) }})
	a.Start()
	slow.Start()
	errActor.Start()
	defer a.Stop()
	defer slow.Stop()
	defer errActor.Stop()

	if _, err := a.SyncAsk(slow, "x", SendOptions{Timeout: 10 * time.Millisecond}); !errors.Is(err, ErrAskTimeout) {
		t.Fatalf("expected timeout, got: %v", err)
	}
	if _, err := a.SyncAsk(errActor, "x", SendOptions{Timeout: time.Second}); err == nil || err.Error() != "x" {
		t.Fatalf("expected err, got: %v", err)
	}
}

func TestMetricsEdgeCases(t *testing.T) {
	sys := NewSystem()
	rr := httptest.NewRecorder()
	sys.writeMetrics(rr)
	if rr.Code != 204 {
		t.Fatalf("expected no content")
	}
	sys.metrics = NewMetrics()
	sys.metrics.ObserveLatency(-1)
	sys.metrics.IncRestart()
}

func TestRemoteErrorPaths(t *testing.T) {
	sysA := NewSystem()
	if err := sysA.remoteDeliver("127.0.0.1:1", &remoteEnvelope{}); err == nil {
		t.Fatalf("expected remote not enabled")
	}
	sysB := NewSystem()
	if err := sysA.EnableRemote("127.0.0.1:0"); err != nil {
		t.Fatalf("enable A: %v", err)
	}
	if err := sysB.EnableRemote("127.0.0.1:0"); err != nil {
		t.Fatalf("enable B: %v", err)
	}
	defer sysA.StopRemote()
	defer sysB.StopRemote()
	sysA.SetLocation("missing", sysB.RemoteAddr())
	if err := sysA.deliverToID("missing", envelopeMeta{kind: envelopeKindUser}, PriorityNormal, "x", false); err == nil {
		t.Fatalf("expected ack error")
	}
	sysA.SetLocation("missing", "")
	if _, ok := sysA.locationOf("missing"); ok {
		t.Fatalf("expected removed")
	}
	sysA.SetLocation("missing", sysB.RemoteAddr())
	_ = sysA.deliverToID("missing", envelopeMeta{kind: envelopeKindUser}, PriorityNormal, "x", false)
}

func TestExponentialBackoff(t *testing.T) {
	b := ExponentialBackoff(1*time.Millisecond, 3*time.Millisecond)
	if b(0) != 1*time.Millisecond {
		t.Fatalf("bad backoff")
	}
	if b(2) != 3*time.Millisecond {
		t.Fatalf("bad cap")
	}
}

func TestTokenBucketRefillBranches(t *testing.T) {
	tb := NewTokenBucket(0, 1)
	now := time.Now().UnixNano()
	tb.refill(now)
	tb.rate.Store(1)
	tb.lastNS.Store(now)
	tb.refill(now - 1)
	tb.refill(now)
	tb.refill(now + 1)
	tb.lastNS.Store(now + int64(time.Second))
	tb.refill(now)
}

func TestActorRefMethods(t *testing.T) {
	sys := NewSystem()
	ref := sys.Ref("id")
	_ = ref.ID()
	ref.Start()
	ref.Stop()
	ref.Receive(nil)
	a := NewBaseActor(sys, BaseActorOptions{Name: "a", Receive: func(_ *Context, _ any) {}})
	a.Start()
	defer a.Stop()
	ref.Send(a, "x")
}
