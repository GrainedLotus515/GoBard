package session

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

func idleTestSession(t *testing.T, idle IdleTimeouts) (*Session, *controlledSource, chan struct{}) {
	t.Helper()
	source := &controlledSource{opened: make(chan attemptFixture, 32)}
	logger := log.New(io.Discard)
	engine, err := playback.New(source, logger)
	if err != nil {
		t.Fatal(err)
	}
	left := make(chan struct{}, 8)
	voice := voiceFixture{
		connect: func(context.Context, string) (playback.Output, error) {
			return outputFunc(func(context.Context, []byte) error { return nil }), nil
		},
		disconnect: func(context.Context) error { left <- struct{}{}; return nil },
	}
	s, err := newIdleSession(context.Background(), "idle-test", engine, nil, voice, logger, nil, 8, idle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Join(context.Background(), "voice"); err != nil {
		t.Fatal(err)
	}
	return s, source, left
}

func expectLeft(t *testing.T, left chan struct{}, want bool, within time.Duration) {
	t.Helper()
	select {
	case <-left:
		if !want {
			t.Fatal("left voice before the idle condition held for the timeout")
		}
	case <-time.After(within):
		if want {
			t.Fatal("did not leave voice after the idle timeout")
		}
	}
}

func TestPauseTimeoutLeavesVoiceAndKeepsQueue(t *testing.T) {
	s, source, left := idleTestSession(t, IdleTimeouts{Paused: 60 * time.Millisecond})
	tickets, err := s.Add(context.Background(), []Track{{MediaID: "audio", Duration: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	first := nextAttempt(t, source)
	// Resuming before the timeout restarts the clock.
	apply(t, s, Operation{Action: Pause})
	time.Sleep(30 * time.Millisecond)
	apply(t, s, Operation{Action: Resume})
	expectLeft(t, left, false, 120*time.Millisecond)

	apply(t, s, Operation{Action: Pause})
	expectLeft(t, left, true, time.Second)
	snap := apply(t, s, Operation{Action: Inspect})
	if snap.Connected || snap.RequestID != first.request.ID {
		t.Fatalf("after pause timeout connected=%v request=%q, want queue kept", snap.Connected, snap.RequestID)
	}
	select {
	case <-tickets[0].done:
		t.Fatalf("queued request settled: %+v", tickets[0].result)
	default:
	}
	// Joining again resumes the same request.
	if _, err := s.Join(context.Background(), "voice"); err != nil {
		t.Fatal(err)
	}
	if next := nextAttempt(t, source); next.request.ID != first.request.ID {
		t.Fatal("rejoin did not resume the kept request")
	}
}

func TestAloneTimeoutLeavesVoiceOnlyForOwnChannel(t *testing.T) {
	s, source, left := idleTestSession(t, IdleTimeouts{Alone: 60 * time.Millisecond})
	if _, err := s.Add(context.Background(), []Track{{MediaID: "audio", Duration: time.Minute}}); err != nil {
		t.Fatal(err)
	}
	first := nextAttempt(t, source)
	// Another channel being empty is irrelevant.
	if err := s.ObserveAudience("other", true); err != nil {
		t.Fatal(err)
	}
	expectLeft(t, left, false, 120*time.Millisecond)
	// A listener returning before the timeout keeps the bot.
	if err := s.ObserveAudience("voice", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := s.ObserveAudience("voice", false); err != nil {
		t.Fatal(err)
	}
	expectLeft(t, left, false, 120*time.Millisecond)

	if err := s.ObserveAudience("voice", true); err != nil {
		t.Fatal(err)
	}
	expectLeft(t, left, true, time.Second)
	if snap := apply(t, s, Operation{Action: Inspect}); snap.Connected || snap.RequestID != first.request.ID {
		t.Fatalf("after alone timeout connected=%v request=%q, want queue kept", snap.Connected, snap.RequestID)
	}
}

func TestIdleTimeoutsDisabledByZero(t *testing.T) {
	s, source, left := idleTestSession(t, IdleTimeouts{})
	if _, err := s.Add(context.Background(), []Track{{MediaID: "audio", Duration: time.Minute}}); err != nil {
		t.Fatal(err)
	}
	_ = nextAttempt(t, source)
	apply(t, s, Operation{Action: Pause})
	if err := s.ObserveAudience("voice", true); err != nil {
		t.Fatal(err)
	}
	expectLeft(t, left, false, 150*time.Millisecond)
}

func aloneCoordinator(t *testing.T) *coordinator {
	t.Helper()
	ticket := &Ticket{done: make(chan struct{})}
	c := &coordinator{session: &Session{ctx: context.Background(), observed: make(chan struct{}, 1), memberUpdates: make(map[string]memberObservation)},
		voice: voiceFixture{}, logger: log.New(io.Discard), speakers: make(map[string]bool), connected: true, channel: "voice", idle: IdleTimeouts{Alone: time.Hour},
		queue: queue{index: 0, capacity: 8, entries: []entry{{track: Track{Duration: time.Minute}, ticket: ticket}}}}
	t.Cleanup(func() {
		if c.aloneTimer != nil {
			c.aloneTimer.Stop()
		}
	})
	if err := c.session.ObserveAudience("voice", true); err != nil {
		t.Fatal(err)
	}
	c.drainObserved()
	c.armIdle()
	if c.aloneIdle == nil {
		t.Fatal("alone timer not armed")
	}
	return c
}

func TestBriefListenerRestartsAloneClock(t *testing.T) {
	c := aloneCoordinator(t)
	// Left again before the coordinator drained: the latest value is still
	// alone, but the clock must restart.
	for _, alone := range []bool{false, true} {
		if err := c.session.ObserveAudience("voice", alone); err != nil {
			t.Fatal(err)
		}
	}
	c.drainObserved()
	if c.aloneIdle != nil {
		t.Fatal("interrupted alone period kept its original deadline")
	}
}

func TestAloneExpiryYieldsToPublishedListener(t *testing.T) {
	c := aloneCoordinator(t)
	if err := c.session.ObserveAudience("voice", false); err != nil {
		t.Fatal(err)
	}
	c.aloneExpired()
	if !c.connected || c.needDisconnect {
		t.Fatal("left an occupied channel")
	}
	if err := c.session.ObserveAudience("voice", true); err != nil {
		t.Fatal(err)
	}
	c.drainObserved()
	c.armIdle()
	c.aloneExpired()
	if c.connected || !c.needDisconnect {
		t.Fatal("did not leave when still alone at expiry")
	}
}
