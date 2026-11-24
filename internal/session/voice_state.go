package session

import (
	"context"
	"errors"
	"time"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

// voiceState is the coordinator's managed voice connection. Exactly one
// concrete state is current and each carries only the data valid in it, so
// contradictions such as "connected with a retry armed" or "connecting while
// a disconnect is owed" cannot be represented. Only (*coordinator).voiceEvent
// assigns it; docs/playback-lifecycle.md has the state diagram.
//
// The per-request recovery budget (coordinator.recoveryAttempts) is policy
// that outlives any single state: a successful reconnect keeps it, and only
// queue advancement or an explicit join resets it.
type voiceState interface{ isVoiceState() }

type (
	// voiceDisconnected holds and owes no connection. A queue may be retained.
	voiceDisconnected struct{}
	// voiceConnecting runs an explicit join's Connect; reply acknowledges it.
	voiceConnecting struct {
		dial
		reply chan response
	}
	// voiceConnected owns c.output.
	voiceConnected struct{}
	// voiceLost: the connection was lost (moved away or transport lost)
	// while an attempt was running. What follows is decided once the
	// attempt joins; release records a stop or disconnect requested in the
	// meantime.
	voiceLost struct{ release bool }
	// voiceBackoff waits to spend the next recovery attempt.
	voiceBackoff struct{ timer *time.Timer }
	// voiceReconnecting runs a recovery Connect.
	voiceReconnecting struct{ dial }
	// voiceReleasing owes a Disconnect. It starts once no attempt runs and
	// the abandoned (already canceled) voiceConnecting or voiceReconnecting,
	// if any, has reported.
	voiceReleasing struct{ abandoned voiceState }
	// voiceDisconnecting runs Disconnect.
	voiceDisconnecting struct{ cancel context.CancelFunc }
)

func (voiceDisconnected) isVoiceState()  {}
func (voiceConnecting) isVoiceState()    {}
func (voiceConnected) isVoiceState()     {}
func (voiceLost) isVoiceState()          {}
func (voiceBackoff) isVoiceState()       {}
func (voiceReconnecting) isVoiceState()  {}
func (voiceReleasing) isVoiceState()     {}
func (voiceDisconnecting) isVoiceState() {}

// dial is an in-flight Connect. A recover tears down the old connection
// first, which Discord reports as "" before the new channel, so observations
// are not judged while dialing: only the newest one is kept and reconciled
// against the outcome.
type dial struct {
	cancel      context.CancelFunc
	observed    string
	observedSet bool
	// lost: a transport loss arrived during the dial. The new connection
	// can drop after Connect finishes but before its result is applied, so
	// a successful result is treated as lost.
	lost bool
}

type voiceEventKind uint8

const (
	// voiceJoin is an explicit join to channel, acknowledged on reply.
	voiceJoin voiceEventKind = iota + 1
	// voiceAttach supplies an unmanaged fixture output for channel.
	voiceAttach
	// voiceTransportDone is the result of the in-flight Connect/Disconnect.
	voiceTransportDone
	// voiceObserved is the bot's own observed channel ("" when removed).
	voiceObserved
	// voiceTransportLost: the adapter lost the voice transport without the
	// bot being removed from its channel.
	voiceTransportLost
	// voiceIdleTimeout: paused or alone for the configured timeout (reason).
	voiceIdleTimeout
	// voiceTransportFailed: the attempt ended in a transport failure (output
	// failure or an observed loss); spend the recovery budget.
	voiceTransportFailed
	// voiceAttemptJoined: the attempt has joined; resolve voiceLost. cause is
	// the control that was pending when the attempt was interrupted.
	voiceAttemptJoined
	// voiceStop releases anything but a healthy established connection.
	voiceStop
	// voiceDisconnect leaves voice: an explicit Disconnect, a drained queue,
	// or a stop that found broken transport.
	voiceDisconnect
	// voiceQuiescent: no attempt is running, so an owed Disconnect may start.
	voiceQuiescent
	// voiceRetry is the backoff timer firing.
	voiceRetry
	// voiceShutdown joins in-flight transport and disconnects for Close.
	voiceShutdown
)

type voiceEvent struct {
	kind    voiceEventKind
	channel string
	reply   chan response
	output  playback.Output
	result  transportResult
	cause   playback.Outcome
	reason  string
}

// voiceEvent is the single transition point: it is the only code that
// assigns c.link. Each handler computes the next state from the current one
// and does not assign. Playback side effects of a voice transition
// (interrupting the attempt on a move or removal, superseding lookups) happen
// in the handlers. The attempt's pending cause stays owned by playback code.
func (c *coordinator) voiceEvent(e voiceEvent) {
	switch e.kind {
	case voiceJoin:
		c.link = c.onJoin(e.channel, e.reply)
	case voiceAttach:
		c.output, c.channel = e.output, e.channel
		c.link = voiceConnected{}
	case voiceTransportDone:
		next, dialed := c.onTransportDone(e.result)
		c.link = next
		var d dial
		var reply chan response
		switch s := dialed.(type) {
		case voiceConnecting:
			d, reply = s.dial, s.reply
		case voiceReconnecting:
			d = s.dial
		default:
			return
		}
		if d.observedSet {
			// Moved or removed while connecting: judge the newest observation
			// against the connection just established, before acknowledging.
			c.voiceEvent(voiceEvent{kind: voiceObserved, channel: d.observed})
		}
		if d.lost {
			c.voiceEvent(voiceEvent{kind: voiceTransportLost})
		}
		if reply != nil {
			if c.connected() {
				reply <- response{snapshot: c.snapshot()}
			} else {
				reply <- response{err: ErrSuperseded}
			}
		}
	case voiceObserved:
		c.link = c.onObserved(e.channel)
	case voiceTransportLost:
		c.link = c.onTransportLost()
	case voiceIdleTimeout:
		c.link = c.left(e.reason)
	case voiceTransportFailed:
		c.link = c.onTransportFailed()
	case voiceAttemptJoined:
		c.link = c.onAttemptJoined(e.cause)
	case voiceStop:
		if !c.connected() {
			c.link = c.released(c.link)
		}
	case voiceDisconnect:
		c.link = c.released(c.link)
	case voiceQuiescent:
		if s, ok := c.link.(voiceReleasing); ok && s.abandoned == nil && c.active == nil {
			c.link = voiceDisconnecting{cancel: c.launchTransport(transportDisconnect)}
		}
	case voiceRetry:
		c.link = c.onRetry()
	case voiceShutdown:
		c.link = c.onShutdown()
	}
}
func (c *coordinator) onJoin(channel string, reply chan response) voiceState {
	if c.voice == nil {
		reply <- response{err: errors.New("managed voice unavailable")}
		return c.link
	}
	if c.connected() {
		if c.channel != channel {
			reply <- response{err: ErrVoiceAccess}
		} else {
			reply <- response{snapshot: c.snapshot()}
		}
		return c.link
	}
	if _, idle := c.link.(voiceDisconnected); !idle || c.active != nil {
		reply <- response{err: ErrBusy}
		return c.link
	}
	c.channel = channel
	if c.queue.current() != nil && !c.acquireStream() {
		reply <- response{err: ErrStreamLimit}
		return c.link
	}
	c.recoveryAttempts = 0
	c.drained = false
	return voiceConnecting{dial: dial{cancel: c.launchTransport(transportConnect)}, reply: reply}
}

// onTransportDone also returns the Connecting/Reconnecting state whose
// Connect just succeeded, so voiceEvent can reconcile its observation and
// acknowledge the join once Connected is current.
func (c *coordinator) onTransportDone(result transportResult) (next, dialed voiceState) {
	var (
		d     dial
		reply chan response
		kind  = transportRecover
	)
	switch s := c.link.(type) {
	case voiceDisconnecting:
		s.cancel()
		c.output = nil
		clear(c.speakers)
		if result.err != nil {
			c.logVoiceFailure("Voice cleanup failed", "voice.cleanup", result.err, string(transportDisconnect))
		}
		return voiceDisconnected{}, nil
	case voiceReleasing:
		// A stop or disconnect abandoned this connect; late success is
		// discarded and the owed Disconnect closes it.
		if j, ok := s.abandoned.(voiceConnecting); ok && j.reply != nil {
			j.reply <- response{err: ErrSuperseded}
		}
		return voiceReleasing{}, nil
	case voiceConnecting:
		d, reply, kind = s.dial, s.reply, transportConnect
	case voiceReconnecting:
		d = s.dial
	default:
		return c.link, nil
	}
	d.cancel()
	if c.session.ctx.Err() != nil {
		if reply != nil {
			reply <- response{err: ErrSuperseded}
		}
		return voiceDisconnected{}, nil
	}
	if result.err == nil && result.output == nil {
		result.err = errors.New("voice adapter returned no output")
	}
	if result.err != nil {
		if reply != nil {
			reply <- response{err: failure.Wrap("voice_join", failure.Transport, result.err)}
		}
		c.logVoiceFailure("Voice connection failed", "voice.connection", result.err, string(kind))
		if d.observedSet && d.observed != "" {
			c.channel = d.observed
		}
		if kind != transportRecover {
			return c.releasing(), nil
		}
		if d.observedSet && d.observed == "" {
			// The adapter withholds its own leaves (teardown, failure
			// cleanup, lost socket), so "" is a removal by someone else and
			// the remaining retries are not spent.
			return c.leave(c.releasing(), "removed"), nil
		}
		return c.recoverNext(), nil
	}
	dialed = c.link
	c.output = result.output
	// Speaking events from the replaced connection may have arrived after
	// the loss cleared them; the new connection reports fresh state.
	c.forgetSpeakers()
	c.logger.Info("Voice connected", "event", "voice.connected", "session_id", c.id, "recovery_attempt", c.recoveryAttempts)
	return voiceConnected{}, dialed
}

// onObserved judges the bot's own observed channel ("" when removed).
func (c *coordinator) onObserved(channel string) voiceState {
	switch s := c.link.(type) {
	case voiceConnecting:
		s.observed, s.observedSet = channel, true
		return s
	case voiceReconnecting:
		s.observed, s.observedSet = channel, true
		return s
	case voiceBackoff, voiceLost:
		if channel == "" {
			return c.left("removed")
		}
		// Recovery is owed: target the newest observed channel.
		c.channel = channel
	case voiceConnected:
		if channel == c.channel {
			return s
		}
		if channel == "" {
			return c.left("removed")
		}
		c.channel = channel
		return c.connectionLost()
	}
	return c.link
}

// onTransportLost handles a voice transport lost without a removal. A dial
// records it and applies it if its Connect succeeds; the other non-connected
// states already recover or release.
func (c *coordinator) onTransportLost() voiceState {
	switch s := c.link.(type) {
	case voiceConnecting:
		s.lost = true
		return s
	case voiceReconnecting:
		s.lost = true
		return s
	case voiceConnected:
	default:
		return c.link
	}
	next := c.connectionLost()
	if c.voice == nil {
		return next
	}
	decision := "retry"
	switch next.(type) {
	case voiceLost:
		decision = "after_attempt"
	case voiceReleasing:
		decision = "declined"
	}
	c.logger.Warn("Voice transport lost", "event", "session.recovery", "session_id", c.id, "reason", "transport_lost", "recovery", decision)
	return next
}

// connectionLost: the established connection is gone (moved away or
// transport lost) but the bot was not removed, so queued work recovers.
func (c *coordinator) connectionLost() voiceState {
	c.forgetSpeakers()
	if c.active != nil {
		// A pending control keeps its outcome; recovery is decided once
		// the attempt joins (voiceAttemptJoined).
		c.interrupt(playback.TransportFailure)
		return voiceLost{}
	}
	// Voice recovery exists to resume queued work; an idle bot whose
	// connection moved or dropped releases it instead of following.
	c.generation++
	if c.queue.current() == nil {
		return c.releasing()
	}
	return c.recoverNext()
}

// left handles a deliberate end of the connection that no user command
// requested: removal by someone else (own channel observed as "") or an idle
// timeout. Unlike a move or a network failure there is no recovery: the
// connection is released and the queue keeps the current request's resume
// position, exactly as after an explicit Disconnect. A control already
// pending keeps its outcome.
func (c *coordinator) left(reason string) voiceState {
	return c.leave(c.released(c.link), reason)
}

// leave applies left's effects given the released next state.
func (c *coordinator) leave(next voiceState, reason string) voiceState {
	if c.voice != nil {
		c.logger.Info("Left voice; recovery declined, queue retained", "event", "session.recovery", "session_id", c.id, "recovery", "declined", "reason", reason)
	}
	if _, lost := next.(voiceLost); lost {
		// The removal, not the earlier move, now decides the outcome.
		next = c.releasing()
	}
	c.generation++
	c.forgetSpeakers()
	if c.active != nil && c.pending == playback.TransportFailure {
		// Cancellation for the move is already in flight.
		c.pending = playback.Disconnected
	}
	c.interrupt(playback.Disconnected)
	return next
}

// onTransportFailed: the attempt ended in a transport failure (output failure
// or an observed loss); spend the recovery budget.
func (c *coordinator) onTransportFailed() voiceState {
	switch s := c.link.(type) {
	case voiceLost:
		if s.release {
			return c.releasing()
		}
	case voiceReleasing, voiceDisconnecting:
		return s
	}
	return c.recoverNext()
}

// onAttemptJoined resolves voiceLost once its attempt has joined. cause is
// the control that was pending when the attempt was interrupted.
func (c *coordinator) onAttemptJoined(cause playback.Outcome) voiceState {
	s, ok := c.link.(voiceLost)
	if !ok {
		return c.link
	}
	if s.release {
		return c.releasing()
	}
	// The loss arrived during another control, which kept its outcome, so
	// lookups are superseded here. When the loss itself interrupted the
	// attempt, complete already superseded them.
	interrupted := cause != "" && cause != playback.TransportFailure
	if interrupted {
		c.generation++
	}
	switch {
	case c.queue.current() != nil:
		return c.recoverNext()
	case interrupted:
		return c.releasing()
	default:
		// The queue drain releases the connection (driveTransport).
		return voiceDisconnected{}
	}
}

func (c *coordinator) onRetry() voiceState {
	if _, ok := c.link.(voiceBackoff); !ok {
		return c.link
	}
	if c.active != nil || c.session.ctx.Err() != nil {
		return voiceDisconnected{}
	}
	c.recoveryAttempts++
	c.logger.Warn("Reconnecting voice; current request retained", "event", "session.recovery", "session_id", c.id, "request_id", c.last.Request.ID, "attempt_id", c.last.Request.AttemptID, "recovery", "retry", "attempt", c.recoveryAttempts)
	return voiceReconnecting{dial: dial{cancel: c.launchTransport(transportRecover)}}
}

// onShutdown cancels and joins in-flight transport, rejects a pending join
// and disconnects for Close.
func (c *coordinator) onShutdown() voiceState {
	if c.voice == nil {
		return c.link
	}
	var reply chan response
	switch s := c.link.(type) {
	case voiceBackoff:
		s.timer.Stop()
	case voiceConnecting:
		s.cancel()
		<-c.transportResults
		reply = s.reply
	case voiceReconnecting:
		s.cancel()
		<-c.transportResults
	case voiceDisconnecting:
		s.cancel()
		<-c.transportResults
	case voiceReleasing:
		if s.abandoned != nil {
			<-c.transportResults
		}
		if j, ok := s.abandoned.(voiceConnecting); ok {
			reply = j.reply
		}
	}
	if reply != nil {
		reply <- response{err: ErrClosed}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.session.ctx), 3*time.Second)
	defer cancel()
	if err := c.voice.Disconnect(ctx); err != nil {
		c.logVoiceFailure("Voice shutdown cleanup failed", "voice.cleanup", err, "shutdown")
	}
	return voiceDisconnected{}
}

