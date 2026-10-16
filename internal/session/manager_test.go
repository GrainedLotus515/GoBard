package session

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

func TestBusyObservationIncludesPausedWorkAndFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	source := &controlledSource{opened: make(chan attemptFixture, 1)}
	engine, err := playback.New(source, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(ctx, engine, 1, 4, Defaults{Volume: 100, DuckTarget: 70})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if count, err := m.Busy(ctx); err != nil || count != 0 {
		t.Fatal("empty manager not idle", count, err)
	}
	s, err := m.Create("guild", outputFunc(func(context.Context, []byte) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.Enqueue(ctx, playback.Request{MediaID: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	_ = nextAttempt(t, source)
	if _, err := s.Apply(ctx, Operation{Action: Pause}); err != nil {
		t.Fatal(err)
	}
	if count, err := m.Busy(ctx); err != nil || count != 1 {
		t.Fatal("paused attempt reported idle", count, err)
	}
	if _, err := s.Apply(ctx, Operation{Action: Stop}); err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if count, err := m.Busy(ctx); err != nil || count != 0 {
		t.Fatal("stopped session retained work", count, err)
	}
	cancel()
	if _, err := m.Busy(ctx); err == nil {
		t.Fatal("canceled inspection reported idle")
	}
}

func TestManagerAdmissionDefaultsAndShutdown(t *testing.T) {
	engine, err := playback.New(&boundedSource{}, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(context.Background(), engine, 4, 8, Defaults{Volume: 65, Ducking: true, DuckTarget: 25})
	if err != nil {
		t.Fatal(err)
	}
	output := outputFunc(func(context.Context, []byte) error { return nil })
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := m.Create(strconv.Itoa(i), output)
			if errors.Is(err, ErrGuildLimit) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			admitted.Add(1)
			snap, err := s.Apply(context.Background(), Operation{Action: Inspect})
			if err != nil || snap.Volume != 65 || !snap.Ducking || snap.DuckTarget != 25 {
				t.Error(snap, err)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 4 {
		t.Fatal(admitted.Load())
	}
	m.Close()
	m.Close()
	if _, err := m.Create("late", output); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestManagerConcurrentCreateRemoveClose(t *testing.T) {
	for range 25 {
		engine, err := playback.New(&boundedSource{}, log.New(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewManager(context.Background(), engine, 2, 8, Defaults{Volume: 100, DuckTarget: 70})
		if err != nil {
			t.Fatal(err)
		}
		output := outputFunc(func(context.Context, []byte) error { return nil })
		if _, err := m.Create("existing", output); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, err := m.Create("new", output)
			if err != nil && !errors.Is(err, ErrClosed) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := m.Remove("existing"); err != nil && !errors.Is(err, ErrClosed) {
				t.Error(err)
			}
		}()
		go func() { defer wg.Done(); m.Close() }()
		wg.Wait()
		if _, ok := m.Get("existing"); ok {
			t.Fatal("closed manager retained accessible session")
		}
	}
}
