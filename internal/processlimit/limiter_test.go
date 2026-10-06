package processlimit

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

func TestLimiterReservesInteractiveCapacity(t *testing.T) {
	for _, capacity := range []int{1, 4, 8, 12} {
		t.Run(fmt.Sprintf("capacity-%d", capacity), func(t *testing.T) {
			limiter := New(capacity)
			wantBulk := capacity
			if capacity > 1 {
				wantBulk--
			}
			if got := limiter.NonInteractiveCapacity(); got != wantBulk {
				t.Fatalf("NonInteractiveCapacity() = %d, want %d", got, wantBulk)
			}

			releases := make([]func(), 0, wantBulk)
			for range wantBulk {
				release, err := limiter.AcquireClass(context.Background(), Bulk)
				if err != nil {
					t.Fatalf("AcquireClass(Bulk) error = %v", err)
				}
				releases = append(releases, release)
			}
			defer func() {
				for _, release := range releases {
					release()
				}
			}()

			if capacity > 1 {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				release, err := limiter.AcquireClass(ctx, Interactive)
				if err != nil {
					t.Fatalf("reserved interactive AcquireClass() error = %v", err)
				}
				release()
			}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if _, err := limiter.AcquireClass(ctx, Bulk); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("extra bulk AcquireClass() error = %v, want deadline exceeded", err)
			}
		})
	}
}

func TestLimiterCapsBackgroundAtOneAndRollsBackCancellation(t *testing.T) {
	limiter := New(4)
	release, err := limiter.AcquireClass(context.Background(), Background)
	if err != nil {
		t.Fatalf("first background acquire error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := limiter.AcquireClass(ctx, Background); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second background acquire error = %v, want deadline exceeded", err)
	}
	release()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		next, acquireErr := limiter.AcquireClass(context.Background(), Background)
		if acquireErr != nil {
			t.Errorf("background acquire after cancellation error = %v", acquireErr)
			return
		}
		next()
	}()
	wg.Wait()
}

func TestLimiterCancellationRollsBackEveryGate(t *testing.T) {
	t.Run("background gate", func(t *testing.T) {
		limiter := New(4)
		held, err := limiter.AcquireClass(context.Background(), Background)
		if err != nil {
			t.Fatal(err)
		}
		defer held()
		assertAcquireDeadline(t, limiter, Background)
	})

	t.Run("non-interactive gate", func(t *testing.T) {
		limiter := New(2)
		held, err := limiter.AcquireClass(context.Background(), Bulk)
		if err != nil {
			t.Fatal(err)
		}
		assertAcquireDeadline(t, limiter, Background)
		held()
		next, err := limiter.AcquireClass(context.Background(), Background)
		if err != nil {
			t.Fatalf("background gate leaked after cancellation: %v", err)
		}
		next()
	})

	t.Run("total gate", func(t *testing.T) {
		limiter := New(1)
		held, err := limiter.AcquireClass(context.Background(), Interactive)
		if err != nil {
			t.Fatal(err)
		}
		assertAcquireDeadline(t, limiter, Background)
		held()
		next, err := limiter.AcquireClass(context.Background(), Background)
		if err != nil {
			t.Fatalf("earlier gates leaked after total-gate cancellation: %v", err)
		}
		next()
	})
}

func assertAcquireDeadline(t *testing.T, limiter *Limiter, class WorkClass) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := limiter.AcquireClass(ctx, class); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireClass(%s) error = %v, want deadline exceeded", class, err)
	}
}

func BenchmarkLimiterAcquireInteractive(b *testing.B) {
	limiter := New(12)
	b.ReportAllocs()
	for b.Loop() {
		release, err := limiter.AcquireClass(context.Background(), Interactive)
		if err != nil {
			b.Fatal(err)
		}
		release()
	}
}