// released is the state after leaving voice from s. It does not assign.
func (c *coordinator) released(s voiceState) voiceState {
	if c.voice == nil {
		// Unmanaged output has nothing to release; a loss still resolves
		// when its attempt joins.
		if _, lost := s.(voiceLost); lost {
			return s
		}
		return voiceDisconnected{}
	}
	switch s := s.(type) {
	case voiceBackoff:
		s.timer.Stop()
	case voiceConnecting:
		s.cancel()
		return voiceReleasing{abandoned: s}
	case voiceReconnecting:
		s.cancel()
		return voiceReleasing{abandoned: s}
	case voiceLost:
		return voiceLost{release: true}
	case voiceReleasing, voiceDisconnecting:
		// Already leaving; cutting a Disconnect short only to repeat it is waste.
		return s
	}
	return voiceReleasing{}
}

// releasing owes a Disconnect when nothing is in flight.
func (c *coordinator) releasing() voiceState {
	if c.voice == nil {
		return voiceDisconnected{}
	}
	return voiceReleasing{}
}

// recoverNext spends the per-request recovery budget: three attempts with
// 0/1/2 s backoff. The budget resets only when the queue advances or on an
// explicit join, never after a successful reconnect, so a flapping voice
// connection cannot loop one track indefinitely. It does not assign.
func (c *coordinator) recoverNext() voiceState {
	if c.voice == nil || c.session.ctx.Err() != nil {
		return voiceDisconnected{}
	}
	if c.recoveryAttempts >= 3 {
		c.logger.Error("Voice recovery exhausted; queue retained", "event", "session.recovery", "session_id", c.id, "request_id", c.last.Request.ID, "attempt_id", c.last.Request.AttemptID, "recovery", "exhausted", "attempts", c.recoveryAttempts)
		return voiceReleasing{}
	}
	return voiceBackoff{timer: time.NewTimer(time.Duration(c.recoveryAttempts) * time.Second)}
}

