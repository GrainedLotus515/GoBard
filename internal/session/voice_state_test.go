package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

// describeVoice names a state and the data that decides its next transition.
func describeVoice(s voiceState) string {
	switch s := s.(type) {
	case voiceLost:
		if s.release {
			return "Lost{release}"
		}
		return "Lost"
	case voiceReleasing:
		if s.abandoned != nil {
			return "Releasing{" + describeVoice(s.abandoned) + "}"
		}
		return "Releasing"
	case voiceConnecting:
		if s.observedSet {
			return fmt.Sprintf("Connecting{observed=%q}", s.observed)
		}
		return "Connecting"
	case voiceReconnecting:
		if s.observedSet {
			return fmt.Sprintf("Reconnecting{observed=%q}", s.observed)
		}
		return "Reconnecting"
	case nil:
		return "<nil>"
	}
	return fmt.Sprintf("%T", s)[len("session.voice"):]
}

func TestVoiceTransitions(t *testing.T) {
	ok := transportResult{output: outputFunc(func(context.Context, []byte) error { return nil })}
	failed := transportResult{err: errors.New("dial failed")}
	connecting := func() voiceState { return voiceConnecting{dial: dial{cancel: func() {}}} }
	reconnecting := func() voiceState { return voiceReconnecting{dial: dial{cancel: func() {}}} }
	backoff := func() voiceState { return voiceBackoff{timer: time.NewTimer(time.Hour)} }
	for _, tc := range []struct {
		name     string
		from     func() voiceState
		event    voiceEvent
		queued   bool
		active   bool
		pending  playback.Outcome
		attempts int
		want     string
		launched bool // a Connect/Disconnect was started
		channel  string
		reply    error // nil: no reply expected or a successful one
	}{
		{name: "join from disconnected", from: func() voiceState { return voiceDisconnected{} }, event: voiceEvent{kind: voiceJoin, channel: "a"}, want: "Connecting", launched: true, channel: "a"},
		{name: "join while backing off", from: backoff, event: voiceEvent{kind: voiceJoin, channel: "a"}, queued: true, want: "Backoff", reply: ErrBusy},
		{name: "join elsewhere while connected", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceJoin, channel: "b"}, want: "Connected", reply: ErrVoiceAccess},
		{name: "join while lost", from: func() voiceState { return voiceLost{} }, event: voiceEvent{kind: voiceJoin, channel: "a"}, queued: true, active: true, pending: playback.TransportFailure, want: "Lost", reply: ErrBusy},

		{name: "connect succeeds", from: connecting, event: voiceEvent{kind: voiceTransportDone, result: ok}, want: "Connected"},
		{name: "connect fails", from: connecting, event: voiceEvent{kind: voiceTransportDone, result: failed}, want: "Releasing"},
		{name: "recover succeeds", from: reconnecting, event: voiceEvent{kind: voiceTransportDone, result: ok}, queued: true, attempts: 1, want: "Connected"},
		{name: "recover fails with budget", from: reconnecting, event: voiceEvent{kind: voiceTransportDone, result: failed}, queued: true, attempts: 1, want: "Backoff"},
		{name: "recover fails exhausted", from: reconnecting, event: voiceEvent{kind: voiceTransportDone, result: failed}, queued: true, attempts: 3, want: "Releasing"},
		{name: "abandoned connect reports", from: func() voiceState { return voiceReleasing{abandoned: connecting()} }, event: voiceEvent{kind: voiceTransportDone, result: ok}, want: "Releasing"},
		{name: "disconnect completes", from: func() voiceState { return voiceDisconnecting{cancel: func() {}} }, event: voiceEvent{kind: voiceTransportDone}, want: "Disconnected"},
		{name: "observed during connect recorded", from: connecting, event: voiceEvent{kind: voiceObserved, channel: ""}, want: `Connecting{observed=""}`, channel: "a"},
		{name: "observed during recover recorded", from: reconnecting, event: voiceEvent{kind: voiceObserved, channel: "b"}, queued: true, want: `Reconnecting{observed="b"}`, channel: "a"},

		{name: "observed own channel", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceObserved, channel: "a"}, want: "Connected", channel: "a"},
		{name: "moved while idle leaves", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceObserved, channel: "b"}, want: "Releasing", channel: "b"},
		{name: "moved with queued work follows", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceObserved, channel: "b"}, queued: true, want: "Backoff", channel: "b"},
		{name: "moved during attempt", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceObserved, channel: "b"}, queued: true, active: true, want: "Lost", channel: "b"},
		{name: "kicked while connected", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceObserved, channel: ""}, queued: true, want: "Releasing", channel: "a"},
		{name: "kicked while lost", from: func() voiceState { return voiceLost{} }, event: voiceEvent{kind: voiceObserved, channel: ""}, queued: true, active: true, pending: playback.TransportFailure, want: "Releasing", channel: "a"},
		{name: "kicked after stop while lost", from: func() voiceState { return voiceLost{release: true} }, event: voiceEvent{kind: voiceObserved, channel: ""}, queued: true, active: true, pending: playback.TransportFailure, want: "Releasing", channel: "a"},
		{name: "moved again while lost retargets", from: func() voiceState { return voiceLost{} }, event: voiceEvent{kind: voiceObserved, channel: "c"}, queued: true, active: true, pending: playback.TransportFailure, want: "Lost", channel: "c"},
		{name: "idle timeout leaves", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceIdleTimeout, reason: "paused_timeout"}, queued: true, active: true, want: "Releasing", channel: "a"},
		{name: "kicked during backoff", from: backoff, event: voiceEvent{kind: voiceObserved, channel: ""}, queued: true, want: "Releasing", channel: "a"},
		{name: "moved during backoff retargets", from: backoff, event: voiceEvent{kind: voiceObserved, channel: "c"}, queued: true, want: "Backoff", channel: "c"},
		{name: "observed while disconnected ignored", from: func() voiceState { return voiceDisconnected{} }, event: voiceEvent{kind: voiceObserved, channel: ""}, want: "Disconnected", channel: "a"},

		{name: "output failure recovers", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceTransportFailed}, queued: true, want: "Backoff"},
		{name: "output failure exhausted", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceTransportFailed}, queued: true, attempts: 3, want: "Releasing"},
		{name: "lost attempt failed", from: func() voiceState { return voiceLost{} }, event: voiceEvent{kind: voiceTransportFailed}, queued: true, want: "Backoff"},
		{name: "lost attempt failed after stop", from: func() voiceState { return voiceLost{release: true} }, event: voiceEvent{kind: voiceTransportFailed}, queued: true, want: "Releasing"},
		{name: "lost during control joined with work", from: func() voiceState { return voiceLost{} }, event: voiceEvent{kind: voiceAttemptJoined, cause: playback.Skipped}, queued: true, want: "Backoff"},
		{name: "lost during control joined idle", from: func() voiceState { return voiceLost{} }, event: voiceEvent{kind: voiceAttemptJoined, cause: playback.Skipped}, want: "Releasing"},
		{name: "lost then completed idle", from: func() voiceState { return voiceLost{} }, event: voiceEvent{kind: voiceAttemptJoined, cause: playback.TransportFailure}, want: "Disconnected"},
		{name: "lost then completed with work", from: func() voiceState { return voiceLost{} }, event: voiceEvent{kind: voiceAttemptJoined, cause: playback.TransportFailure}, queued: true, want: "Backoff"},
		{name: "released lost joined", from: func() voiceState { return voiceLost{release: true} }, event: voiceEvent{kind: voiceAttemptJoined, cause: playback.Skipped}, queued: true, want: "Releasing"},
		{name: "attempt joined while connected", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceAttemptJoined}, queued: true, want: "Connected"},

		{name: "stop keeps healthy connection", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceStop}, want: "Connected"},
		{name: "stop cancels connect", from: connecting, event: voiceEvent{kind: voiceStop}, want: "Releasing{Connecting}"},
		{name: "stop cancels recover", from: reconnecting, event: voiceEvent{kind: voiceStop}, want: "Releasing{Reconnecting}"},
		{name: "stop during backoff", from: backoff, event: voiceEvent{kind: voiceStop}, queued: true, want: "Releasing"},
		{name: "stop while lost", from: func() voiceState { return voiceLost{} }, event: voiceEvent{kind: voiceStop}, queued: true, active: true, pending: playback.TransportFailure, want: "Lost{release}"},
		{name: "stop while disconnecting", from: func() voiceState { return voiceDisconnecting{cancel: func() {}} }, event: voiceEvent{kind: voiceStop}, want: "Disconnecting"},
		{name: "disconnect while connected", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceDisconnect}, want: "Releasing"},
		{name: "disconnect while disconnected", from: func() voiceState { return voiceDisconnected{} }, event: voiceEvent{kind: voiceDisconnect}, want: "Releasing"},

		{name: "owed disconnect starts", from: func() voiceState { return voiceReleasing{} }, event: voiceEvent{kind: voiceQuiescent}, want: "Disconnecting", launched: true},
		{name: "owed disconnect waits for attempt", from: func() voiceState { return voiceReleasing{} }, event: voiceEvent{kind: voiceQuiescent}, queued: true, active: true, pending: playback.Disconnected, want: "Releasing"},
		{name: "owed disconnect waits for abandoned connect", from: func() voiceState { return voiceReleasing{abandoned: connecting()} }, event: voiceEvent{kind: voiceQuiescent}, want: "Releasing{Connecting}"},
		{name: "retry reconnects", from: backoff, event: voiceEvent{kind: voiceRetry}, queued: true, attempts: 1, want: "Reconnecting", launched: true},
		{name: "stale retry ignored", from: func() voiceState { return voiceConnected{} }, event: voiceEvent{kind: voiceRetry}, want: "Connected"},
		{name: "shutdown during backoff", from: backoff, event: voiceEvent{kind: voiceShutdown}, queued: true, want: "Disconnected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := &coordinator{session: &Session{ctx: ctx}, voice: voiceFixture{connect: func(context.Context, string) (playback.Output, error) { return ok.output, nil }},
				logger: log.New(io.Discard), speakers: make(map[string]bool), channel: "a", transportResults: make(chan transportResult, 1),
				queue: queue{index: -1, capacity: 8}, cancel: func(error) {}, pending: tc.pending, recoveryAttempts: tc.attempts, link: tc.from()}
			if tc.queued {
				ticket := &Ticket{done: make(chan struct{}), request: playback.Request{ID: "request"}}
				c.queue = queue{index: 0, capacity: 8, entries: []entry{{track: Track{Duration: time.Minute}, ticket: ticket}}}
				if tc.active {
					c.active = ticket
				}
			}
			event := tc.event
			if event.kind == voiceJoin {
				event.reply = make(chan response, 1)
			}
			if s, ok := c.link.(voiceConnecting); ok {
				// Connecting always carries the join's acknowledgement.
				s.reply = make(chan response, 1)
				c.link = s
			}
			if s, ok := c.link.(voiceReleasing); ok {
				if j, ok := s.abandoned.(voiceConnecting); ok {
					j.reply = make(chan response, 1)
					c.link = voiceReleasing{abandoned: j}
				}
			}
			c.voiceEvent(event)
			defer stopBackoff(c)
			if got := describeVoice(c.link); got != tc.want {
				t.Fatalf("state = %s, want %s", got, tc.want)
			}
			if tc.channel != "" && c.channel != tc.channel {
				t.Fatalf("channel = %q, want %q", c.channel, tc.channel)
			}
			wait := 20 * time.Millisecond
			if tc.launched {
				wait = 2 * time.Second
			}
			select {
			case <-c.transportResults:
				if !tc.launched {
					t.Fatal("unexpected transport operation")
				}
			case <-time.After(wait):
				if tc.launched {
					t.Fatal("transport operation not launched")
				}
			}
			if event.reply != nil && tc.reply != nil {
				if r := <-event.reply; !errors.Is(r.err, tc.reply) {
					t.Fatalf("reply = %v, want %v", r.err, tc.reply)
				}
			}
		})
	}
}

