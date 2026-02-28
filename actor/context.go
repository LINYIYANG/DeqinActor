package actor

// Context 提供消息处理期间的执行上下文信息。
// 它包含当前 Actor 的引用、发送者信息以及请求-响应模式所需的元数据。
// Context 在每次消息处理时创建，不应在消息处理完成后保存。
type Context struct {
	// system 指向所属的 Actor 系统，用于发送消息和访问系统级功能
	system *System
	// self 指向当前处理消息的 Actor 实例
	self *BaseActor
	// senderID 发送消息的 Actor ID，可能为空
	senderID string
	// correlationID 请求-响应模式中的关联 ID，用于匹配请求和响应
	correlationID string
	// replyTo 响应应发送到的 Actor ID
	replyTo string
}

// Self 返回当前处理消息的 Actor 实例。
// 可用于在消息处理中向自己发送消息或访问 Actor 属性。
func (c *Context) Self() *BaseActor { return c.self }

// SenderID 返回消息发送者的 Actor ID。
// 如果发送者未知或消息不是通过 Tell/Ask 发送的，则返回空字符串。
func (c *Context) SenderID() string { return c.senderID }

// CorrelationID 返回当前请求的关联 ID。
// 在请求-响应模式中，此 ID 用于将响应与原始请求匹配。
func (c *Context) CorrelationID() string { return c.correlationID }

// Respond 向请求发送者发送响应消息。
// 仅在处理 Request 类型消息时有效，其他情况下此方法不执行任何操作。
// 参数 value 为响应值，err 为可能的错误信息。
func (c *Context) Respond(value any, err error) {
	if c.correlationID == "" || c.replyTo == "" || c.system == nil {
		return
	}
	c.system.sendResponse(c.self.id, c.replyTo, &Response{
		CorrelationID: c.correlationID,
		Value:         value,
		Err:           err,
	})
}
