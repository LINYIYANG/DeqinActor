package actor

import "time"

// SendAsync 向目标发送请求消息，返回一个 Future。
// Future 在响应到达或超时后完成。
// 默认超时为 30 秒，可通过 opts 覆盖。
func (a *BaseActor) SendAsync(target IActor, msg any, opts ...SendOptions) *Future[*Response] {
	o := SendOptions{Timeout: 30 * time.Second}
	if len(opts) > 0 {
		if opts[0].Timeout != 0 {
			o.Timeout = opts[0].Timeout
		}
		o.Priority = opts[0].Priority
		o.Persist = opts[0].Persist
		o.OnComplete = opts[0].OnComplete
		o.AllowDegrade = opts[0].AllowDegrade
	}
	f := newFuture[*Response]()
	if a.system == nil {
		f.complete(&Response{CorrelationID: "", Err: ErrActorNotFound})
		return f
	}
	cid := NewActorID()
	req := &Request{CorrelationID: cid, Payload: msg, ReplyTo: a.id}
	a.system.TrackPending(cid, f, o.Timeout)
	if o.OnComplete != nil {
		f.OnComplete(o.OnComplete)
	}
	_ = a.system.sendRequest(a, target, req, o.Priority, o.Persist)
	return f
}

// SyncAsk 发送请求并阻塞等待响应。
// 默认超时为 5 秒，可通过 opts 覆盖。
// 返回响应和可能的错误。
func (a *BaseActor) SyncAsk(target IActor, msg any, opts ...SendOptions) (*Response, error) {
	o := SendOptions{Timeout: 5 * time.Second, AllowDegrade: false}
	if len(opts) > 0 {
		if opts[0].Timeout != 0 {
			o.Timeout = opts[0].Timeout
		}
		o.Priority = opts[0].Priority
		o.Persist = opts[0].Persist
		o.OnComplete = opts[0].OnComplete
		o.AllowDegrade = opts[0].AllowDegrade
	}
	resp, _, err := a.Ask(target, msg, o)
	return resp, err
}

// Ask 是核心的请求-响应 API。
// 支持断路器检查和可选的降级到异步行为。
//
// 返回值：
//   - *Response: 响应结果（同步成功时）
//   - *Future[*Response]: Future 对象（降级为异步时）
//   - error: 错误信息
//
// 当 AllowDegrade 为 true 且系统饱和时，会降级为异步模式，
// 返回 (nil, future, ErrDegradedToAsync)。
func (a *BaseActor) Ask(target IActor, msg any, opt SendOptions) (*Response, *Future[*Response], error) {
	if a.system == nil {
		return nil, nil, ErrActorNotFound
	}
	b := a.system.breakerFor(target)
	if b != nil && !b.Allow(time.Now()) {
		return nil, nil, ErrCircuitOpen
	}
	timeout := opt.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	start := time.Now()
	if opt.AllowDegrade && !a.system.tryAcquireWaitToken() {
		f := a.SendAsync(target, msg, SendOptions{Timeout: timeout, Priority: opt.Priority, Persist: opt.Persist, OnComplete: opt.OnComplete})
		return nil, f, ErrDegradedToAsync
	}
	if !opt.AllowDegrade {
		a.system.acquireWaitToken()
	}
	defer a.system.releaseWaitToken()

	f := a.SendAsync(target, msg, SendOptions{Timeout: timeout, Priority: opt.Priority, Persist: opt.Persist, OnComplete: opt.OnComplete})
	resp, ok := f.Await(timeout)
	if a.system.metrics != nil {
		a.system.metrics.ObserveLatency(time.Since(start))
	}
	if !ok || resp == nil {
		if b != nil {
			b.OnFailure(time.Now())
		}
		return nil, f, ErrAskTimeout
	}
	if resp.Err != nil {
		if b != nil {
			b.OnFailure(time.Now())
		}
		return resp, f, resp.Err
	}
	if b != nil {
		b.OnSuccess()
	}
	return resp, f, nil
}
