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
