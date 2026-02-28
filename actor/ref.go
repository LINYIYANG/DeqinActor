package actor

// ActorRef 是对 Actor 的轻量级引用，实现了 IActor 接口。
// 它只持有 Actor ID 和 System 引用，不包含实际的 Actor 状态。
// ActorRef 可用于向远程 Actor 或尚未创建的 Actor 发送消息。
// 当目标 Actor 不在本地时，消息会被路由到远程位置（如果已配置）。
type ActorRef struct {
	// sys 指向所属的 Actor 系统
	sys *System
	// id 目标 Actor 的唯一标识符
	id string
}

// ID 返回引用的 Actor ID。
func (r *ActorRef) ID() string { return r.id }

// Start 是空操作，ActorRef 不需要启动。
// 实现 IActor 接口以保持一致性。
func (r *ActorRef) Start() { _ = r.id }

// Stop 是空操作，ActorRef 不需要停止。
// 实现 IActor 接口以保持一致性。
func (r *ActorRef) Stop() { _ = r.id }

// Receive 是空操作，ActorRef 不能直接接收消息。
// 实现 IActor 接口以保持一致性。
func (r *ActorRef) Receive(_ any) { _ = r.id }

// Send 向目标 Actor 发送消息。
// 通过 System 的 Tell 方法实现消息投递。
func (r *ActorRef) Send(target IActor, msg any) {
	if r.sys == nil {
		return
	}
	_ = r.sys.Tell(nil, target, msg, SendOptions{})
}

// Ref 创建一个指向指定 ID 的 ActorRef。
// 返回的 ActorRef 可用于向该 ID 对应的 Actor 发送消息，
// 无论 Actor 是本地的还是远程的。
func (s *System) Ref(id string) *ActorRef { return &ActorRef{sys: s, id: id} }
