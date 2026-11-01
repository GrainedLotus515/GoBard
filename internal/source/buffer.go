package source

import (
	"context"
	"io"
	"sync"
	"time"
)

const readAheadBytes = 8 << 20
const preparationBytes = 256 << 10
const acquisitionBlockBytes = 32 << 10

// byteBuffer has one producer and one consumer. Fixed compressed-byte storage
// copies caller bytes before returning. Only memory copies hold mu; external
// I/O and cancellation-aware saturation waits happen outside it.
type byteBuffer struct {
	mu                 sync.Mutex
	data               []byte
	head, size         int
	first              time.Time
	ended              bool
	err                error
	readable, writable chan struct{}
}

func newByteBuffer(capacity int) *byteBuffer {
	return &byteBuffer{data: make([]byte, capacity), readable: make(chan struct{}, 1), writable: make(chan struct{}, 1)}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (b *byteBuffer) write(ctx context.Context, p []byte) error {
	for len(p) > 0 {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		b.mu.Lock()
		n := min(len(p), len(b.data)-b.size)
		if n > 0 {
			tail := (b.head + b.size) % len(b.data)
			first := copy(b.data[tail:], p[:n])
			copy(b.data, p[first:n])
			b.size += n
			if b.first.IsZero() {
				b.first = time.Now()
			}
		}
		b.mu.Unlock()
		if n > 0 {
			p = p[n:]
			signal(b.readable)
			continue
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-b.writable:
		}
	}
	return nil
}

func (b *byteBuffer) read(ctx context.Context, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, context.Cause(ctx)
		}
		b.mu.Lock()
		n := min(len(p), b.size)
		if n > 0 {
			first := copy(p[:n], b.data[b.head:])
			copy(p[first:n], b.data[:n-first])
			b.head = (b.head + n) % len(b.data)
			b.size -= n
		}
		ended, err := b.ended, b.err
		b.mu.Unlock()
		if n > 0 {
			signal(b.writable)
			return n, nil
		}
		if ended {
			if err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		select {
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		case <-b.readable:
		}
	}
}

func (b *byteBuffer) finish(err error) {
	b.mu.Lock()
	b.ended, b.err = true, err
	b.mu.Unlock()
	signal(b.readable)
}

// prepare runs before the feeder becomes the sole consumer.
func (b *byteBuffer) prepare(ctx context.Context, delay time.Duration) error {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	var deadline <-chan time.Time
	for {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		b.mu.Lock()
		size, first, ended, err := b.size, b.first, b.ended, b.err
		b.mu.Unlock()
		if ended {
			return err
		}
		if size >= min(preparationBytes, len(b.data)) {
			return nil
		}
		if timer == nil && !first.IsZero() {
			timer = time.NewTimer(max(0, time.Until(first.Add(delay))))
			deadline = timer.C
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-deadline:
			return nil
		case <-b.readable:
		}
	}
}
