# DeqinActor

## 中文

DeqinActor 是一个面向 Go 1.21+ 的通用 Actor 并发框架，核心目标是：高复用、低开销、可观测、可扩展（可选 gRPC 远程透明通信），并尽量保持零外部依赖（除 gRPC）。

### 架构

```mermaid
flowchart LR
  A[Caller Actor/BaseActor] -->|Tell/SendAsync/Ask| S[System]
  S --> R[Registry]
  S --> L[RateLimiter(TokenBucket)]
  S --> M[Metrics(/metrics)]
  S -->|local| MB[Mailbox(urgent+normal)]
  S -->|remote| GRPC[gRPC Transport]
  MB --> B[Actor Loop]
  B --> H[Receive(ctx,msg)]
  H -->|ctx.Respond| S
```

### 功能要点

- Actor 抽象：IActor + BaseActor（生命周期、邮箱、panic 恢复、日志）
- 注册与寻址：按 ID/Name 查找本地 Actor；支持位置透明（ID -> 地址）远程路由
- 异步通信：无锁环形队列 + 分段扩容；优先级（urgent 插队）；Future[*Response]（超时/回调/组合）
- 同步通信：SyncAsk（默认 5s）；断路器（50 次失败阈值/30s 熔断/半开探测）；可选线程池饱和降级到异步
- 监督：OneForOne / OneForAll / RestForOne + 重试次数 + 退避算法
- 监控：内置 Prometheus 文本格式指标（吞吐、延迟直方图、邮箱堆积、重启次数）
- 流控：令牌桶限流，支持动态调整 QPS
- 持久化：可选 WAL（邮箱消息持久化与重放）
- TestKit：Probe 断言、虚拟时间、故障注入

### 快速开始

```go
sys := actor.NewSystem()
_ = sys.EnableMetrics(":0")
sys.EnableRateLimit(200000, 200000)

pong := actor.NewBaseActor(sys, actor.BaseActorOptions{
  Name: "pong",
  Receive: func(ctx *actor.Context, msg any) {
    if msg == "ping" {
      ctx.Respond("pong", nil)
    }
  },
})

ping := actor.NewBaseActor(sys, actor.BaseActorOptions{Name: "ping"})
ping.Start()
pong.Start()
defer ping.Stop()
defer pong.Stop()

resp, err := ping.SyncAsk(pong, "ping", actor.SendOptions{Timeout: time.Second})
_ = resp
_ = err
```

### 分布式（gRPC 透明通信）

```go
sysA := actor.NewSystem()
sysB := actor.NewSystem()
_ = sysA.EnableRemote("127.0.0.1:0")
_ = sysB.EnableRemote("127.0.0.1:0")

remote := actor.NewBaseActor(sysB, actor.BaseActorOptions{ID: "remoteB", Receive: func(_ *actor.Context, msg any) {}})
remote.Start()

sysA.SetLocation("remoteB", sysB.RemoteAddr())
refB := sysA.Ref("remoteB")
_ = sysA.Tell(nil, refB, "hello", actor.SendOptions{})
```

### 基准与压测

- 单元测试与竞态检测：
  - `go test ./...`
  - `go test -race ./...`
- 建议基准（示例）：
  - `go test -run Test -bench . -benchmem ./...`
  - 可按业务消息类型（小对象/大对象/带响应）分别建模压测

### 最佳实践

- 消息类型保持小而稳定，尽量避免在高频路径里做序列化/反序列化
- 优先使用 `SendAsync` + Future 组合编排；对外部依赖调用使用 `SyncAsk` + 断路器
- 需要低延迟时避免在 Receive 中做阻塞 IO；可将 IO 下沉到专用 Actor
- 启用限流与监控，观察邮箱堆积和 P99 延迟，及时调整 QPS、邮箱策略与退避参数
- 使用持久化时，明确哪些消息需要 Persist，避免全量持久化带来的 IO 放大

## English

DeqinActor is a reusable actor concurrency framework for Go 1.21+. It targets high throughput, low overhead, observability, and optional distributed transparency via gRPC, with zero external dependencies except gRPC.

### Highlights

- Actor abstraction: IActor + BaseActor (lifecycle, mailbox, panic recovery, logging)
- Registry & addressing: local lookup by ID/Name; location transparency (ID -> address) for remote routing
- Async: lock-free ring + segmented growth; urgent priority lane; Future[*Response] (timeouts/callbacks/composition)
- Sync: SyncAsk (default 5s); circuit breaker (50 failures / 30s open / half-open probe); optional degrade-to-async on saturation
- Supervision: OneForOne / OneForAll / RestForOne with retries and backoff
- Metrics: built-in Prometheus text exposition (throughput, histogram latency, backlog, restarts)
- Rate limiting: token-bucket with dynamic QPS updates
- Persistence: optional WAL replay for mailbox recovery
- TestKit: probes, virtual time, fault injection

### Testing

- `go test ./...`
- `go test -race ./...`