// Snapshot projections of each state must match the pre-refactor fields.
func TestVoiceStateSnapshot(t *testing.T) {
	for _, tc := range []struct {
		state                 voiceState
		connected, connecting bool
		busy                  bool
		recovery              string
		attempts              int
	}{
		{state: voiceDisconnected{}},
		{state: voiceDisconnected{}, attempts: 3, recovery: "voice_exhausted"},
		{state: voiceConnected{}, connected: true},
		{state: voiceConnected{}, connected: true, attempts: 3},
		{state: voiceConnecting{}, connecting: true, busy: true},
		{state: voiceReconnecting{}, connecting: true, busy: true, recovery: "voice_reconnect"},
		{state: voiceBackoff{}, busy: true, recovery: "voice_reconnect"},
		{state: voiceReleasing{}, busy: true},
		{state: voiceReleasing{}, busy: true, attempts: 3, recovery: "voice_exhausted"},
		{state: voiceReleasing{abandoned: voiceConnecting{}}, connecting: true, busy: true},
		{state: voiceReleasing{abandoned: voiceReconnecting{}}, connecting: true, busy: true, recovery: "voice_reconnect"},
		{state: voiceDisconnecting{}, busy: true},
	} {
		c := &coordinator{queue: queue{index: -1}, link: tc.state, recoveryAttempts: tc.attempts}
		s := c.snapshot()
		if s.Connected != tc.connected || s.Connecting != tc.connecting || s.Busy != tc.busy || s.Recovery != tc.recovery {
			t.Errorf("%s attempts=%d: connected=%v connecting=%v busy=%v recovery=%q", describeVoice(tc.state), tc.attempts, s.Connected, s.Connecting, s.Busy, s.Recovery)
		}
	}
}
