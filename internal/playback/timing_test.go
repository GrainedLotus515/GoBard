package playback

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

type stalledStream struct{ calls int }

func (s *stalledStream) Next(ctx context.Context, packet []byte) (int, error) {
	s.calls++
	if s.calls > 3 {
		return 0, io.EOF
	}
	if s.calls == 2 {
		if err := waitForTimingTest(ctx); err != nil {
			return 0, err
		}
	}
	packet[0] = 1
	return 1, nil
}
func (*stalledStream) Close() error { return nil }

func waitForTimingTest(ctx context.Context) error {
	timer := time.NewTimer(80 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestTimingDiagnosesReadAndSendStalls(t *testing.T) {
	engine, err := New(openFunc(func(context.Context, Request) (Stream, error) { return &stalledStream{}, nil }), log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	sends := 0
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := engine.Run(ctx, Request{}, sendFunc(func(ctx context.Context, _ []byte) error {
		sends++
		if sends == 3 {
			return waitForTimingTest(ctx)
		}
		return nil
	}))
	if result.Outcome != Completed || result.Frames != 3 {
		t.Fatal(result)
	}
	if result.Timing.FirstFrame <= 0 || result.Timing.MaxRead < 80*time.Millisecond || result.Timing.MaxSend < 80*time.Millisecond || result.Timing.SlowReads < 1 || result.Timing.SlowSends < 1 {
		t.Fatalf("stalls were not observable: %+v", result.Timing)
	}
}

func TestTimingThresholdAndBoundedMaxima(t *testing.T) {
	var timing Timing
	timing.record(60*time.Millisecond, 60*time.Millisecond)
	if timing.SlowReads != 0 || timing.SlowSends != 0 {
		t.Fatal("threshold is strictly greater than 60 ms")
	}
	for range 1000 {
		timing.record(80*time.Millisecond, 90*time.Millisecond)
	}
	if timing.MaxRead != 80*time.Millisecond || timing.MaxSend != 90*time.Millisecond || timing.SlowReads != 1000 || timing.SlowSends != 1000 {
		t.Fatal(timing)
	}
}
