package session

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

type boundedSource struct {
	opened atomic.Int64
	closed atomic.Int64
}

func (s *boundedSource) Open(context.Context, playback.Request) (playback.Stream, error) {
	s.opened.Add(1)
	return &boundedStream{source: s}, nil
}

type boundedStream struct {
	source *boundedSource
	sent   bool
}

func (s *boundedStream) Next(_ context.Context, dst []byte) (int, error) {
	if s.sent {
		return 0, io.EOF
	}
	s.sent = true
	dst[0] = 1
	return 1, nil
}
func (s *boundedStream) Close() error { s.source.closed.Add(1); return nil }

type outputFunc func(context.Context, []byte) error

func (f outputFunc) Send(ctx context.Context, b []byte) error { return f(ctx, b) }

func TestSkipCompletionRaceAndJoinedShutdown(t *testing.T) {
	for range 100 {
		source := &boundedSource{}
		engine, err := playback.New(source, log.New(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		entered := make(chan struct{})
		release := make(chan struct{})
		output := outputFunc(func(ctx context.Context, _ []byte) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		s, err := New(context.Background(), "race", engine, output, 1)
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := s.Enqueue(context.Background(), playback.Request{})
		if err != nil {
			t.Fatal(err)
		}
		<-entered
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); close(release) }()
		go func() {
			defer wg.Done()
			if err := s.Control(context.Background(), playback.Skipped); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		r, err := ticket.Wait(ctx)
		cancel()
		if err != nil || (r.Outcome != playback.Completed && r.Outcome != playback.Skipped) {
			t.Fatalf("%+v %v", r, err)
		}
		s.Close()
		if source.opened.Load() != 1 || source.closed.Load() != 1 {
			t.Fatal("attempt leaked or repeated")
		}
		again, err := ticket.Wait(context.Background())
		if err != nil || again.Outcome != r.Outcome {
			t.Fatal("terminal result changed")
		}
	}
}

func TestCanceledAdmissionAndWaitDoNotRollback(t *testing.T) {
	source := &boundedSource{}
	engine, err := playback.New(source, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	s, err := New(context.Background(), "cancel", engine, outputFunc(func(ctx context.Context, _ []byte) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}), 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Enqueue(ctx, playback.Request{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ticket, err := s.Enqueue(context.Background(), playback.Request{})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := ticket.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s.Close()
	r, err := ticket.Wait(context.Background())
	if err != nil || r.Outcome != playback.Shutdown || source.closed.Load() != 1 {
		t.Fatal(r, err)
	}
}

func TestIndependentGuilds(t *testing.T) {
	source := &boundedSource{}
	engine, err := playback.New(source, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := New(context.Background(), "guild", engine, outputFunc(func(context.Context, []byte) error { return nil }), 1)
			if err != nil {
				t.Error(err)
				return
			}
			defer s.Close()
			ticket, err := s.Enqueue(context.Background(), playback.Request{})
			if err != nil {
				t.Error(err)
				return
			}
			result, err := ticket.Wait(context.Background())
			if err != nil || result.Outcome != playback.Completed || result.Frames != 1 {
				t.Error(result, err)
			}
		}()
	}
	wg.Wait()
	if source.opened.Load() != 16 || source.closed.Load() != 16 {
		t.Fatal("guild resources not joined")
	}
}
