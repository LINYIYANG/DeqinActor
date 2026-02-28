package actor

import (
	"log"
	"os"
	"sync"
	"sync/atomic"

	"deqinactor/mailbox"
	"deqinactor/persistence"
)

// ReceiveFunc 是用户定义的消息处理函数。
// Context 提供消息处理期间的执行上下文，msg 为接收到的消息。
type ReceiveFunc func(*Context, any)

// BaseActor 是一个可复用的 Actor 实现，提供以下功能：
//   - 高吞吐量邮箱（支持紧急优先级通道）
//   - 生命周期管理（Start/Stop）和 ID/名称注册
//   - Panic 恢复和失败通知到 System
//   - 可选的基于 WAL 的消息重放（启用持久化时）
//
// BaseActor 是框架的核心 Actor 实现，用户通过提供 ReceiveFunc 来定义消息处理逻辑。
// 每个 BaseActor 在独立的 goroutine 中运行，通过邮箱接收消息。
type BaseActor struct {
	// id Actor 的唯一标识符
	id string
	// name Actor 的可选人类可读名称
	name string

	// system 所属的 Actor 系统
	system *System
	// mb 高吞吐量邮箱
	mb *mailbox.Mailbox
	// receive 用户定义的消息处理函数
	receive ReceiveFunc
	// wal 用于持久化的预写日志
	wal *persistence.WAL

	// state Actor 的当前状态
	state atomic.Uint32

	// startOnce 确保 Start 只执行一次
	startOnce sync.Once
	// stopOnce 确保 Stop 只执行一次
	stopOnce sync.Once
	// done 在 Actor 停止时关闭的通道
	done chan struct{}
}

// BaseActorOptions 配置 BaseActor 实例。
type BaseActorOptions struct {
	// ID 可选的稳定标识符。如果为空，将生成新 ID。
	ID string
	// Name 可选的人类可读名称，注册到 System 注册表中。
	Name string
	// Mailbox 邮箱配置，控制容量/背压/持久化行为。
	Mailbox mailbox.Options
	// Receive 消息处理函数。
	Receive ReceiveFunc
}

// NewBaseActor 构造一个绑定到 System 的 BaseActor。
// 如果 System 启用了持久化且未提供邮箱持久化钩子，
// 会自动为该 Actor 配置 WAL，并在启动时重放持久化的消息。
func NewBaseActor(sys *System, opts BaseActorOptions) *BaseActor {
	a := &BaseActor{
		id:      opts.ID,
		name:    opts.Name,
		system:  sys,
		receive: opts.Receive,
		done:    make(chan struct{}),
	}
	if a.id == "" {
		a.id = NewActorID()
	}
	if sys != nil && sys.persistDir != "" && opts.Mailbox.Persist == nil {
		_ = os.MkdirAll(sys.persistDir, 0755)
		wal, err := persistence.Open(sys.actorWALPath(a.id))
		if err == nil {
			a.wal = wal
			opts.Mailbox.Persist = wal.Append
			opts.Mailbox.EncodeForPersist = func(v any) ([]byte, bool) {
				b, err := sys.serializer.Marshal(v)
				return b, err == nil
			}
		}
	}
	a.mb = mailbox.New(opts.Mailbox)
	a.state.Store(uint32(ActorStateNew))
	return a
}

// ID 返回 Actor 的唯一标识符。
func (a *BaseActor) ID() string { return a.id }

// Name 返回 Actor 的注册名称（可能为空）。
func (a *BaseActor) Name() string { return a.name }

// Start 注册 Actor 并启动其邮箱处理循环。
// 如果启用了持久化，会先重放 WAL 中的消息。
// 此方法是幂等的，多次调用只会执行一次。
func (a *BaseActor) Start() {
	a.startOnce.Do(func() {
		if a.wal != nil && a.system != nil {
			if recs, err := a.wal.Replay(); err == nil {
				for _, b := range recs {
					v, err := a.system.serializer.Unmarshal(b)
					if err == nil {
						_ = a.mb.Push(mailbox.Envelope{Priority: uint8(PriorityNormal), Payload: v, Meta: envelopeMeta{kind: envelopeKindUser}, Persist: false})
					}
				}
			}
		}
		if a.system != nil {
			a.system.registry.Register(a.id, a.name, a)
		}
		a.state.Store(uint32(ActorStateRunning))
		go a.run()
	})
}

// Stop 关闭邮箱，等待处理循环退出，并注销 Actor。
// 此方法是幂等的，多次调用只会执行一次。
func (a *BaseActor) Stop() {
	a.stopOnce.Do(func() {
		a.state.Store(uint32(ActorStateStopping))
		a.mb.Close()
		<-a.done
		a.state.Store(uint32(ActorStateStopped))
		if a.system != nil {
			a.system.registry.Unregister(a.id, a.name)
		}
		if a.wal != nil {
			_ = a.wal.Close()
		}
	})
}

// Receive 直接向用户处理函数投递消息。
// 正常使用中，消息通过 Tell/SendAsync/Ask 推送到邮箱。
// 此方法主要用于直接调用或测试。
func (a *BaseActor) Receive(msg any) {
	if a.receive == nil {
		return
	}
	a.receive(&Context{system: a.system, self: a}, msg)
}

// Send 是从该 Actor 向目标发送消息的便捷方法。
// 使用默认发送选项调用 System.Tell。
func (a *BaseActor) Send(target IActor, msg any) {
	if a.system == nil || target == nil {
		return
	}
	a.system.Tell(a, target, msg, SendOptions{})
}

// tell 将消息推送到 Actor 的邮箱。
// 这是内部消息投递的核心方法。
func (a *BaseActor) tell(fromID string, msg any, meta envelopeMeta, pri Priority, persist bool) error {
	env := mailbox.Envelope{
		Priority: uint8(pri),
		Payload:  msg,
		Meta:     meta,
		Persist:  persist,
	}
	return a.mb.Push(env)
}

// run 是 Actor 的主处理循环。
// 从邮箱弹出消息并调用 handle 处理，直到邮箱关闭。
func (a *BaseActor) run() {
	defer close(a.done)
	for {
		env, ok := a.mb.Pop()
		if ok {
			a.handle(env)
			continue
		}
		if !a.mb.Wait() {
			return
		}
	}
}

// handle 处理单个消息信封。
// 根据消息类型（用户消息、请求、响应）分发到相应的处理逻辑。
// 包含 panic 恢复，发生 panic 时通知 System 并停止 Actor。
func (a *BaseActor) handle(env mailbox.Envelope) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("actor panic id=%s name=%s err=%v", a.id, a.name, r)
			if a.system != nil {
				a.system.notifyFailure(a.id, r)
			}
			a.mb.Close()
			go a.Stop()
		}
	}()
	if a.system != nil && a.system.metrics != nil {
		a.system.metrics.IncIn()
	}
	meta, _ := env.Meta.(envelopeMeta)
	switch meta.kind {
	case envelopeKindResponse:
		if resp, ok := env.Payload.(*Response); ok && a.system != nil {
			a.system.completeFuture(resp)
		}
		return
	case envelopeKindRequest:
		req, _ := env.Payload.(*Request)
		if req == nil {
			return
		}
		if a.receive == nil {
			return
		}
		ctx := &Context{
			system:        a.system,
			self:          a,
			senderID:      meta.fromID,
			correlationID: req.CorrelationID,
			replyTo:       req.ReplyTo,
		}
		a.receive(ctx, req.Payload)
		return
	default:
		if a.receive == nil {
			return
		}
		ctx := &Context{system: a.system, self: a, senderID: meta.fromID}
		a.receive(ctx, env.Payload)
	}
}
