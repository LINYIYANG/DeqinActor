package actor

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExamplePingPong(t *testing.T) {
	sys := NewSystem()
	pong := NewBaseActor(sys, BaseActorOptions{Name: "pong", Receive: func(ctx *Context, msg any) {
		if msg == "ping" {
			ctx.Respond("pong", nil)
		}
	}})
	ping := NewBaseActor(sys, BaseActorOptions{Name: "ping"})
	ping.Start()
	pong.Start()
	defer ping.Stop()
	defer pong.Stop()

	resp, err := ping.SyncAsk(pong, "ping", SendOptions{Timeout: time.Second})
	if err != nil || resp.Value.(string) != "pong" {
		t.Fatalf("unexpected: %#v %v", resp, err)
	}
}

func TestExampleWordCount(t *testing.T) {
	sys := NewSystem()
	wc := NewBaseActor(sys, BaseActorOptions{Name: "wc", Receive: func(ctx *Context, msg any) {
		s := msg.(string)
		m := map[string]int{}
		for _, w := range strings.Fields(s) {
			m[w]++
		}
		ctx.Respond(m, nil)
	}})
	client := NewBaseActor(sys, BaseActorOptions{Name: "client"})
	client.Start()
	wc.Start()
	defer client.Stop()
	defer wc.Stop()

	resp, err := client.SyncAsk(wc, "go go actor", SendOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	m := resp.Value.(map[string]int)
	if m["go"] != 2 || m["actor"] != 1 {
		t.Fatalf("unexpected: %#v", m)
	}
}

func TestExampleMapReduce(t *testing.T) {
	sys := NewSystem()
	reducer := NewBaseActor(sys, BaseActorOptions{Name: "reducer", Receive: func(ctx *Context, msg any) {
		pairs := msg.([][2]int)
		sum := 0
		for _, p := range pairs {
			sum += p[1]
		}
		ctx.Respond(sum, nil)
	}})
	mapper := NewBaseActor(sys, BaseActorOptions{Name: "mapper", Receive: func(ctx *Context, msg any) {
		nums := msg.([]int)
		out := make([][2]int, 0, len(nums))
		for _, n := range nums {
			out = append(out, [2]int{n, n * n})
		}
		ctx.Respond(out, nil)
	}})
	client := NewBaseActor(sys, BaseActorOptions{Name: "client"})
	client.Start()
	mapper.Start()
	reducer.Start()
	defer client.Stop()
	defer mapper.Stop()
	defer reducer.Stop()

	mapped, err := client.SyncAsk(mapper, []int{1, 2, 3, 4}, SendOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("map err: %v", err)
	}
	reduced, err := client.SyncAsk(reducer, mapped.Value.([][2]int), SendOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("reduce err: %v", err)
	}
	if reduced.Value.(int) != (1 + 4 + 9 + 16) {
		t.Fatalf("unexpected: %#v", reduced.Value)
	}
}

func TestExampleFizzBuzz(t *testing.T) {
	sys := NewSystem()
	fb := NewBaseActor(sys, BaseActorOptions{Name: "fb", Receive: func(ctx *Context, msg any) {
		n := msg.(int)
		out := ""
		if n%3 == 0 {
			out += "Fizz"
		}
		if n%5 == 0 {
			out += "Buzz"
		}
		if out == "" {
			out = strconv.Itoa(n)
		}
		ctx.Respond(out, nil)
	}})
	client := NewBaseActor(sys, BaseActorOptions{Name: "client"})
	client.Start()
	fb.Start()
	defer client.Stop()
	defer fb.Stop()

	resp, err := client.SyncAsk(fb, 15, SendOptions{Timeout: time.Second})
	if err != nil || resp.Value.(string) != "FizzBuzz" {
		t.Fatalf("unexpected: %#v %v", resp, err)
	}
}
