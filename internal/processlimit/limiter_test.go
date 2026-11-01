package processlimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterHonorsCapacityAndContext(t *testing.T) {
	limiter := New(1)
	release, err := limiter.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := limiter.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked Acquire() error = %v, want deadline exceeded", err)
	}

	release()
	release() // Idempotent release must not drain a later reservation.
	if nextRelease, err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() after release error = %v", err)
	} else {
		nextRelease()
	}
}

func TestInvalidLimiterCannotSilentlyAllowProcesses(t *testing.T) {
	for _, n := range []int{-1, 0, 17} {
		limiter := New(n)
		if limiter != nil {
			t.Fatalf("invalid budget %d accepted", n)
		}
		if _, err := limiter.Acquire(context.Background()); err == nil {
			t.Fatal("nil limiter bypassed process budget")
		}
	}
	if _, err := New(1).Acquire(nil); err == nil { //nolint:staticcheck // Deliberately exercise invalid lifecycle input.
		t.Fatal("nil lifecycle context accepted")
	}
}

func BenchmarkLimiterAcquire(b *testing.B) {
	limiter := New(12)
	b.ReportAllocs()
	for b.Loop() {
		release, err := limiter.Acquire(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		release()
	}
}
