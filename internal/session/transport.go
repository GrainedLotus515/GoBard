package session

import (
	"context"
	"errors"
	"time"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

// Voice is bound to one guild by the adapter. Connect replaces any prior
// connection and owns its returned Output. Calls are serialized, cancellable,
// and made only after the previous playback attempt joins. Disconnect is
// idempotent and closes/joins transport resources even after a failed Connect.
// Typed nil implementations are prohibited by contract.
type Voice interface {
	Connect(context.Context, string) (playback.Output, error)
	Disconnect(context.Context) error
}

type transportResult struct {
	output playback.Output
	err    error
}

// voiceLost applies the bot's own observed channel ("" when removed). Callers
// validate the channel length at the observation boundary.
func (c *coordinator) voiceLost(channel string) {
	if !c.connected {
		switch {
		case c.transportKind == "connect" || c.transportKind == "recover":
			// A recover tears down the old connection first, which Discord
			// reports as "" before the new channel, so "" is judged only
			// against the outcome (see transportComplete).
			// Reconcile after the in-flight connection reports; a newer
			// observation must not be overwritten by its success.
			c.connectObserved, c.connectObservedSet = channel, true
		case c.retry != nil || c.reconnectAfter || c.active != nil && c.pending == playback.TransportFailure:
			if channel == "" {
				c.leaveVoice("removed")
			} else {
				// Recovery is pending: target the newest observed channel.
				c.channel = channel
			}
		}
		return
	}
	if channel == c.channel {
		return
	}
	if channel == "" {
		c.leaveVoice("removed")
		return
	}
	c.channel = channel
	c.connected = false
	c.forgetSpeakers()
	if c.active != nil {
		if c.pending != "" {
			// The pending control keeps its outcome; recovery runs once the
			// attempt joins (see complete).
			c.reconnectAfter = true
		}
		c.interrupt(playback.TransportFailure)
	} else {
		c.recoverLost()
	}
}

// leaveVoice handles a deliberate end of the voice connection that no user
// command requested: removal by someone else (own channel observed as ""),
// or an idle timeout. Unlike a move or a network failure, no recovery is
// attempted; the connection is released and the queue is kept with the
// current request's resume position, exactly as an explicit Disconnect. A
// control already pending keeps its outcome.
func (c *coordinator) leaveVoice(reason string) {
	if c.voice != nil {
		c.logger.Info("Left voice; recovery declined, queue retained", "event", "session.recovery", "session_id", c.id, "recovery", "declined", "reason", reason)
	}
	c.reconnectAfter = false
	c.disconnectVoice()
	c.generation++
	c.connected = false
	c.forgetSpeakers()
	if c.active != nil && c.pending == playback.TransportFailure {
		// The connection was already lost to a move; the removal decides the
		// outcome. Cancellation is already in flight.
		c.pending = playback.Disconnected
	}
	c.interrupt(playback.Disconnected)
}

// recoverLost handles a lost connection once no attempt is running. Voice
// recovery exists to resume queued work; with nothing queued (after /stop,
// or a kick during a join) the dead connection is released instead of
// rejoining an idle channel. Either way in-flight resolutions are superseded.
func (c *coordinator) recoverLost() {
	c.generation++
	if c.queue.current() == nil {
		c.disconnectVoice()
		return
	}
	c.scheduleRecovery()
}

func (c *coordinator) join(cmd command) {
	if c.voice == nil {
		cmd.reply <- response{err: errors.New("managed voice unavailable")}
		return
	}
	if c.connected {
		if c.channel != cmd.joinChannel {
			cmd.reply <- response{err: ErrVoiceAccess}
		} else {
			cmd.reply <- response{snapshot: c.snapshot()}
		}
		return
	}
	if c.transportCancel != nil || c.active != nil || c.retry != nil || c.needDisconnect {
		cmd.reply <- response{err: ErrBusy}
		return
	}
	c.channel = cmd.joinChannel
	if c.queue.current() != nil && !c.acquireStream() {
		cmd.reply <- response{err: ErrStreamLimit}
		return
	}
	c.joinReply = cmd.reply
	c.recoveryAttempts = 0
	c.drained = false
	c.launchTransport("connect")
}

func (c *coordinator) resolution(cmd command) {
	if cmd.release != "" {
		generation, exists := c.reservations[cmd.release]
		delete(c.reservations, cmd.release)
		// Superseded or repeated cleanup has no authority to end the voice
		// connection retained by a later Stop.
		if exists && generation == c.generation && c.queue.current() == nil && c.connected {
			c.drained = true
		}
		cmd.reply <- response{}
		return
	}
	if !c.connected || cmd.access == nil || c.channel == "" || cmd.access.Channel != c.channel {
		cmd.reply <- response{err: ErrVoiceAccess}
		return
	}
	if _, exists := c.reservations[cmd.reserve]; exists || len(c.reservations) >= 16 {
		cmd.reply <- response{err: ErrBusy}
		return
	}
	c.reservations[cmd.reserve] = c.generation
	cmd.reply <- response{snapshot: c.snapshot()}
}

func (c *coordinator) driveTransport() {
	if c.voice == nil || c.session.ctx.Err() != nil {
		return
	}
	if c.drained && len(c.reservations) == 0 && c.queue.current() == nil {
		c.disconnectVoice()
		c.connected = false
		c.drained = false
		c.dirty = true
	}
	if c.needDisconnect && c.active == nil && c.transportCancel == nil {
		c.needDisconnect = false
		c.launchTransport("disconnect")
	}
}

func (c *coordinator) launchTransport(kind string) {
	parent := c.session.ctx
	timeout := 12 * time.Second
	if kind == "disconnect" {
		parent = context.WithoutCancel(parent)
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	c.transportCancel, c.transportKind = cancel, kind
	c.connectObservedSet = false
	c.dirty = true
	channel := c.channel
	go func() {
		defer cancel()
		var result transportResult
		if kind == "disconnect" {
			result.err = c.voice.Disconnect(ctx)
		} else {
			result.output, result.err = c.voice.Connect(ctx, channel)
		}
		c.transportResults <- result
	}()
}

func (c *coordinator) transportComplete(result transportResult) {
	kind := c.transportKind
	c.transportCancel()
	c.transportCancel, c.transportKind = nil, ""
	if kind == "disconnect" {
		c.output = nil
		clear(c.speakers)
		if result.err != nil {
			c.logVoiceFailure("Voice cleanup failed", "voice.cleanup", result.err, kind)
		}
		return
	}
	if c.needDisconnect || c.session.ctx.Err() != nil {
		if c.joinReply != nil {
			c.joinReply <- response{err: ErrSuperseded}
			c.joinReply = nil
		}
		return
	}
	if result.err == nil && result.output == nil {
		result.err = errors.New("voice adapter returned no output")
	}
	if result.err != nil {
		if c.joinReply != nil {
			c.joinReply <- response{err: failure.Wrap("voice_join", failure.Transport, result.err)}
			c.joinReply = nil
		}
		c.logVoiceFailure("Voice connection failed", "voice.connection", result.err, kind)
		if c.connectObservedSet && c.connectObserved != "" {
			c.channel = c.connectObserved
		}
		c.connectObservedSet = false
		if kind == "recover" {
			c.scheduleRecovery()
		} else {
			c.needDisconnect = true
		}
		return
	}
	c.output, c.connected = result.output, true
	// Speaking events from the replaced connection may have arrived after
	// the loss cleared them; the new connection reports fresh state.
	c.forgetSpeakers()
	c.logger.Info("Voice connected", "event", "voice.connected", "session_id", c.id, "recovery_attempt", c.recoveryAttempts)
	if c.connectObservedSet {
		// The bot was moved or disconnected while connecting. Treat the newer
		// observation as a loss of the connection just established, before
		// acknowledging the join.
		c.connectObservedSet = false
		c.voiceLost(c.connectObserved)
	}
	if c.joinReply != nil {
		if c.connected {
			c.joinReply <- response{snapshot: c.snapshot()}
		} else {
			c.joinReply <- response{err: ErrSuperseded}
		}
		c.joinReply = nil
	}
}

func (c *coordinator) disconnectVoice() {
	if c.voice == nil {
		return
	}
	if c.retryTimer != nil {
		c.retryTimer.Stop()
		c.retry = nil
	}
	if c.transportKind == "disconnect" {
		// Already tearing down; cutting that short only to repeat it is waste.
		return
	}
	c.needDisconnect = true
	if c.transportCancel != nil {
		c.transportCancel()
	}
}

// scheduleRecovery spends the per-request recovery budget. The budget resets
// only when the queue advances or on an explicit join, never after a
// successful reconnect, so a flapping voice connection cannot loop one track
// indefinitely.
func (c *coordinator) scheduleRecovery() {
	if c.voice == nil || c.needDisconnect || c.session.ctx.Err() != nil {
		return
	}
	if c.recoveryAttempts >= 3 {
		c.needDisconnect = true
		c.logger.Error("Voice recovery exhausted; queue retained", "event", "session.recovery", "session_id", c.id, "request_id", c.last.Request.ID, "attempt_id", c.last.Request.AttemptID, "recovery", "exhausted", "attempts", c.recoveryAttempts)
		return
	}
	delay := time.Duration(c.recoveryAttempts) * time.Second
	if c.retryTimer == nil {
		c.retryTimer = time.NewTimer(delay)
	} else {
		c.retryTimer.Reset(delay)
	}
	c.retry = c.retryTimer.C
}

func (c *coordinator) startRecovery() {
	if c.active != nil || c.needDisconnect || c.transportCancel != nil || c.session.ctx.Err() != nil {
		return
	}
	c.recoveryAttempts++
	c.logger.Warn("Reconnecting voice; current request retained", "event", "session.recovery", "session_id", c.id, "request_id", c.last.Request.ID, "attempt_id", c.last.Request.AttemptID, "recovery", "retry", "attempt", c.recoveryAttempts)
	c.launchTransport("recover")
}

func (c *coordinator) closeVoice() {
	if c.voice == nil {
		return
	}
	if c.retryTimer != nil {
		c.retryTimer.Stop()
	}
	if c.transportCancel != nil {
		c.transportCancel()
		<-c.transportResults
	}
	if c.joinReply != nil {
		c.joinReply <- response{err: ErrClosed}
		c.joinReply = nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.session.ctx), 3*time.Second)
	defer cancel()
	if err := c.voice.Disconnect(ctx); err != nil {
		c.logVoiceFailure("Voice shutdown cleanup failed", "voice.cleanup", err, "shutdown")
	}
}

func (c *coordinator) logVoiceFailure(message, event string, err error, kind string) {
	f := failure.Wrap("voice", failure.Transport, err)
	c.logger.Warn(message, "event", event, "session_id", c.id, "request_id", c.last.Request.ID, "attempt_id", c.last.Request.AttemptID, "category", f.Category, "reason", f.Reason, "guidance", f.Guidance(), "fingerprint", f.Fingerprint, "attempt", c.recoveryAttempts, "recovery", kind)
}
