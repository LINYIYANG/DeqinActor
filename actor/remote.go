package actor

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

// gobCodec 实现 gRPC 的 gob 编解码器。
// 使用 Go 原生的 gob 格式进行消息序列化，仅适用于 Go 程序之间的通信。
type gobCodec struct{}

// Name 返回编解码器名称 "gob"。
func (g gobCodec) Name() string { return "gob" }

// Marshal 使用 gob 编码将值序列化为字节切片。
func (g gobCodec) Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal 使用 gob 解码将字节切片反序列化为值。
func (g gobCodec) Unmarshal(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

// remoteEnvelope 是远程消息的信封结构，用于跨节点传输。
// 包含路由信息和序列化后的消息负载。
type remoteEnvelope struct {
	// ToID 目标 Actor 的 ID
	ToID string
	// FromID 发送者 Actor 的 ID
	FromID string
	// Kind 消息类型（用户消息、请求、响应）
	Kind envelopeKind
	// Priority 消息优先级
	Priority Priority
	// CorrelationID 请求-响应模式中的关联 ID
	CorrelationID string
	// ReplyTo 响应应发送到的 Actor ID
	ReplyTo string
	// Payload 序列化后的消息内容
	Payload []byte
}

// remoteAck 是远程消息投递的确认响应。
type remoteAck struct {
	// OK 表示投递是否成功
	OK bool
	// Err 错误信息（如果投递失败）
	Err string
}

// RemoteServer 定义远程消息投递服务接口。
type RemoteServer interface {
	Deliver(context.Context, *remoteEnvelope) (*remoteAck, error)
}

// remoteTransport 管理远程通信的传输层。
// 它包含 gRPC 服务器和客户端连接池，支持跨节点的 Actor 消息传递。
type remoteTransport struct {
	// sys 所属的 Actor 系统
	sys *System
	// server gRPC 服务器实例
	server *grpc.Server
	// lis 网络监听器
	lis net.Listener
	// addr 本地监听地址
	addr string

	// mu 保护 conns 的并发访问
	mu sync.Mutex
	// conns 到远程节点的连接池，按地址索引
	conns map[string]*grpc.ClientConn
}

// EnableRemote 启用远程通信功能。
// 在指定地址（默认 :50051）启动 gRPC 服务器，接收来自其他节点的消息。
// 启用后，可以通过 SetLocation 配置远程 Actor 的位置，实现跨节点消息路由。
func (s *System) EnableRemote(listenAddr string) error {
	if listenAddr == "" {
		listenAddr = ":50051"
	}
	if s.remote != nil {
		return nil
	}
	encoding.RegisterCodec(gobCodec{})
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	rt := &remoteTransport{
		sys:   s,
		lis:   lis,
		addr:  lis.Addr().String(),
		conns: make(map[string]*grpc.ClientConn),
	}
	rt.server = grpc.NewServer(grpc.ForceServerCodec(gobCodec{}))
	rt.register(rt.server)
	s.remote = rt
	go func() { _ = rt.server.Serve(lis) }()
	return nil
}

// RemoteAddr 返回远程通信的本地监听地址。
// 如果未启用远程通信，返回空字符串。
func (s *System) RemoteAddr() string {
	if s.remote == nil {
		return ""
	}
	return s.remote.addr
}

// StopRemote 停止远程通信功能。
// 关闭 gRPC 服务器和所有客户端连接。
func (s *System) StopRemote() {
	if s.remote == nil {
		return
	}
	s.remote.server.Stop()
	_ = s.remote.lis.Close()
	s.remote.mu.Lock()
	for _, c := range s.remote.conns {
		_ = c.Close()
	}
	s.remote.conns = nil
	s.remote.mu.Unlock()
	s.remote = nil
}

// SetLocation 设置 Actor 的远程位置。
// 当向该 Actor 发送消息时，如果本地找不到，会路由到指定的远程地址。
// 设置空地址会清除该 Actor 的位置信息。
func (s *System) SetLocation(actorID, addr string) {
	if actorID == "" {
		return
	}
	s.locMu.Lock()
	if s.locations == nil {
		s.locations = make(map[string]string)
	}
	if addr == "" {
		delete(s.locations, actorID)
	} else {
		s.locations[actorID] = addr
	}
	s.locMu.Unlock()
}

// locationOf 查询 Actor 的远程位置。
// 返回远程地址和一个布尔值表示是否找到。
func (s *System) locationOf(actorID string) (string, bool) {
	s.locMu.RLock()
	addr, ok := s.locations[actorID]
	s.locMu.RUnlock()
	return addr, ok
}

// remoteDeliver 向远程地址投递消息。
// 使用连接池管理 gRPC 连接，支持自动重连。
func (s *System) remoteDeliver(addr string, env *remoteEnvelope) error {
	if s.remote == nil {
		return errors.New("remote not enabled")
	}
	conn, err := s.remote.conn(addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ack remoteAck
	err = conn.Invoke(ctx, "/deqinactor.Remote/Deliver", env, &ack, grpc.ForceCodec(gobCodec{}))
	if err != nil {
		return err
	}
	if !ack.OK && ack.Err != "" {
		return errors.New(ack.Err)
	}
	return nil
}

// conn 获取或创建到指定地址的 gRPC 连接。
// 连接会被缓存以便复用。
func (rt *remoteTransport) conn(addr string) (*grpc.ClientConn, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if c, ok := rt.conns[addr]; ok {
		return c, nil
	}
	cc, err := grpc.Dial(addr, grpc.WithInsecure(), grpc.WithDefaultCallOptions(grpc.ForceCodec(gobCodec{})))
	if err != nil {
		return nil, err
	}
	rt.conns[addr] = cc
	return cc, nil
}

// register 向 gRPC 服务器注册远程消息投递服务。
func (rt *remoteTransport) register(srv *grpc.Server) {
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "deqinactor.Remote",
		HandlerType: (*RemoteServer)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Deliver",
				Handler: func(s any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					var in remoteEnvelope
					if err := dec(&in); err != nil {
						return nil, err
					}
					return rt.Deliver(ctx, &in)
				},
			},
		},
		Streams:  nil,
		Metadata: "gob",
	}, rt)
}

// Deliver 处理接收到的远程消息。
// 反序列化消息负载并投递到本地 Actor。
func (rt *remoteTransport) Deliver(_ context.Context, in *remoteEnvelope) (*remoteAck, error) {
	msg, err := rt.sys.serializer.Unmarshal(in.Payload)
	if err != nil {
		return &remoteAck{OK: false, Err: err.Error()}, nil
	}
	a, ok := rt.sys.registry.Get(in.ToID)
	if !ok {
		return &remoteAck{OK: false, Err: ErrActorNotFound.Error()}, nil
	}
	meta := envelopeMeta{
		kind:          in.Kind,
		fromID:        in.FromID,
		correlationID: in.CorrelationID,
		replyTo:       in.ReplyTo,
	}
	if ba, ok := a.(*BaseActor); ok {
		_ = ba.tell(in.FromID, msg, meta, in.Priority, false)
		return &remoteAck{OK: true}, nil
	}
	a.Receive(msg)
	return &remoteAck{OK: true}, nil
}
