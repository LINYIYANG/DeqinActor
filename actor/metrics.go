package actor

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync/atomic"
	"time"
)

// Metrics 收集和暴露 Actor 系统的运行时指标。
// 指标包括消息计数、延迟分布、重启次数和运行时间等。
// 所有指标都使用原子操作，支持并发访问且无锁竞争。
// 指标格式兼容 Prometheus，可通过 /metrics 端点暴露。
type Metrics struct {
	// startedAtUnix 系统启动时间的 Unix 时间戳
	startedAtUnix atomic.Int64
	// msgOut 发出的消息总数
	msgOut atomic.Uint64
	// msgIn 接收的消息总数
	msgIn atomic.Uint64
	// restarts Actor 重启的总次数
	restarts atomic.Uint64

	// latBuckets 延迟直方图的桶边界
	latBuckets []time.Duration
	// latCounts 每个延迟桶的计数
	latCounts []atomic.Uint64
	// latSumNS 延迟总和（纳秒），用于计算平均延迟
	latSumNS atomic.Uint64
}

// NewMetrics 创建一个新的指标收集器，使用预定义的延迟桶边界。
// 延迟桶覆盖从 10 微秒到 100 毫秒的范围，适合大多数 Actor 通信场景。
func NewMetrics() *Metrics {
	b := []time.Duration{
		10 * time.Microsecond,
		50 * time.Microsecond,
		100 * time.Microsecond,
		500 * time.Microsecond,
		1 * time.Millisecond,
		2 * time.Millisecond,
		5 * time.Millisecond,
		10 * time.Millisecond,
		20 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
	}
	return &Metrics{
		latBuckets: b,
		latCounts:  make([]atomic.Uint64, len(b)+1),
	}
}

// MarkStart 记录系统启动时间。
// 仅在首次调用时生效，后续调用被忽略。
func (m *Metrics) MarkStart() {
	if m.startedAtUnix.Load() == 0 {
		m.startedAtUnix.Store(time.Now().Unix())
	}
}

// IncOut 增加发出消息计数。
func (m *Metrics) IncOut() { m.msgOut.Add(1) }

// IncIn 增加接收消息计数。
func (m *Metrics) IncIn() { m.msgIn.Add(1) }

// IncRestart 增加 Actor 重启计数。
func (m *Metrics) IncRestart() { m.restarts.Add(1) }

// ObserveLatency 记录一次延迟观测值。
// 延迟被分配到相应的桶中，并累加到总延迟。
func (m *Metrics) ObserveLatency(d time.Duration) {
	if d < 0 {
		return
	}
	m.latSumNS.Add(uint64(d.Nanoseconds()))
	i := sort.Search(len(m.latBuckets), func(i int) bool { return d <= m.latBuckets[i] })
	m.latCounts[i].Add(1)
}

// EnableMetrics 启用指标收集和 HTTP 暴露端点。
// 指标将在指定地址（默认 :9090）的 /metrics 路径下以 Prometheus 格式暴露。
// 此方法应在 System 创建后、Actor 启动前调用。
func (s *System) EnableMetrics(addr string) error {
	if addr == "" {
		addr = ":9090"
	}
	if s.metrics == nil {
		s.metrics = NewMetrics()
	}
	s.metrics.MarkStart()
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) { s.writeMetrics(w) })
	go func() { _ = http.ListenAndServe(addr, mux) }()
	return nil
}

// writeMetrics 将指标以 Prometheus 文本格式写入 HTTP 响应。
// 包含消息计数、邮箱积压、重启次数、延迟直方图和运行时间等指标。
func (s *System) writeMetrics(w http.ResponseWriter) {
	if s.metrics == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	now := time.Now()
	snap := s.registry.Snapshot()
	var backlog int64
	for _, a := range snap {
		if ba, ok := a.(*BaseActor); ok && ba.mb != nil {
			backlog += ba.mb.Len()
		}
	}

	_, _ = fmt.Fprintln(w, "# TYPE deqinactor_messages_out_total counter")
	_, _ = fmt.Fprintln(w, "deqinactor_messages_out_total", s.metrics.msgOut.Load())
	_, _ = fmt.Fprintln(w, "# TYPE deqinactor_messages_in_total counter")
	_, _ = fmt.Fprintln(w, "deqinactor_messages_in_total", s.metrics.msgIn.Load())
	_, _ = fmt.Fprintln(w, "# TYPE deqinactor_mailbox_backlog gauge")
	_, _ = fmt.Fprintln(w, "deqinactor_mailbox_backlog", backlog)
	_, _ = fmt.Fprintln(w, "# TYPE deqinactor_restarts_total counter")
	_, _ = fmt.Fprintln(w, "deqinactor_restarts_total", s.metrics.restarts.Load())

	_, _ = fmt.Fprintln(w, "# TYPE deqinactor_latency_seconds histogram")
	var cum uint64
	for i, b := range s.metrics.latBuckets {
		cum += s.metrics.latCounts[i].Load()
		_, _ = fmt.Fprintln(w, "deqinactor_latency_seconds_bucket{le=\""+strconv.FormatFloat(b.Seconds(), 'f', -1, 64)+"\"}", cum)
	}
	cum += s.metrics.latCounts[len(s.metrics.latBuckets)].Load()
	_, _ = fmt.Fprintln(w, "deqinactor_latency_seconds_bucket{le=\"+Inf\"}", cum)
	_, _ = fmt.Fprintln(w, "deqinactor_latency_seconds_sum", float64(s.metrics.latSumNS.Load())/1e9)
	_, _ = fmt.Fprintln(w, "deqinactor_latency_seconds_count", cum)

	_, _ = fmt.Fprintln(w, "# TYPE deqinactor_uptime_seconds gauge")
	started := s.metrics.startedAtUnix.Load()
	if started == 0 {
		started = now.Unix()
	}
	_, _ = fmt.Fprintln(w, "deqinactor_uptime_seconds", now.Sub(time.Unix(started, 0)).Seconds())
}
