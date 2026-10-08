// Package processlimit coordinates bounded external process use across the
// media pipeline. It is intentionally independent of player and youtube so
// every yt-dlp launch shares the same process budget.
package processlimit

import (
	"context"
	"fmt"
	"sync/atomic"
)

// Limiter bounds concurrent external processes. A release returned from
// Acquire is idempotent, making cleanup paths safe under cancellation races.
type Limiter struct {
	total          chan struct{}
	nonInteractive chan struct{}
	background     chan struct{}
}

// WorkClass describes how a yt-dlp process contributes to user-visible
// latency. Interactive work always retains one reserved slot when the process
// budget is greater than one. Bulk and background work share the remainder;
// cache downloads receive an additional process-wide cap of one.
type WorkClass string

const (
	Interactive WorkClass = "interactive"
	Bulk        WorkClass = "bulk"
	Background  WorkClass = "background"
)

type reservation struct {
	gates    [3]chan struct{}
	count    int
	released atomic.Bool
}

func (r *reservation) add(gate chan struct{}) {
	r.gates[r.count] = gate
	r.count++
}

func (r *reservation) release() {
	if r == nil || r.released.Swap(true) {
		return
	}
	for i := r.count - 1; i >= 0; i-- {
		<-r.gates[i]
	}
}

// New returns a limiter with capacity 1–16, or nil for an invalid budget.
func New(maxConcurrency int) *Limiter {
	if maxConcurrency < 1 || maxConcurrency > 16 {
		return nil
	}
	nonInteractiveCapacity := maxConcurrency
	if maxConcurrency > 1 {
		nonInteractiveCapacity--
	}
	return &Limiter{
		total:          make(chan struct{}, maxConcurrency),
		nonInteractive: make(chan struct{}, nonInteractiveCapacity),
		background:     make(chan struct{}, 1),
	}
}

// Acquire reserves one process slot until the returned release function is
// called or ctx expires.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	return l.AcquireClass(ctx, Interactive)
}

// AcquireClass reserves capacity for a classified process. Non-interactive
// gates are acquired before the total gate so a waiter can never consume the
// interactive reservation while blocked behind another bulk operation.
func (l *Limiter) AcquireClass(ctx context.Context, class WorkClass) (func(), error) {
	if l == nil || l.total == nil {
		return nil, fmt.Errorf("process limiter unavailable")
	}
	if ctx == nil {
		return nil, fmt.Errorf("process context required")
	}

	acquired := &reservation{}
	acquire := func(slots chan struct{}) error {
		select {
		case slots <- struct{}{}:
			acquired.add(slots)
			return nil
		case <-ctx.Done():
			acquired.release()
			return ctx.Err()
		}
	}

	switch class {
	case Background:
		if err := acquire(l.background); err != nil {
			return nil, err
		}
		if err := acquire(l.nonInteractive); err != nil {
			return nil, err
		}
	case Bulk:
		if err := acquire(l.nonInteractive); err != nil {
			return nil, err
		}
	case Interactive:
	default:
		return nil, fmt.Errorf("unknown process work class %q", class)
	}
	if err := acquire(l.total); err != nil {
		return nil, err
	}

	return acquired.release, nil
}

// Capacity reports the configured process budget.
func (l *Limiter) Capacity() int {
	if l == nil || l.total == nil {
		return 0
	}
	return cap(l.total)
}

// NonInteractiveCapacity reports the combined bulk/background budget.
func (l *Limiter) NonInteractiveCapacity() int {
	if l == nil || l.nonInteractive == nil {
		return 0
	}
	return cap(l.nonInteractive)
}
