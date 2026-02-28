package actor

import (
	"errors"
	"path/filepath"
	"sync"
	"time"
)

// ErrActorNotFound 当目标 Actor 在本地和远程都无法找到时返回此错误。
var ErrActorNotFound = errors.New("actor not found")

// System 是 Actor 的运行时容器，提供以下核心功能：
//   - 本地 Actor 注册表（支持按 ID 和可选名称查找）
//   - 请求/响应关联机制，用于 Future 完成
//   - 断路器和可选的饱和度降级机制
//   - 可选的持久化、指标收集、限流和远程传输
//
// System 是 Actor 框架的核心，所有 Actor 都在 System 中创建和管理。
// 一个应用程序通常只需要一个 System 实例。
type System struct {
	// registry 本地 Actor 注册表
	registry *Registry

	// pendingMu 保护 pending 的并发访问
	pendingMu sync.Mutex
	// pending 等待响应的 Future 映射，按 correlationID 索引
	pending map[string]any

	// breakerMu 保护 breakers 的并发访问
	breakerMu sync.Mutex
	// breakers 每个目标 Actor 的断路器，按 Actor ID 索引
	breakers map[string]*CircuitBreaker

	// waitTokens 用于限制同步等待数量的令牌通道
	// 防止过多 goroutine 阻塞在 Ask 调用上
	waitTokens chan struct{}

	// serializer 消息序列化器，用于持久化和远程传输
	serializer Serializer
	// persistDir 持久化存储目录
	persistDir string

	// metrics 指标收集器
	metrics *Metrics
	// limiter 令牌桶限流器
	limiter *TokenBucket
	// remote 远程传输层
	remote *remoteTransport

	// locMu 保护 locations 的并发访问
	locMu sync.RWMutex
	// locations Actor ID 到远程地址的映射
	locations map[string]string

	// failMu 保护 failSub 的并发访问
	failMu sync.Mutex
	// failSub 失败通知订阅者列表
	failSub []func(actorID string, reason any)
}

// NewSystem 创建一个新的 Actor 运行时，使用默认设置。
// 默认使用 GobSerializer 作为序列化器，等待令牌池大小为 4096。
func NewSystem() *System {
	return &System{
		registry:   NewRegistry(),
		pending:    make(map[string]any),
		breakers:   make(map[string]*CircuitBreaker),
		waitTokens: make(chan struct{}, 4096),
		serializer: &GobSerializer{},
	}
}

// Registry 返回运行时注册表，用于 Actor 查找和注册。
func (s *System) Registry() *Registry { return s.registry }

// Serializer 返回用于持久化和远程传输的序列化器。
func (s *System) Serializer() Serializer { return s.serializer }

// SetSerializer 设置用于持久化和远程传输的序列化器。
// 如果 serializer 为 nil，调用将被忽略。
func (s *System) SetSerializer(serializer Serializer) {
	if serializer == nil {
		return
	}
	s.serializer = serializer
}

// EnablePersistence 在指定目录下启用每个 Actor 的 WAL 持久化。
// 持久化的消息会在 Actor 重启时重放。
func (s *System) EnablePersistence(dir string) {
	s.persistDir = dir
}

// EnableRateLimit 启用令牌桶限流器，限制出站消息投递速率。
// qps 为每秒允许的请求数，burst 为突发容量。
func (s *System) EnableRateLimit(qps int64, burst int64) {
	s.limiter = NewTokenBucket(qps, burst)
}

// SetQPS 更新限流器的 QPS。
// 如果限流未启用，会初始化一个新的限流器。
func (s *System) SetQPS(qps int64) {
	if s.limiter == nil {
		s.limiter = NewTokenBucket(qps, qps)
		return
	}
	s.limiter.SetQPS(qps)
}

// actorWALPath 返回指定 Actor 的 WAL 文件路径。
func (s *System) actorWALPath(actorID string) string {
	if s.persistDir == "" || actorID == "" {
		return ""
	}
	return filepath.Join(s.persistDir, actorID+".wal")
}

