package client

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type idleTimeoutBody struct {
	body     io.ReadCloser
	timeout  time.Duration
	timer    *time.Timer
	timedOut atomic.Bool
	mu       sync.Mutex
}

type idleTimeoutError struct{}

func (idleTimeoutError) Error() string   { return "stream read timeout" }
func (idleTimeoutError) Timeout() bool   { return true }
func (idleTimeoutError) Temporary() bool { return true }

func newIdleTimeoutBody(body io.ReadCloser, timeout time.Duration) io.ReadCloser {
	wrapped := &idleTimeoutBody{body: body, timeout: timeout}
	wrapped.timer = time.AfterFunc(timeout, func() {
		wrapped.timedOut.Store(true)
		_ = body.Close()
	})
	return wrapped
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err != nil {
		if b.timedOut.Load() {
			return n, idleTimeoutError{}
		}
		return n, err
	}
	b.reset()
	return n, nil
}

func (b *idleTimeoutBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
	}
	return b.body.Close()
}

func (b *idleTimeoutBody) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Reset(b.timeout)
	}
}
