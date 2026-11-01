package session

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

func TestActiveAdmissionSeparateFromRetainedSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	source := &controlledSource{opened: make(chan attemptFixture, 8)}
	engine, err := playback.New(source, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(ctx, engine, 3, 1, 8, Defaults{Volume: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	output := outputFunc(func(context.Context, []byte) error { return nil })
	a, err := m.Create("a", output)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Create("b", output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create("idle", output); err != nil {
		t.Fatal("idle retained session rejected", err)
	}
	tickets, err := a.Add(ctx, tracks("first"))
	if err != nil {
		t.Fatal(err)
	}
	nextAttempt(t, source)
	awaitDelivered(t, a)
	apply(t, a, Operation{Action: Pause})
	if _, err := b.Add(ctx, tracks("rejected")); !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("overload=%v", err)
	}
	if snap := apply(t, b, Operation{Action: Inspect}); len(snap.Tracks) != 0 {
		t.Fatal("partial queue admission")
	}
	apply(t, a, Operation{Action: Disconnect})
	for apply(t, a, Operation{Action: Inspect}).Active {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, err := b.Add(ctx, tracks("second")); err != nil {
		t.Fatal("disconnect did not release", err)
	}
	nextAttempt(t, source)
	if _, err := a.Apply(ctx, Operation{Action: Connect, Output: output}); !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("retained queue bypassed admission: %v", err)
	}
	if err := m.Remove("b"); err != nil {
		t.Fatal(err)
	}
	apply(t, a, Operation{Action: Connect, Output: output})
	nextAttempt(t, source)
	apply(t, a, Operation{Action: Stop})
	if result, err := tickets[0].Wait(ctx); err != nil || result.Outcome != playback.Stopped {
		t.Fatal(result, err)
	}
	m.Close()
	if len(m.streamSlots) != 0 {
		t.Fatal("shutdown leaked lease")
	}
}

func TestStreamSlotHeldUntilCanceledAttemptJoins(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	engine, err := playback.New(&controlledSource{opened: make(chan attemptFixture, 2)}, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(context.Background(), engine, 2, 1, 4, Defaults{Volume: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.Create("a", outputFunc(func(ctx context.Context, _ []byte) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		<-finish
		return ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Create("b", outputFunc(func(context.Context, []byte) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add(context.Background(), tracks("first")); err != nil {
		t.Fatal(err)
	}
	<-entered
	apply(t, a, Operation{Action: Stop})
	_, err = b.Add(context.Background(), tracks("blocked"))
	close(finish)
	if !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("released before join: %v", err)
	}
	a.Close()
	if _, err := b.Add(context.Background(), tracks("admitted")); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStreamAdmissionIsAtomic(t *testing.T) {
	source := &controlledSource{opened: make(chan attemptFixture, 8)}
	engine, err := playback.New(source, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(t.Context(), engine, 8, 1, 4, Defaults{Volume: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var workers sync.WaitGroup
	results := make(chan error, 8)
	for i := range 8 {
		s, err := m.Create(strconv.Itoa(i), outputFunc(func(context.Context, []byte) error { return nil }))
		if err != nil {
			t.Fatal(err)
		}
		workers.Add(1)
		go func() { defer workers.Done(); _, err := s.Add(t.Context(), tracks("race")); results <- err }()
	}
	workers.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrStreamLimit) {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatalf("admitted %d streams into one slot", accepted)
	}
	m.Close()
	if len(m.streamSlots) != 0 {
		t.Fatal("leaked admission")
	}
}