// deliverToID 向指定 ID 的 Actor 投递消息。
// 首先尝试本地投递，如果本地找不到则尝试远程投递。
func (s *System) deliverToID(toID string, meta envelopeMeta, pri Priority, payload any, persist bool) error {
	if toID == "" {
		return ErrActorNotFound
	}
	if a, ok := s.registry.Get(toID); ok {
		if ba, ok := a.(*BaseActor); ok {
			return ba.tell(meta.fromID, payload, meta, pri, persist)
		}
		a.Receive(payload)
		return nil
	}
	if addr, ok := s.locationOf(toID); ok {
		b, err := s.serializer.Marshal(payload)
		if err != nil {
			return err
		}
		env := &remoteEnvelope{
			ToID:          toID,
			FromID:        meta.fromID,
			Kind:          meta.kind,
			Priority:      pri,
			CorrelationID: meta.correlationID,
			ReplyTo:       meta.replyTo,
			Payload:       b,
		}
		return s.remoteDeliver(addr, env)
	}
	return ErrActorNotFound
}

// FindByID 通过 ID 查找本地 Actor。
func (s *System) FindByID(id string) (IActor, bool) { return s.registry.Get(id) }

// FindByName 通过注册名称查找本地 Actor。
func (s *System) FindByName(name string) (IActor, bool) { return s.registry.GetByName(name) }

// Tell 向目标 Actor 投递单向消息（即发即忘）。
// 如果目标可以解析为 ID 且已知远程位置，消息会被路由到远程节点。
// 参数 from 为发送者 Actor（可为 nil），target 为目标 Actor，msg 为消息内容，opts 为发送选项。
func (s *System) Tell(from *BaseActor, target IActor, msg any, opts SendOptions) error {
	if target == nil {
		return ErrActorNotFound
	}
	if s.limiter != nil {
		s.limiter.Wait(1)
	}
	if s.metrics != nil {
		s.metrics.IncOut()
	}
	pri := opts.Priority
	payload := msg
	if pm, ok := msg.(PriorityMessage); ok {
		pri = pm.Priority
		payload = pm.Msg
	}
	meta := envelopeMeta{kind: envelopeKindUser}
	if from != nil {
		meta.fromID = from.id
	}
	if a, ok := target.(*BaseActor); ok {
		return a.tell(meta.fromID, payload, meta, pri, opts.Persist)
	}
	if ref, ok := target.(*ActorRef); ok {
		return s.deliverToID(ref.id, meta, pri, payload, opts.Persist)
	}
	if t, ok := target.(interface{ ID() string }); ok {
		if id := t.ID(); id != "" {
			return s.deliverToID(id, meta, pri, payload, opts.Persist)
		}
	}
	target.Receive(payload)
	return nil
}

// sendRequest 向目标 Actor 发送请求消息。
// 用于 Ask/SendAsync 等请求-响应模式。
func (s *System) sendRequest(from *BaseActor, to IActor, req *Request, pri Priority, persist bool) error {
	if s.limiter != nil {
		s.limiter.Wait(1)
	}
	if s.metrics != nil {
		s.metrics.IncOut()
	}
	meta := envelopeMeta{
		kind:          envelopeKindRequest,
		fromID:        "",
		correlationID: req.CorrelationID,
		replyTo:       req.ReplyTo,
	}
	if from != nil {
		meta.fromID = from.id
	}
	if a, ok := to.(*BaseActor); ok {
		return a.tell(meta.fromID, req, meta, pri, persist)
	}
	if ref, ok := to.(*ActorRef); ok {
		return s.deliverToID(ref.id, meta, pri, req, persist)
	}
	if t, ok := to.(interface{ ID() string }); ok {
		if id := t.ID(); id != "" {
			return s.deliverToID(id, meta, pri, req, persist)
		}
	}
	to.Receive(req)
	return nil
}

