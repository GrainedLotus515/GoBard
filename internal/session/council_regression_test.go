package session

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

func pendingCoordinator(pending playback.Outcome) (*coordinator, *Ticket) {
	ticket := &Ticket{done: make(chan struct{}), request: playback.Request{ID: "request"}}
	c := &coordinator{session: &Session{ctx: context.Background()}, cancel: func(error) {}, active: ticket, link: voiceConnected{}, channel: "original", pending: pending,
		voice: voiceFixture{}, logger: log.New(io.Discard), speakers: make(map[string]bool), volume: 100, duckTarget: 50, ducking: true,
		queue: queue{index: 0, capacity: 8, entries: []entry{{track: Track{Duration: time.Minute}, ticket: ticket}, {track: Track{Duration: time.Minute}, ticket: &Ticket{done: make(chan struct{})}}}}}
	return c, ticket
}

func TestLifecycleObservationsAcceptedDuringPendingControl(t *testing.T) {
	c, _ := pendingCoordinator(playback.Skipped)
	c.speakers["speaker"] = true
	if err := c.apply(Operation{Action: Speaking, Speaker: "speaker"}); err != nil {
		t.Fatalf("speaking stop rejected during pending skip: %v", err)
	}
	if err := c.apply(Operation{Action: Speaking, Speaker: "other", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.apply(Operation{Action: VoiceMember, Speaker: "other", Channel: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if len(c.speakers) != 0 {
		t.Fatalf("stale speakers retained: %v", c.speakers)
	}
	if err := c.apply(Operation{Action: Volume, Position: 10}); !errors.Is(err, ErrBusy) {
		t.Fatalf("competing control accepted during pending transition: %v", err)
	}
}

func TestVoiceLostDuringPendingControlRecoversAfterJoin(t *testing.T) {
	c, ticket := pendingCoordinator(playback.Skipped)
	generation := c.generation
	if err := c.apply(Operation{Action: VoiceLost, Channel: "moved"}); err != nil {
		t.Fatalf("voice loss rejected during pending skip: %v", err)
	}
	if c.connected() || c.channel != "moved" || c.pending != playback.Skipped {
		t.Fatalf("observation not recorded or pending cause overwritten: state=%T channel=%q pending=%q", c.link, c.channel, c.pending)
	}
	c.complete(playback.Result{Request: ticket.request, Outcome: playback.Skipped, Frames: 1})
	select {
	case <-ticket.done:
	default:
		t.Fatal("skipped request not settled")
	}
	if ticket.result.Outcome != playback.Skipped {
		t.Fatalf("skip outcome replaced: %q", ticket.result.Outcome)
	}
	if !inBackoff(c) || c.generation == generation {
		t.Fatal("voice recovery not scheduled after pending attempt joined")
	}
	stopBackoff(c)
}

func TestStopRetainedRequestReportsStopNotEarlierAttempt(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		ticket := &Ticket{done: make(chan struct{}), request: playback.Request{ID: "request", Offset: time.Second}}
		c := &coordinator{session: &Session{ctx: context.Background()}, queue: queue{index: 0, capacity: 8, entries: []entry{{track: Track{Duration: time.Minute}, ticket: ticket}}},
			last: playback.Result{Request: ticket.request, Outcome: playback.Disconnected, Frames: 50}}
		want := playback.Stopped
		if shutdown {
			want = playback.Shutdown
			c.settleAll(playback.Shutdown, nil)
		} else {
			c.stop()
		}
		select {
		case <-ticket.done:
		default:
			t.Fatal("retained request not settled")
		}
		if ticket.result.Outcome != want {
			t.Fatalf("retained request outcome = %q, want %q", ticket.result.Outcome, want)
		}
	}
}

func TestObservationsCoalesceLatestWithoutMailbox(t *testing.T) {
	s := &Session{observed: make(chan struct{}, 1), memberUpdates: make(map[string]memberObservation)}
	for _, channel := range []string{"a", "b", ""} {
		if err := s.ObserveVoice(channel); err != nil {
			t.Fatal(err)
		}
	}
	for _, speaking := range []bool{true, false, true, false} {
		if err := s.ObserveSpeaker("speaker", speaking); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ObserveMember("member", "elsewhere"); err != nil {
		t.Fatal(err)
	}
	// A same-channel voice-state update (e.g. self-mute) must not replace the
	// pending speaking=false for the same member.
	if err := s.ObserveMember("speaker", "here"); err != nil {
		t.Fatal(err)
	}
	o := s.takeObserved(nil)
	channel, voice, speakers := o.voice, o.hasVoice, o.speakers
	if !voice || channel != "" || !o.hasPrior || o.prior != "b" {
		t.Fatalf("latest voice observation lost: %+v", o)
	}
	if len(speakers) != 3 {
		t.Fatalf("observations not coalesced per member and kind: %+v", speakers)
	}
	var order []Action
	for _, op := range speakers {
		if op.Speaker == "speaker" {
			order = append(order, op.Action)
			if op.Action == Speaking && op.Enabled {
				t.Fatalf("stale speaker observation won: %+v", op)
			}
		}
	}
	if len(order) != 2 || order[0] != Speaking || order[1] != VoiceMember {
		t.Fatalf("speaking update lost or replayed out of order: %v", order)
	}
	if o := s.takeObserved(nil); o.hasVoice || len(o.speakers) != 0 {
		t.Fatal("observations delivered twice")
	}
}

func TestObservationsAreBounded(t *testing.T) {
	s := &Session{observed: make(chan struct{}, 1), memberUpdates: make(map[string]memberObservation)}
	if err := s.ObserveVoice(string(make([]byte, 33))); err == nil {
		t.Fatal("oversized channel accepted")
	}
	for i := range maxPendingSpeakers {
		if err := s.ObserveSpeaker(string(rune('A'+i%26))+string(rune('a'+i/26)), true); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ObserveSpeaker("overflow", true); !errors.Is(err, ErrBusy) {
		t.Fatalf("unbounded speaker observations: %v", err)
	}
	if !s.takeObserved(nil).resetSpeakers {
		t.Fatal("overflow did not request a speaker reset")
	}
}

func TestSpeakerObservationsReachCoordinator(t *testing.T) {
	s, source := newControlsTest(t)
	if _, err := s.Add(context.Background(), tracks("a")); err != nil {
		t.Fatal(err)
	}
	controls := nextAttempt(t, source).request.Controls
	apply(t, s, Operation{Action: DuckTarget, Position: 20})
	apply(t, s, Operation{Action: Ducking, Enabled: true})
	awaitVolume := func(want int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for int(controls.Volume()) != want {
			if time.Now().After(deadline) {
				t.Fatalf("volume = %d, want %d", controls.Volume(), want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if err := s.ObserveSpeaker("speaker", true); err != nil {
		t.Fatal(err)
	}
	awaitVolume(20)
	if err := s.ObserveSpeaker("speaker", false); err != nil {
		t.Fatal(err)
	}
	awaitVolume(100)
	if err := s.ObserveSpeaker("speaker", true); err != nil {
		t.Fatal(err)
	}
	awaitVolume(20)
	if err := s.ObserveMember("speaker", "elsewhere"); err != nil {
		t.Fatal(err)
	}
	awaitVolume(100)
}

func TestVoiceObservationDuringConnectReconciledOnSuccess(t *testing.T) {
	c := &coordinator{session: &Session{ctx: context.Background()}, voice: voiceFixture{}, logger: log.New(io.Discard), speakers: make(map[string]bool),
		channel: "target", link: voiceReconnecting{dial: dial{cancel: func() {}}},
		queue: queue{index: 0, capacity: 8, entries: []entry{{track: Track{Duration: time.Minute}, ticket: &Ticket{done: make(chan struct{})}}}}}
	c.voiceLost("moved")
	connectSucceeds(c)
	if c.connected() || c.channel != "moved" || !inBackoff(c) {
		t.Fatalf("newer observation overwritten by connect success: state=%T channel=%q", c.link, c.channel)
	}
	stopBackoff(c)
}

func TestVoiceObservationRetargetsPendingRecovery(t *testing.T) {
	c := &coordinator{session: &Session{ctx: context.Background()}, voice: voiceFixture{}, logger: log.New(io.Discard), channel: "b", queue: queue{index: -1, capacity: 8}}
	c.link = c.recoverNext()
	c.voiceLost("c")
	if c.channel != "c" {
		t.Fatalf("recovery targets stale channel %q", c.channel)
	}
	stopBackoff(c)
}

func TestInspectHonorsCallerDeadlineAfterAdmission(t *testing.T) {
	// No coordinator drains the mailbox, modeling a session stuck in shutdown.
	s := &Session{ctx: context.Background(), done: make(chan struct{}), commands: make(chan command, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Apply(ctx, Operation{Action: Inspect}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("inspect = %v", err)
	}
}

func TestRemoveDuringConstructionClosesSession(t *testing.T) {
	engine, err := playback.New(&controlledSource{opened: make(chan attemptFixture, 1)}, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(context.Background(), engine, 4, 1, 8, Defaults{Volume: 100, DuckTarget: 50})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	var built *Session
	_, err = m.create("guild", func() (*Session, error) {
		if err := m.Remove("guild"); err != nil {
			t.Errorf("remove during construction = %v", err)
		}
		built, err = newSession(m.ctx, "guild", engine, outputFunc(func(context.Context, []byte) error { return nil }), nil, nil, m.streamSlots, 8)
		return built, err
	})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("create = %v", err)
	}
	select {
	case <-built.Done():
	default:
		t.Fatal("session constructed during removal was not closed")
	}
	if _, ok := m.Get("guild"); ok {
		t.Fatal("removed guild still registered")
	}
	if _, err := m.Create("guild", outputFunc(func(context.Context, []byte) error { return nil })); err != nil {
		t.Fatalf("guild slot not released: %v", err)
	}
}

func TestSecondMoveDuringVoiceLossTeardownRetargets(t *testing.T) {
	c, _ := pendingCoordinator("")
	c.voiceLost("b")
	c.voiceLost("c")
	if c.channel != "c" {
		t.Fatalf("recovery would drag bot back to %q", c.channel)
	}
}

func TestJoinNotAcknowledgedWhenLostDuringConnect(t *testing.T) {
	reply := make(chan response, 1)
	c := &coordinator{session: &Session{ctx: context.Background()}, voice: voiceFixture{}, logger: log.New(io.Discard), speakers: make(map[string]bool),
		channel: "target", link: voiceConnecting{dial: dial{cancel: func() {}}, reply: reply}, queue: queue{index: -1, capacity: 8}}
	c.voiceLost("")
	connectSucceeds(c)
	if r := <-reply; !errors.Is(r.err, ErrSuperseded) {
		t.Fatalf("join acknowledged despite observed loss: %+v", r)
	}
	stopBackoff(c)
}

func TestStopAfterPendingSeekReportsStop(t *testing.T) {
	c, ticket := pendingCoordinator(playback.Seeked)
	c.stopAfter = true
	c.complete(playback.Result{Request: ticket.request, Outcome: playback.Seeked, Frames: 3})
	if ticket.result.Outcome != playback.Stopped || ticket.result.Frames != 3 {
		t.Fatalf("request ended as %q with %d frames", ticket.result.Outcome, ticket.result.Frames)
	}
}

func TestDepartureAfterSpeakingDoesNotResurrectSpeaker(t *testing.T) {
	c, _ := pendingCoordinator("")
	c.session = &Session{ctx: context.Background(), observed: make(chan struct{}, 1), memberUpdates: make(map[string]memberObservation)}
	for _, observe := range []func() error{
		func() error { return c.session.ObserveSpeaker("speaker", true) },
		func() error { return c.session.ObserveMember("speaker", "") },
	} {
		if err := observe(); err != nil {
			t.Fatal(err)
		}
	}
	c.drainObserved()
	if len(c.speakers) != 0 {
		t.Fatalf("departed speaker resurrected: %v", c.speakers)
	}
	// Rejoin then speak: the newer speaking state wins.
	if err := c.session.ObserveMember("speaker", "original"); err != nil {
		t.Fatal(err)
	}
	if err := c.session.ObserveSpeaker("speaker", true); err != nil {
		t.Fatal(err)
	}
	c.drainObserved()
	if !c.speakers["speaker"] {
		t.Fatal("speaking after rejoin ignored")
	}
}

func TestPublishedObservationAppliedBeforeLaterControl(t *testing.T) {
	joined := make(chan string, 4)
	s, source := voiceTestSession(t, voiceFixture{connect: func(_ context.Context, channel string) (playback.Output, error) {
		joined <- channel
		return outputFunc(func(context.Context, []byte) error { return nil }), nil
	}})
	if _, err := s.Join(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	<-joined
	if _, err := s.Add(context.Background(), []Track{{Provider: "fixture", MediaID: "audio", Duration: time.Minute}}); err != nil {
		t.Fatal(err)
	}
	nextAttempt(t, source)
	if err := s.ObserveVoice("b"); err != nil {
		t.Fatal(err)
	}
	// A user still in the old channel must be judged against the move.
	if _, err := s.ApplyAs(context.Background(), Access{Channel: "a"}, Operation{Action: Pause}); !errors.Is(err, ErrVoiceAccess) {
		t.Fatalf("stale-channel user controlled moved session: %v", err)
	}
}

func TestVoiceLossForgetsSpeakers(t *testing.T) {
	c, _ := pendingCoordinator("")
	c.speakers["speaker"] = true
	c.voiceLost("moved")
	if len(c.speakers) != 0 {
		t.Fatalf("speakers from lost connection retained: %v", c.speakers)
	}
}

func TestStopThenVoiceLossReleasesInsteadOfRejoining(t *testing.T) {
	c, ticket := pendingCoordinator(playback.Stopped)
	c.stopAfter = true
	c.voiceLost("")
	c.complete(playback.Result{Request: ticket.request, Outcome: playback.Stopped, Frames: 1})
	if inBackoff(c) || !c.owesDisconnect() {
		t.Fatalf("stopped and kicked bot would rejoin: state=%T", c.link)
	}
}

func TestIdleVoiceLossReleasesConnection(t *testing.T) {
	for _, channel := range []string{"", "moved"} {
		c := &coordinator{session: &Session{ctx: context.Background()}, voice: voiceFixture{}, logger: log.New(io.Discard), speakers: make(map[string]bool),
			link: voiceConnected{}, channel: "original", queue: queue{index: -1, capacity: 8}}
		generation := c.generation
		c.voiceLost(channel)
		if inBackoff(c) || !c.owesDisconnect() || c.generation == generation {
			t.Fatalf("%q: idle loss state=%T generation bumped=%v", channel, c.link, c.generation != generation)
		}
	}
}

func TestLeaveAndRejoinBeforeDrainClearsSpeaker(t *testing.T) {
	for _, pendingSpeaking := range []bool{false, true} {
		c, _ := pendingCoordinator("")
		c.session = &Session{ctx: context.Background(), observed: make(chan struct{}, 1), memberUpdates: make(map[string]memberObservation)}
		c.speakers["speaker"] = true
		if pendingSpeaking {
			if err := c.session.ObserveSpeaker("speaker", true); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.session.ObserveMember("speaker", ""); err != nil {
			t.Fatal(err)
		}
		if err := c.session.ObserveMember("speaker", "original"); err != nil {
			t.Fatal(err)
		}
		c.drainObserved()
		if len(c.speakers) != 0 {
			t.Fatalf("pending speaking=%v: silent rejoin kept stale speaker", pendingSpeaking)
		}
	}
}

func TestCompletionRacingVoiceLossStaysCompleted(t *testing.T) {
	for _, more := range []bool{false, true} {
		c, ticket := pendingCoordinator("")
		if !more {
			c.queue.entries = c.queue.entries[:1]
		}
		c.voiceLost("moved")
		c.complete(playback.Result{Request: ticket.request, Outcome: playback.Completed, Frames: 10})
		if ticket.result.Outcome != playback.Completed {
			t.Fatalf("completion overwritten with %q", ticket.result.Outcome)
		}
		if c.connected() {
			t.Fatal("lost connection treated as live")
		}
		if inBackoff(c) != more {
			t.Fatalf("more work=%v but recovery scheduled=%v", more, inBackoff(c))
		}
		stopBackoff(c)
	}
}

func TestBotMovedAwayAndBackBeforeDrainStillReconciles(t *testing.T) {
	c, _ := pendingCoordinator("")
	c.session = &Session{ctx: context.Background(), observed: make(chan struct{}, 1), memberUpdates: make(map[string]memberObservation)}
	c.speakers["speaker"] = true
	for _, channel := range []string{"elsewhere", "original"} {
		if err := c.session.ObserveVoice(channel); err != nil {
			t.Fatal(err)
		}
	}
	c.drainObserved()
	if c.connected() || c.pending != playback.TransportFailure || c.channel != "original" || len(c.speakers) != 0 {
		t.Fatalf("intervening move erased: state=%T pending=%q channel=%q speakers=%v", c.link, c.pending, c.channel, c.speakers)
	}
}

func TestObservationOverflowResetsSpeakers(t *testing.T) {
	c, _ := pendingCoordinator("")
	c.session = &Session{ctx: context.Background(), observed: make(chan struct{}, 1), memberUpdates: make(map[string]memberObservation)}
	c.speakers["tracked"] = true
	for i := range maxPendingSpeakers {
		if err := c.session.ObserveMember(string(rune('A'+i%26))+string(rune('a'+i/26)), "elsewhere"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.session.ObserveMember("tracked", ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("observation beyond bound = %v", err)
	}
	c.drainObserved()
	if c.speakers["tracked"] {
		t.Fatal("departure lost to overflow kept stale speaker")
	}
}

func TestIndependentFailureRacingVoiceLossKeepsItsOutcome(t *testing.T) {
	c, ticket := pendingCoordinator("")
	c.queue.entries = c.queue.entries[:1]
	c.voiceLost("moved")
	failed := playback.Result{Request: ticket.request, Outcome: playback.EncodeFailure, Frames: 10, Failure: &failure.Error{Stage: "decode", Category: failure.Codec}}
	c.complete(failed)
	if ticket.result.Outcome != playback.EncodeFailure || c.queue.current() != nil {
		t.Fatalf("independent failure reinterpreted as voice loss: outcome=%q retained=%v", ticket.result.Outcome, c.queue.current() != nil)
	}
	if inBackoff(c) {
		t.Fatal("recovery scheduled with no work left")
	}
}

func TestObservationProcessesPublishedResultFirst(t *testing.T) {
	c, ticket := pendingCoordinator("")
	c.queue.entries = c.queue.entries[:1]
	c.results = make(chan playback.Result, 1)
	c.session = &Session{ctx: context.Background(), observed: make(chan struct{}, 1), memberUpdates: make(map[string]memberObservation)}
	c.results <- playback.Result{Request: ticket.request, Outcome: playback.Completed, Frames: 10}
	if err := c.session.ObserveVoice("moved"); err != nil {
		t.Fatal(err)
	}
	<-c.session.observed
	c.applyObserved()
	if ticket.result.Outcome != playback.Completed {
		t.Fatalf("observation reinterpreted finished attempt: %q", ticket.result.Outcome)
	}
}

func TestRemovalDuringPlaybackDeclinesRecoveryAndKeepsQueue(t *testing.T) {
	for _, pending := range []playback.Outcome{"", playback.TransportFailure} {
		c, ticket := pendingCoordinator(pending)
		if pending != "" {
			// Moved first (recovery owed), then removed before the attempt joined.
			c.link = voiceLost{}
		}
		c.voiceLost("")
		if c.pending != playback.Disconnected || !c.owesDisconnect() || c.connected() {
			t.Fatalf("pending=%q: removal pending=%q state=%T", pending, c.pending, c.link)
		}
		c.complete(playback.Result{Request: ticket.request, Outcome: playback.Disconnected, Frames: 50})
		if inBackoff(c) {
			t.Fatalf("pending=%q: removed bot scheduled a rejoin", pending)
		}
		select {
		case <-ticket.done:
			t.Fatalf("pending=%q: interrupted request settled instead of retained", pending)
		default:
		}
		if c.queue.current() == nil || c.queue.current().ticket.request.Offset != timeForFrames(50) {
			t.Fatalf("pending=%q: queue or resume position lost", pending)
		}
	}
}

func TestRemovalDuringRecoveryBackoffCancelsIt(t *testing.T) {
	ticket := &Ticket{done: make(chan struct{})}
	c := &coordinator{session: &Session{ctx: context.Background()}, voice: voiceFixture{}, logger: log.New(io.Discard), speakers: make(map[string]bool), channel: "voice",
		queue: queue{index: 0, capacity: 8, entries: []entry{{track: Track{Duration: time.Minute}, ticket: ticket}}}}
	c.link = c.recoverNext()
	c.voiceLost("")
	if inBackoff(c) || !c.owesDisconnect() || c.queue.current() == nil {
		t.Fatalf("removal during backoff: state=%T queued=%v", c.link, c.queue.current() != nil)
	}
}

func TestRemovalWhileReconnectingIsDistinguishedFromOwnTeardown(t *testing.T) {
	for _, tc := range []struct {
		observed []string
		removed  bool
	}{
		// Recover tears down the old connection ("") before joining again.
		{observed: []string{"", "voice"}, removed: false},
		{observed: []string{"", "voice", ""}, removed: true},
	} {
		ticket := &Ticket{done: make(chan struct{})}
		c := &coordinator{session: &Session{ctx: context.Background()}, voice: voiceFixture{}, logger: log.New(io.Discard), speakers: make(map[string]bool), channel: "voice",
			queue: queue{index: 0, capacity: 8, entries: []entry{{track: Track{Duration: time.Minute}, ticket: ticket}}},
			link:  voiceReconnecting{dial: dial{cancel: func() {}}}}
		for _, channel := range tc.observed {
			c.voiceLost(channel)
		}
		connectSucceeds(c)
		if removed := c.owesDisconnect() && !c.connected() && !inBackoff(c); removed != tc.removed {
			t.Fatalf("%q: removed=%v state=%T", tc.observed, removed, c.link)
		}
	}
}

func TestFailedRecoverCleanupIsNotMistakenForRemoval(t *testing.T) {
	// A failed recover publishes "" for its own teardown and failure cleanup,
	// before and after the new channel; neither may cancel bounded recovery.
	for _, observed := range [][]string{{""}, {"", "voice", ""}} {
		ticket := &Ticket{done: make(chan struct{})}
		c := &coordinator{session: &Session{ctx: context.Background()}, voice: voiceFixture{}, logger: log.New(io.Discard), speakers: make(map[string]bool), channel: "voice",
			queue: queue{index: 0, capacity: 8, entries: []entry{{track: Track{Duration: time.Minute}, ticket: ticket}}},
			link:  voiceReconnecting{dial: dial{cancel: func() {}}}}
		for _, channel := range observed {
			c.voiceLost(channel)
		}
		c.voiceEvent(voiceEvent{kind: voiceTransportDone, result: transportResult{err: errors.New("voice handshake failed")}})
		if !inBackoff(c) || c.owesDisconnect() {
			t.Fatalf("%q: recovery cancelled by own cleanup: state=%T", observed, c.link)
		}
		stopBackoff(c)
	}
}
func inBackoff(c *coordinator) bool {
	_, ok := c.link.(voiceBackoff)
	return ok
}

func stopBackoff(c *coordinator) {
	if s, ok := c.link.(voiceBackoff); ok {
		s.timer.Stop()
	}
}

func connectSucceeds(c *coordinator) {
	c.voiceEvent(voiceEvent{kind: voiceTransportDone, result: transportResult{output: outputFunc(func(context.Context, []byte) error { return nil })}})
}
