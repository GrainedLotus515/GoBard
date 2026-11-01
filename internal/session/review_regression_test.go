package session

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

func TestReviewTransportRecoveryPreservesDeliveredPosition(t *testing.T) {
	s, source := voiceTestSession(t, voiceFixture{connect: func(context.Context, string) (playback.Output, error) {
		return outputFunc(func(context.Context, []byte) error { return nil }), nil
	}})
	if _, err := s.Join(context.Background(), "voice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(context.Background(), tracks("audio")); err != nil {
		t.Fatal(err)
	}
	first := nextAttempt(t, source)
	deadline := time.Now().Add(time.Second)
	for apply(t, s, Operation{Action: Inspect}).Frames == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no audio delivered")
		}
		time.Sleep(time.Millisecond)
	}
	apply(t, s, Operation{Action: VoiceLost})
	second := nextAttempt(t, source)
	if second.request.ID != first.request.ID {
		t.Fatal("recovery replaced the logical request")
	}
	if second.request.Offset < playback.FrameDuration {
		t.Fatalf("voice recovery replays delivered audio: offset=%v, want >=%v", second.request.Offset, playback.FrameDuration)
	}
}

func awaitDelivered(t *testing.T, s *Session) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		snap := apply(t, s, Operation{Action: Inspect})
		if snap.Frames > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no delivered frame")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestVoiceRecoveryAccumulatesAfterSeekAndDisconnect(t *testing.T) {
	s, source := voiceTestSession(t, voiceFixture{connect: func(context.Context, string) (playback.Output, error) {
		return outputFunc(func(context.Context, []byte) error { return nil }), nil
	}})
	if _, err := s.Join(context.Background(), "voice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(context.Background(), tracks("audio")); err != nil {
		t.Fatal(err)
	}
	first := nextAttempt(t, source)
	apply(t, s, Operation{Action: Seek, Offset: time.Second})
	previous := nextAttempt(t, source)
	for range 2 {
		awaitDelivered(t, s)
		apply(t, s, Operation{Action: VoiceLost})
		next := nextAttempt(t, source)
		if next.request.ID != first.request.ID || next.request.AttemptID == previous.request.AttemptID || next.request.Offset != previous.request.Offset+playback.FrameDuration {
			t.Fatalf("resume identities/offset: previous=%+v next=%+v", previous.request, next.request)
		}
		previous = next
	}
	awaitDelivered(t, s)
	apply(t, s, Operation{Action: Disconnect})
	deadline := time.Now().Add(time.Second)
	for {
		_, err := s.Join(context.Background(), "voice")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	next := nextAttempt(t, source)
	if next.request.Offset != previous.request.Offset+playback.FrameDuration {
		t.Fatalf("intentional disconnect rewound: %+v", next.request)
	}
}

func TestInterruptedRecoveryBoundariesAndControlPriority(t *testing.T) {
	for _, tc := range []struct {
		name     string
		live     bool
		duration time.Duration
		frames   uint64
		pending  playback.Outcome
		stop     bool
		retained bool
		outcome  playback.Outcome
	}{
		{"finite", false, time.Minute, 2, "", false, true, ""},
		{"end", false, time.Second + playback.FrameDuration, 1, "", false, false, playback.TransportFailure},
		{"overflow", false, time.Minute, math.MaxUint64, "", false, false, playback.TransportFailure},
		{"live", true, time.Minute, 1, "", false, false, playback.TransportFailure},
		{"unknown", false, 0, 1, "", false, false, playback.TransportFailure},
		{"skip owns race", false, time.Minute, 1, playback.Skipped, false, false, playback.Skipped},
		{"disconnect owns race", false, time.Minute, 1, playback.Disconnected, false, true, ""},
		{"stop retains established transport failure", false, time.Minute, 1, playback.TransportFailure, true, false, playback.TransportFailure},
		{"stop cancels running attempt", false, time.Minute, 1, playback.Stopped, true, false, playback.Stopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ticket := &Ticket{done: make(chan struct{}), request: playback.Request{ID: "request", Offset: time.Second}}
			c := &coordinator{session: &Session{ctx: context.Background()}, cancel: func(error) {}, active: ticket, connected: true, pending: tc.pending, stopAfter: tc.stop, loop: true, recoveryAttempts: 2,
				queue: queue{index: 0, entries: []entry{{track: Track{Duration: tc.duration, Live: tc.live}, ticket: ticket}}}}
			result := playback.Result{Request: ticket.request, Outcome: playback.TransportFailure, Frames: tc.frames}
			// Engine normally returns the winning cancellation cause.
			if tc.pending != "" {
				result.Outcome = tc.pending
			}
			c.complete(result)
			if tc.retained {
				if c.queue.current() == nil || ticket.request.Offset != time.Second+timeForFrames(tc.frames) || c.recoveryAttempts != 2 {
					t.Fatal("lost position or replenished recovery budget")
				}
				select {
				case <-ticket.done:
					t.Fatal("settled resumable request")
				default:
				}
			} else {
				select {
				case <-ticket.done:
				default:
					t.Fatal("ticket not settled")
				}
				if c.queue.current() != nil || ticket.result.Outcome != tc.outcome {
					t.Fatalf("terminal=%+v", ticket.result)
				}
			}
		})
	}
}