// sendResponse 向目标 Actor 发送响应消息。
// 响应消息使用紧急优先级投递。
func (s *System) sendResponse(fromID, toID string, resp *Response) error {
	if s.limiter != nil {
		s.limiter.Wait(1)
	}
	if s.metrics != nil {
		s.metrics.IncOut()
	}
	meta := envelopeMeta{
		kind:          envelopeKindResponse,
		fromID:        fromID,
		correlationID: resp.CorrelationID,
	}
	return s.deliverToID(toID, meta, PriorityUrgent, resp, false)
}

// completeFuture 完成一个等待中的 Future。
// 通过 correlationID 找到对应的 Future 并设置响应结果。
func (s *System) completeFuture(resp *Response) {
	s.pendingMu.Lock()
	f, ok := s.pending[resp.CorrelationID]
	if ok {
		delete(s.pending, resp.CorrelationID)
	}
	s.pendingMu.Unlock()
	if ok {
		if c, ok := f.(interface{ complete(*Response) }); ok {
			c.complete(resp)
		}
	}
}

// TrackPending 跟踪一个等待响应的 Future。
// 如果设置了超时，超时后 Future 会自动完成并返回超时错误。
func (s *System) TrackPending(correlationID string, v any, timeout time.Duration) {
	s.pendingMu.Lock()
	s.pending[correlationID] = v
	s.pendingMu.Unlock()
	if timeout > 0 {
		time.AfterFunc(timeout, func() {
			s.pendingMu.Lock()
			f, ok := s.pending[correlationID]
			if ok {
				delete(s.pending, correlationID)
			}
			s.pendingMu.Unlock()
			if ok {
				if c, ok := f.(interface{ complete(*Response) }); ok {
					c.complete(&Response{CorrelationID: correlationID, Err: ErrAskTimeout})
				}
			}
		})
	}
}

// breakerFor 获取或创建目标 Actor 的断路器。
// 断路器用于防止对失败服务的持续请求。
func (s *System) breakerFor(target IActor) *CircuitBreaker {
	if target == nil {
		return nil
	}
	var id string
	if a, ok := target.(*BaseActor); ok {
		id = a.id
	} else if t, ok := target.(interface{ ID() string }); ok {
		id = t.ID()
	}
	if id == "" {
		return nil
	}
	s.breakerMu.Lock()
	b, ok := s.breakers[id]
	if !ok {
		b = NewCircuitBreaker(50, 30*time.Second)
		s.breakers[id] = b
	}
	s.breakerMu.Unlock()
	return b
}

// tryAcquireWaitToken 尝试获取一个等待令牌，不阻塞。
// 用于判断是否可以执行同步等待，或需要降级为异步。
func (s *System) tryAcquireWaitToken() bool {
	select {
	case s.waitTokens <- struct{}{}:
		return true
	default:
		return false
	}
}

// acquireWaitToken 获取一个等待令牌，可能阻塞。
func (s *System) acquireWaitToken() { s.waitTokens <- struct{}{} }

// releaseWaitToken 释放一个等待令牌。
func (s *System) releaseWaitToken() {
	select {
	case <-s.waitTokens:
	default:
	}
}

// SubscribeFailures 订阅 Actor 失败通知。
// 当 Actor 发生 panic 时，订阅者会收到通知。
func (s *System) SubscribeFailures(fn func(actorID string, reason any)) {
	s.failMu.Lock()
	s.failSub = append(s.failSub, fn)
	s.failMu.Unlock()
}

// notifyFailure 通知所有订阅者 Actor 失败事件。
func (s *System) notifyFailure(actorID string, reason any) {
	s.failMu.Lock()
	subs := append([]func(string, any){}, s.failSub...)
	s.failMu.Unlock()
	for _, fn := range subs {
		fn(actorID, reason)
	}
}
