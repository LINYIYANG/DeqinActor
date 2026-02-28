package actor

// envelopeKind 定义消息信封的类型，用于区分不同类型的消息处理方式。
type envelopeKind uint8

const (
	// envelopeKindUser 表示普通用户消息，由用户定义的 Receive 函数处理
	envelopeKindUser envelopeKind = iota
	// envelopeKindRequest 表示请求消息，用于请求-响应模式
	envelopeKindRequest
	// envelopeKindResponse 表示响应消息，用于完成 Future 或匹配请求
	envelopeKindResponse
)

// envelopeMeta 包含消息信封的元数据信息。
// 这些信息用于消息路由、请求-响应匹配和发送者追踪。
type envelopeMeta struct {
	// kind 消息类型，决定消息的处理方式
	kind envelopeKind
	// fromID 发送者的 Actor ID，用于回复或追踪
	fromID string
	// correlationID 关联 ID，用于请求-响应匹配
	correlationID string
	// replyTo 响应应发送到的 Actor ID
	replyTo string
}

