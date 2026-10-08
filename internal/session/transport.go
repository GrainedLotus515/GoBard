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
	c.joinReply = cmd.reply
	c.recoveryAttempts = 0
	c.drained = false
	c.launchTransport("connect")
}

func (c *coordinator) resolution(cmd command) {
	if cmd.release != "" {
		delete(c.reservations, cmd.release)
		if c.queue.current() == nil && c.connected {
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
	c.reservations[cmd.reserve] = struct{}{}
	cmd.reply <- response{snapshot: c.snapshot()}
}

func (c *coordinator) driveTransport() {
	if c.voice == nil || c.session.ctx.Err() != nil {
		return
	}
	if c.drained && len(c.reservations) == 0 && c.queue.current() == nil {
		c.needDisconnect = true
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
			c.logger.Warn("Voice cleanup failed", "event", "voice.cleanup", "session_id", c.id, "category", failure.Transport)
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
		c.logger.Warn("Voice connection failed", "event", "voice.connection", "session_id", c.id, "category", failure.Transport, "attempt", c.recoveryAttempts, "recovery", kind)
		if kind == "recover" {
			c.scheduleRecovery()
		} else {
			c.needDisconnect = true
		}
		return
	}
	c.output, c.connected = result.output, true
	c.logger.Info("Voice connected", "event", "voice.connected", "session_id", c.id, "recovery_attempt", c.recoveryAttempts)
	if c.joinReply != nil {
		c.joinReply <- response{snapshot: c.snapshot()}
		c.joinReply = nil
	}
}

func (c *coordinator) disconnectVoice() {
	if c.voice == nil {
		return
	}
	c.needDisconnect = true
	if c.retryTimer != nil {
		c.retryTimer.Stop()
		c.retry = nil
	}
	if c.transportCancel != nil {
		c.transportCancel()
	}
}

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
	if c.active != nil || c.needDisconnect || c.session.ctx.Err() != nil {
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
		c.logger.Warn("Voice shutdown cleanup failed", "event", "voice.cleanup", "session_id", c.id, "category", failure.Transport)
	}
}
