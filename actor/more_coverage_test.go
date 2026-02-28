package actor

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"deqinactor/mailbox"

	"google.golang.org/grpc"
)

type recvOnly struct{ got chan any }

func (r *recvOnly) Start()           {}
func (r *recvOnly) Stop()            {}
func (r *recvOnly) Receive(msg any)  { r.got <- msg }
func (r *recvOnly) Send(IActor, any) {}

func TestFutureAwaitBranches(t *testing.T) {
	f := newFuture[int]()
	go func() {
		time.Sleep(1 * time.Millisecond)
		f.complete(7)
	}()
	if v, ok := f.Await(0); !ok || v != 7 {
		t.Fatalf("await0: %v %v", v, ok)
	}
	g := newFuture[int]()
	if _, ok := g.Await(1 * time.Millisecond); ok {
		t.Fatalf("expected timeout")
	}
}

func TestMetricsEnableAndWriteStartZero(t *testing.T) {
	sys := NewSystem()
	_ = sys.EnableMetrics("")
	_ = sys.EnableMetrics(":0")
	sys.metrics = NewMetrics()
	rr := httptest.NewRecorder()
	sys.writeMetrics(rr)
	if rr.Body.Len() == 0 {
		t.Fatalf("expected body")
	}
}

func TestTokenBucketAllowWaitRefillBranches(t *testing.T) {
	tb := NewTokenBucket(1, 1)
	if tb.Allow(2) {
		t.Fatalf("expected deny")
	}
	tb.Allow(1)
	tb.SetQPS(1_000_000_000)
	start := time.Now()
	tb.Wait(1)
	if time.Since(start) > time.Second {
		t.Fatalf("wait too long")
	}
}

func TestSystemSendRequestBranches(t *testing.T) {
	sys := NewSystem()
	a := NewBaseActor(sys, BaseActorOptions{Name: "a"})
	b := NewBaseActor(sys, BaseActorOptions{ID: "b", Name: "b", Receive: func(ctx *Context, msg any) { ctx.Respond("ok", nil) }})
	a.Start()
	b.Start()
	defer a.Stop()
	defer b.Stop()

	req := &Request{CorrelationID: "c", Payload: "x", ReplyTo: a.ID()}
	if err := sys.sendRequest(a, b, req, PriorityNormal, false); err != nil {
		t.Fatalf("baseactor: %v", err)
	}

	refB := sys.Ref("b")
	if err := sys.sendRequest(a, refB, req, PriorityNormal, false); err != nil {
		t.Fatalf("ref: %v", err)
	}

	got := make(chan any, 1)
	idActor := &dummyActor{id: "idActor", got: got}
	sys.registry.Register("idActor", "", idActor)
	defer sys.registry.Unregister("idActor", "")
	if err := sys.sendRequest(a, idActor, req, PriorityNormal, false); err != nil {
		t.Fatalf("id: %v", err)
	}

	ro := &recvOnly{got: got}
	sys.sendRequest(a, ro, req, PriorityNormal, false)
	select {
	case <-got:
	default:
		t.Fatalf("expected receive")
	}
}

func TestAskAndSendAsyncBranches(t *testing.T) {
	sys := NewSystem()
	a := NewBaseActor(sys, BaseActorOptions{Name: "a"})
	b := NewBaseActor(sys, BaseActorOptions{Name: "b", Receive: func(ctx *Context, msg any) {
		if msg == "err" {
			ctx.Respond(nil, ErrCircuitOpen)
			return
		}
		ctx.Respond("ok", nil)
	}})
	a.Start()
	b.Start()
	defer a.Stop()
	defer b.Stop()

	_ = a.SendAsync(b, "x")
	_ = a.SendAsync(b, "x", SendOptions{})

	_, _, err := a.Ask(b, "x", SendOptions{Timeout: time.Second, AllowDegrade: true})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}

	cb := sys.breakerFor(b)
	now := time.Now()
	cb.open(now)
	if _, _, err := a.Ask(b, "x", SendOptions{Timeout: time.Second}); err != ErrCircuitOpen {
		t.Fatalf("expected open")
	}
}

func TestBaseActorHandleBranches(t *testing.T) {
	sys := NewSystem()
	a := NewBaseActor(sys, BaseActorOptions{Name: "a"})
	a.Start()
	defer a.Stop()

	_ = a.mb.Push(mailbox.Envelope{Meta: envelopeMeta{kind: envelopeKindRequest}, Payload: "bad"})
	_ = a.mb.Push(mailbox.Envelope{Meta: envelopeMeta{kind: envelopeKindResponse}, Payload: "bad"})
	_ = a.mb.Push(mailbox.Envelope{Meta: envelopeMeta{kind: envelopeKindUser}, Payload: "ok"})
	time.Sleep(5 * time.Millisecond)

	b := NewBaseActor(sys, BaseActorOptions{Name: "b"})
	b.receive = nil
	b.Start()
	defer b.Stop()
	_ = sys.Tell(nil, b, "x", SendOptions{})
}

func TestRemoteBranches(t *testing.T) {
	sysS := NewSystem()
	_ = sysS.RemoteAddr()
	if err := sysS.EnableRemote(""); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer sysS.StopRemote()
	if err := sysS.EnableRemote(":0"); err != nil {
		t.Fatalf("enable2: %v", err)
	}
	sysS.SetLocation("", "x")

	addr := sysS.RemoteAddr()
	conn, err := grpc.Dial(addr, grpc.WithInsecure(), grpc.WithDefaultCallOptions(grpc.ForceCodec(gobCodec{})))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	var ack remoteAck
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = conn.Invoke(ctx, "/deqinactor.Remote/Deliver", 123, &ack, grpc.ForceCodec(gobCodec{}))
}

func TestMailboxPushCloseBranch(t *testing.T) {
	m := mailbox.New(mailbox.Options{Capacity: 2, UrgentCapacity: 2, MaxSegments: 1, Policy: mailbox.BackpressureDropNewest})
	m.Close()
	_ = m.Push(mailbox.Envelope{})
}
