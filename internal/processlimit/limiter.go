// Package processlimit coordinates bounded external process use across the
// media pipeline. It is independent of playback and source resolution so every
// yt-dlp launch shares the same process budget.
package processlimit

import (
	"context"
	"fmt"
	"sync/atomic"
)

// Limiter bounds concurrent external processes. A release returned from
// Acquire is idempotent, making cleanup paths safe under cancellation races.
type Limiter struct {
	total chan struct{}
}

type reservation struct {
	total    chan struct{}
	released atomic.Bool
}

// New returns a limiter with capacity 1–16, or nil for an invalid budget.
func New(maxConcurrency int) *Limiter {
	if maxConcurrency < 1 || maxConcurrency > 16 {
		return nil
	}
	return &Limiter{total: make(chan struct{}, maxConcurrency)}
}

// Acquire reserves one process slot until the returned release function is
// called. Cancellation before admission returns the context error without
// consuming capacity.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	if l == nil || l.total == nil {
		return nil, fmt.Errorf("process limiter unavailable")
	}
	if ctx == nil {
		return nil, fmt.Errorf("process context required")
	}

	select {
	case l.total <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	reservation := &reservation{total: l.total}
	return reservation.release, nil
}

func (r *reservation) release() {
	if r.released.Swap(true) {
		return
	}
	<-r.total
}

// Capacity reports the configured process budget.
func (l *Limiter) Capacity() int {
	if l == nil || l.total == nil {
		return 0
	}
	return cap(l.total)
}
