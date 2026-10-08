package session

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

type attemptFixture struct {
	request playback.Request
	finish  chan struct{}
}
type controlledSource struct{ opened chan attemptFixture }

func (s *controlledSource) Open(ctx context.Context, request playback.Request) (playback.Stream, error) {
	f := attemptFixture{request: request, finish: make(chan struct{})}
	select {
	case s.opened <- f:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &controlledStream{finish: f.finish}, nil
}

type controlledStream struct {
	finish chan struct{}
	sent   bool
}

func (s *controlledStream) Next(ctx context.Context, b []byte) (int, error) {
	if !s.sent {
		s.sent = true
		b[0] = 1
		return 1, nil
	}
	select {
	case <-s.finish:
		return 0, io.EOF
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
func (*controlledStream) Close() error { return nil }

func newControlsTest(t *testing.T) (*Session, *controlledSource) {
	t.Helper()
	source := &controlledSource{opened: make(chan attemptFixture, 32)}
	engine, err := playback.New(source, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), "controls", engine, outputFunc(func(context.Context, []byte) error { return nil }), 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, source
}
func nextAttempt(t *testing.T, s *controlledSource) attemptFixture {
	t.Helper()
	select {
	case a := <-s.opened:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("attempt did not start")
		return attemptFixture{}
	}
}
func apply(t *testing.T, s *Session, op Operation) Snapshot {
	t.Helper()
	snap, err := s.Apply(context.Background(), op)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}
func tracks(ids ...string) []Track {
	result := make([]Track, len(ids))
	for i, id := range ids {
		result[i] = Track{MediaID: id, Title: id, Duration: time.Minute}
	}
	return result
}

func TestLoopSkipAndRemovalSelectExactlyOnce(t *testing.T) {
	s, source := newControlsTest(t)
	tickets, err := s.Add(context.Background(), tracks("a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	a := nextAttempt(t, source)
	apply(t, s, Operation{Action: Loop})
	close(a.finish)
	repeat := nextAttempt(t, source)
	if repeat.request.MediaID != "a" || repeat.request.ID != a.request.ID || repeat.request.AttemptID == a.request.AttemptID {
		t.Fatal("loop did not create a distinct attempt for the same request")
	}
	apply(t, s, Operation{Action: Skip})
	b := nextAttempt(t, source)
	if b.request.MediaID != "b" {
		t.Fatal(b.request)
	}
	apply(t, s, Operation{Action: Remove, Position: 1})
	c := nextAttempt(t, source)
	if c.request.MediaID != "c" {
		t.Fatal(c.request)
	}
	snap := apply(t, s, Operation{Action: Inspect})
	if snap.Current != 1 || len(snap.Tracks) != 2 {
		t.Fatal(snap)
	}
	apply(t, s, Operation{Action: Stop})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i, ticket := range tickets {
		r, err := ticket.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := []playback.Outcome{playback.Skipped, playback.Removed, playback.Stopped}[i]
		if r.Outcome != want {
			t.Fatalf("ticket %d: %+v", i, r)
		}
	}
}

func TestQueueMoveClearAndSnapshots(t *testing.T) {
	s, source := newControlsTest(t)
	_, err := s.Add(context.Background(), tracks("a", "b", "c", "d"))
	if err != nil {
		t.Fatal(err)
	}
	_ = nextAttempt(t, source)
	snap := apply(t, s, Operation{Action: Move, Position: 3, To: 1})
	if snap.Tracks[1].MediaID != "d" {
		t.Fatal(snap)
	}
	snap.Tracks[0].Title = "changed"
	if snap = apply(t, s, Operation{Action: Inspect}); snap.Tracks[0].Title != "a" {
		t.Fatal("snapshot shared mutable queue storage")
	}
	if _, err := s.Apply(context.Background(), Operation{Action: Move, Position: 0, To: 2}); err == nil {
		t.Fatal("moved current")
	}
	snap = apply(t, s, Operation{Action: Clear})
	if len(snap.Tracks) != 1 || snap.Current != 0 || snap.Tracks[0].MediaID != "a" {
		t.Fatal(snap)
	}
}

func TestSeekDisconnectReconnectAndSettings(t *testing.T) {
	s, source := newControlsTest(t)
	_, err := s.Add(context.Background(), tracks("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	first := nextAttempt(t, source)
	apply(t, s, Operation{Action: Volume, Position: 80})
	apply(t, s, Operation{Action: Ducking, Enabled: true})
	apply(t, s, Operation{Action: DuckTarget, Position: 30})
	apply(t, s, Operation{Action: Speaking, Speaker: "1", Enabled: true})
	if first.request.Controls.Volume() != 30 {
		t.Fatal("ducking did not affect codec gain")
	}
	apply(t, s, Operation{Action: Speaking, Speaker: "1"})
	if first.request.Controls.Volume() != 80 {
		t.Fatal("volume was not restored")
	}
	apply(t, s, Operation{Action: Seek, Offset: 5 * time.Second})
	restarted := nextAttempt(t, source)
	if restarted.request.Offset != 5*time.Second || restarted.request.ID != first.request.ID {
		t.Fatal(restarted.request)
	}
	apply(t, s, Operation{Action: Disconnect})
	// A barrier query observes asynchronous cleanup; no second attempt may
	// begin while disconnected. Use a deadline, not a scheduling sleep.
	deadline := time.After(2 * time.Second)
	for {
		snap, err := s.Apply(context.Background(), Operation{Action: Inspect})
		if err != nil {
			t.Fatal(err)
		}
		if !snap.Active {
			break
		}
		select {
		case <-deadline:
			t.Fatal("disconnect did not join attempt")
		default:
		}
	}
	apply(t, s, Operation{Action: Connect, Output: outputFunc(func(context.Context, []byte) error { return nil })})
	reconnected := nextAttempt(t, source)
	if reconnected.request.MediaID != "a" {
		t.Fatal("disconnect advanced queue")
	}
	if _, err := s.Apply(context.Background(), Operation{Action: Seek, Offset: time.Hour}); err == nil {
		t.Fatal("out of range seek accepted")
	}
}

func TestAtomicPlaylistAdmissionAndValidation(t *testing.T) {
	s, _ := newControlsTest(t)
	if _, err := s.Add(context.Background(), tracks("1", "2", "3", "4", "5", "6", "7", "8", "9")); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	if snapshot := apply(t, s, Operation{Action: Inspect}); len(snapshot.Tracks) != 0 {
		t.Fatal("partial admission")
	}
	for _, action := range []Action{Volume, DuckTarget} {
		if _, err := s.Apply(context.Background(), Operation{Action: action, Position: 101}); err == nil {
			t.Fatal("invalid gain accepted")
		}
	}
}
