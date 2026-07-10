// Package processlimit coordinates bounded external process use across the
// media pipeline. It is intentionally independent of player and youtube so
// every yt-dlp launch shares the same process budget.
package processlimit

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

const defaultYTDLPConcurrency = 4

// Limiter bounds concurrent external processes. A release returned from
// Acquire is idempotent, making cleanup paths safe under cancellation races.
type Limiter struct {
	slots chan struct{}
}

// New returns a limiter with a positive bounded capacity.
func New(maxConcurrency int) *Limiter {
	if maxConcurrency < 1 || maxConcurrency > 16 {
		maxConcurrency = defaultYTDLPConcurrency
	}
	return &Limiter{slots: make(chan struct{}, maxConcurrency)}
}

// Acquire reserves one process slot until the returned release function is
// called or ctx expires.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	if l == nil || l.slots == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	var once sync.Once
	return func() {
		once.Do(func() { <-l.slots })
	}, nil
}

// Capacity reports the configured process budget.
func (l *Limiter) Capacity() int {
	if l == nil || l.slots == nil {
		return 0
	}
	return cap(l.slots)
}

var global atomic.Pointer[Limiter]

func init() {
	global.Store(New(defaultYTDLPConcurrency))
}

// ConfigureGlobal replaces the limiter used by all process launchers. It is
// intended for one-time process startup configuration; in-flight operations
// retain their original limiter and release it safely.
func ConfigureGlobal(maxConcurrency int) {
	global.Store(New(maxConcurrency))
}

// Global returns the configured shared limiter.
func Global() *Limiter {
	limiter := global.Load()
	if limiter == nil {
		return New(defaultYTDLPConcurrency)
	}
	return limiter
}

// AcquireGlobal reserves a slot from the configured shared limiter.
func AcquireGlobal(ctx context.Context) (func(), error) {
	release, err := Global().Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire yt-dlp process slot: %w", err)
	}
	return release, nil
}
