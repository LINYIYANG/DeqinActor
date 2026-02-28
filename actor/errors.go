package actor

import "errors"

var (
	// ErrCircuitOpen 表示断路器处于打开状态，请求被拒绝。
	// 当目标 Actor 的失败次数超过阈值时，断路器会打开以防止级联故障。
	ErrCircuitOpen = errors.New("circuit breaker open")
	// ErrDegradedToAsync 表示同步请求被降级为异步请求。
	// 当系统负载过高（等待令牌耗尽）且 AllowDegrade 为 true 时会发生此降级。
	ErrDegradedToAsync = errors.New("sync degraded to async")
	// ErrAskTimeout 表示 Ask 请求超时，未在指定时间内收到响应。
	ErrAskTimeout = errors.New("ask timeout")
)

