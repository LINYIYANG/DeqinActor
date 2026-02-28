package actor

import "time"

// IActor 是 Actor 的最小接口，定义生命周期和消息处理能力。
//
// 实现应该是并发安全的，以支持 Send 调用，
// 而 Receive 通常在 Actor 自己的单线程循环中执行。
type IActor interface {
	// Start 启动 Actor，开始处理消息。
	Start()
	// Stop 停止 Actor，停止接收和处理消息。
	Stop()
	// Receive 处理接收到的消息。
	Receive(msg any)
	// Send 向目标 Actor 发送消息。
	Send(target IActor, msg any)
}

// Priority 控制邮箱中的消息处理顺序。
// 优先级较高的消息会被优先处理。
type Priority uint8

const (
	// PriorityNormal 默认优先级，普通消息通道。
	PriorityNormal Priority = iota
	// PriorityUrgent 紧急优先级，优先于普通消息处理。
	// 用于响应消息和需要快速处理的重要消息。
	PriorityUrgent
)

// PriorityMessage 包装任意消息并指定显式优先级。
// 当消息需要特定优先级时使用此包装器。
type PriorityMessage struct {
	// Priority 消息的优先级
	Priority Priority
	// Msg 实际消息内容
	Msg any
}

// Request 表示请求-响应模式中的请求消息。
// CorrelationID 用于匹配对应的 Response。
type Request struct {
	// CorrelationID 关联 ID，用于匹配请求和响应
	CorrelationID string
	// Payload 请求负载
	Payload any
	// ReplyTo 响应应发送到的 Actor ID
	ReplyTo string
}

// Response 是对 Request 的响应，通过 CorrelationID 匹配。
type Response struct {
	// CorrelationID 关联 ID，与请求的 CorrelationID 匹配
	CorrelationID string
	// Value 响应值
	Value any
	// Err 响应错误（如果有）
	Err error
}

// ActorState 描述 Actor 实例的生命周期状态。
type ActorState uint8

const (
	// ActorStateNew 表示 Actor 尚未启动。
	ActorStateNew ActorState = iota
	// ActorStateRunning 表示 Actor 循环正在运行。
	ActorStateRunning
	// ActorStateStopping 表示已请求停止。
	ActorStateStopping
	// ActorStateStopped 表示 Actor 已完全停止。
	ActorStateStopped
)

// SendOptions 控制 Tell/Ask 操作的投递语义。
type SendOptions struct {
	// Priority 消息优先级
	Priority Priority
	// Persist 是否持久化此消息
	Persist bool
	// Timeout 请求超时时间（用于 Ask）
	Timeout time.Duration
	// OnComplete 完成时的回调函数
	OnComplete func(*Response)
	// AllowDegrade 是否允许同步请求降级为异步
	// 当系统饱和时，允许降级可以避免阻塞
	AllowDegrade bool
}
