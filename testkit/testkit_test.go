package testkit

import (
	"math/rand"
	"testing"
	"time"
)

func TestProbe(t *testing.T) {
	p := NewProbe(t, 1)
	_ = p.Chan()
	p.Put(1)
	if got := p.Expect(50 * time.Millisecond); got.(int) != 1 {
		t.Fatalf("unexpected: %#v", got)
	}
	p.ExpectNoMessage(10 * time.Millisecond)
	NewProbe(t, 0).ExpectNoMessage(0)

	var failed int
	p.fail = func(string, ...any) { failed++ }
	if v := p.Expect(5 * time.Millisecond); v != nil || failed != 1 {
		t.Fatalf("expected timeout failure")
	}
	p.Put(2)
	if v := p.Expect(0); v.(int) != 2 {
		t.Fatalf("expected 2")
	}
	p.Put("x")
	p.ExpectNoMessage(5 * time.Millisecond)
	if failed != 2 {
		t.Fatalf("expected unexpected-message failure")
	}
}

func TestFakeClock(t *testing.T) {
	c := NewFakeClock(time.Unix(0, 0))
	_ = c.Now()
	_ = NewFakeClock(time.Time{}).Now()
	ch := c.After(10 * time.Second)
	c.Advance(9 * time.Second)
	select {
	case <-ch:
		t.Fatalf("should not fire")
	default:
	}
	c.Advance(2 * time.Second)
	select {
	case <-ch:
	default:
		t.Fatalf("should fire")
	}
}

func TestChaos(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	c := Chaos{DropProbability: 1, MaxDelay: 0, Rand: r}
	called := false
	if ok := c.Apply(func() { called = true }); ok || called {
		t.Fatalf("expected drop")
	}
	c = Chaos{DropProbability: 0, MaxDelay: 50 * time.Microsecond, Rand: r}
	if ok := c.Apply(func() { called = true }); !ok || !called {
		t.Fatalf("expected call")
	}
	c = Chaos{DropProbability: 0, MaxDelay: 0, Rand: nil}
	if ok := c.Apply(func() {}); !ok {
		t.Fatalf("expected ok")
	}
}
