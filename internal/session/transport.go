package session

import (
	"context"
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
	c.voiceEvent(voiceEvent{kind: voiceObserved, channel: channel})
}

func (c *coordinator) resolution(cmd command) {
	if cmd.release != "" {
		generation, exists := c.reservations[cmd.release]
		delete(c.reservations, cmd.release)
		// Superseded or repeated cleanup has no authority to end the voice
		// connection retained by a later Stop.
		if exists && generation == c.generation && c.queue.current() == nil && c.connected() {
			c.drained = true
		}
		cmd.reply <- response{}
		return
	}
	if !c.connected() || cmd.access == nil || c.channel == "" || cmd.access.Channel != c.channel {
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
		c.voiceEvent(voiceEvent{kind: voiceDisconnect})
		c.drained = false
		c.dirty = true
	}
	c.voiceEvent(voiceEvent{kind: voiceQuiescent})
}

type transportKind string

const (
	transportConnect    transportKind = "connect"
	transportRecover    transportKind = "recover"
	transportDisconnect transportKind = "disconnect"
)

// launchTransport starts the one Connect/Disconnect the coordinator allows in
// flight. Its result arrives on transportResults; the returned cancel belongs
// to the voice state that records the operation.
func (c *coordinator) launchTransport(kind transportKind) context.CancelFunc {
	parent := c.session.ctx
	timeout := 12 * time.Second
	if kind == transportDisconnect {
		parent = context.WithoutCancel(parent)
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	c.dirty = true
	channel := c.channel
	go func() {
		defer cancel()
		var result transportResult
		if kind == transportDisconnect {
			result.err = c.voice.Disconnect(ctx)
		} else {
			result.output, result.err = c.voice.Connect(ctx, channel)
		}
		c.transportResults <- result
	}()
	return cancel
}

func (c *coordinator) logVoiceFailure(message, event string, err error, kind string) {
	f := failure.Wrap("voice", failure.Transport, err)
	c.logger.Warn(message, "event", event, "session_id", c.id, "request_id", c.last.Request.ID, "attempt_id", c.last.Request.AttemptID, "category", f.Category, "reason", f.Reason, "guidance", f.Guidance(), "fingerprint", f.Fingerprint, "attempt", c.recoveryAttempts, "recovery", kind)
}
