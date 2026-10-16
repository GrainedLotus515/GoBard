package session

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

type voiceFixture struct {
	connect    func(context.Context, string) (playback.Output, error)
	disconnect func(context.Context) error
}

func (v voiceFixture) Connect(ctx context.Context, channel string) (playback.Output, error) {
	return v.connect(ctx, channel)
}
func (v voiceFixture) Disconnect(ctx context.Context) error {
	if v.disconnect != nil {
		return v.disconnect(ctx)
	}
	return nil
}

func voiceTestSession(t *testing.T, voice Voice) (*Session, *controlledSource) {
	t.Helper()
	source := &controlledSource{opened: make(chan attemptFixture, 32)}
	logger := log.New(io.Discard)
	engine, err := playback.New(source, logger)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewVoice(context.Background(), "voice-test", engine, voice, logger, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, source
}

func TestStopCancelsJoinAndLateSuccessCannotConnect(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{}, 4)
	s, _ := voiceTestSession(t, voiceFixture{connect: func(ctx context.Context, _ string) (playback.Output, error) {
		close(started)
		<-ctx.Done()
		// An adapter can finish a successful handshake concurrently with cancel.
		return outputFunc(func(context.Context, []byte) error { return nil }), nil
	}, disconnect: func(context.Context) error { closed <- struct{}{}; return nil }})
	done := make(chan error, 1)
	go func() { _, err := s.Join(context.Background(), "voice"); done <- err }()
	<-started
	if snap := apply(t, s, Operation{Action: Inspect}); !snap.Busy {
		t.Fatal("joining session reported idle")
	}
	if _, err := s.ApplyAs(context.Background(), Access{Channel: "voice"}, Operation{Action: Stop}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrSuperseded) {
		t.Fatalf("join = %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("late connection not closed")
	}
	if snap := apply(t, s, Operation{Action: Inspect}); snap.Connected || snap.Connecting {
		t.Fatalf("revived connection: %+v", snap)
	}
}

func TestObservedVoiceLossRecoversPausedAttempt(t *testing.T) {
	joined := make(chan string, 4)
	s, source := voiceTestSession(t, voiceFixture{connect: func(_ context.Context, channel string) (playback.Output, error) {
		joined <- channel
		return outputFunc(func(context.Context, []byte) error { return nil }), nil
	}})
	if _, err := s.Join(context.Background(), "original"); err != nil {
		t.Fatal(err)
	}
	<-joined
	if _, err := s.Add(context.Background(), []Track{{Provider: "fixture", MediaID: "audio"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.opened:
	case <-time.After(time.Second):
		t.Fatal("attempt not started")
	}
	apply(t, s, Operation{Action: Pause})
	if _, err := s.ApplyAs(context.Background(), Access{Channel: "original"}, Operation{Action: VoiceLost}); err == nil {
		t.Fatal("interaction invoked lifecycle action")
	}
	apply(t, s, Operation{Action: VoiceLost, Channel: "moved"})
	select {
	case channel := <-joined:
		if channel != "moved" {
			t.Fatal(channel)
		}
	case <-time.After(time.Second):
		t.Fatal("paused transport did not recover")
	}
}

func TestSkipLastTrackCancelsRecoveryAndDisconnects(t *testing.T) {
	var connects atomic.Int32
	recovering, canceled := make(chan struct{}), make(chan struct{})
	s, source := voiceTestSession(t, voiceFixture{connect: func(ctx context.Context, _ string) (playback.Output, error) {
		if connects.Add(1) > 1 {
			close(recovering)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		}
		return outputFunc(func(context.Context, []byte) error { return nil }), nil
	}})
	if _, err := s.Join(context.Background(), "voice"); err != nil {
		t.Fatal(err)
	}
	tickets, err := s.Add(context.Background(), []Track{{MediaID: "audio"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.opened:
	case <-time.After(time.Second):
		t.Fatal("attempt not started")
	}
	apply(t, s, Operation{Action: VoiceLost})
	select {
	case <-recovering:
	case <-time.After(time.Second):
		t.Fatal("recovery not started")
	}
	if _, err := s.ApplyAs(context.Background(), Access{Channel: "voice"}, Operation{Action: Skip}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("empty queue kept reconnect alive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := tickets[0].Wait(ctx)
	if err != nil || result.Outcome != playback.Skipped {
		t.Fatalf("result %+v %v", result, err)
	}
	s.Close()
	if connects.Load() != 2 {
		t.Fatalf("unexpected retry after empty queue: %d", connects.Load())
	}
}

func TestShutdownJoinsConnectingTransport(t *testing.T) {
	started, joined := make(chan struct{}), make(chan struct{})
	s, _ := voiceTestSession(t, voiceFixture{connect: func(ctx context.Context, _ string) (playback.Output, error) {
		close(started)
		<-ctx.Done()
		close(joined)
		return nil, ctx.Err()
	}})
	done := make(chan error, 1)
	go func() { _, err := s.Join(context.Background(), "voice"); done <- err }()
	<-started
	s.Close()
	s.Close()
	select {
	case <-joined:
	default:
		t.Fatal("join worker outlived session")
	}
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestRecoveryBudgetRetainsRequestAndClosesTransport(t *testing.T) {
	var connects atomic.Int32
	closed := make(chan struct{}, 8)
	s, _ := voiceTestSession(t, voiceFixture{connect: func(context.Context, string) (playback.Output, error) {
		connects.Add(1)
		return outputFunc(func(context.Context, []byte) error { return errors.New("broken transport") }), nil
	}, disconnect: func(context.Context) error { closed <- struct{}{}; return nil }})
	if _, err := s.Join(context.Background(), "voice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(context.Background(), tracks("retained")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(6 * time.Second):
		t.Fatal("recovery budget did not terminate")
	}
	if got := connects.Load(); got != 4 {
		t.Fatalf("connects=%d, want initial + three recovery attempts", got)
	}
	snap := apply(t, s, Operation{Action: Inspect})
	if snap.Connected || len(snap.Tracks) != 1 || snap.Last.Outcome != playback.TransportFailure {
		t.Fatalf("recovery corrupted queue: %+v", snap)
	}
}

func TestPendingResolutionPreventsQueueDrainDisconnect(t *testing.T) {
	closed := make(chan struct{}, 4)
	s, source := voiceTestSession(t, voiceFixture{connect: func(context.Context, string) (playback.Output, error) {
		return outputFunc(func(context.Context, []byte) error { return nil }), nil
	}, disconnect: func(context.Context) error { closed <- struct{}{}; return nil }})
	if _, err := s.Join(context.Background(), "voice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginResolution(context.Background(), Access{Channel: "voice"}, "pending"); err != nil {
		t.Fatal(err)
	}
	tickets, err := s.Add(context.Background(), tracks("one"))
	if err != nil {
		t.Fatal(err)
	}
	first := nextAttempt(t, source)
	close(first.finish)
	if _, err := tickets[0].Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snap := apply(t, s, Operation{Action: Inspect}); !snap.Connected || !snap.Busy {
		t.Fatal("pending lookup lost voice")
	}
	select {
	case <-closed:
		t.Fatal("disconnected during pending lookup")
	default:
	}
	s.EndResolution("pending")
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("drained connection not closed")
	}
}