func (c *coordinator) connected() bool {
	_, ok := c.link.(voiceConnected)
	return ok
}

// dialing reports a Connect still in flight, including one a release
// abandoned that has not reported yet, and whether it is a recovery.
func (c *coordinator) dialing() (inFlight, recovery bool) {
	s := c.link
	if r, ok := s.(voiceReleasing); ok {
		s = r.abandoned
	}
	switch s.(type) {
	case voiceConnecting:
		return true, false
	case voiceReconnecting:
		return true, true
	}
	return false, false
}

// holdsChannel reports that the session is connected to, joining, or
// recovering its channel: channel members may control it and its stream
// lease is kept.
func (c *coordinator) holdsChannel() bool {
	_, backoff := c.link.(voiceBackoff)
	inFlight, _ := c.dialing()
	return c.connected() || backoff || inFlight
}

// voiceBusy reports transport work in progress or owed.
func (c *coordinator) voiceBusy() bool {
	switch c.link.(type) {
	case voiceConnecting, voiceReconnecting, voiceBackoff, voiceReleasing, voiceDisconnecting:
		return true
	}
	return false
}

// owesDisconnect reports that a stop or disconnect is waiting to leave voice.
func (c *coordinator) owesDisconnect() bool {
	switch s := c.link.(type) {
	case voiceReleasing:
		return true
	case voiceLost:
		return s.release
	}
	return false
}

func (c *coordinator) retryTimer() <-chan time.Time {
	if s, ok := c.link.(voiceBackoff); ok {
		return s.timer.C
	}
	return nil
}
