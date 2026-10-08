package session

import (
	"errors"
	"math"
	"time"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

func (c *coordinator) apply(op Operation) error {
	if c.pending != "" && op.Action != Inspect && op.Action != Stop {
		return ErrBusy
	}
	switch op.Action {
	case Inspect:
		return nil
	case Pause, Resume:
		if c.queue.current() == nil {
			return errors.New("nothing is playing")
		}
		c.paused = op.Action == Pause
		if c.controls != nil {
			c.controls.SetPaused(c.paused)
		}
	case Skip:
		if c.active != nil {
			c.interrupt(playback.Skipped)
		} else if e := c.queue.current(); e != nil {
			c.settle(e, playback.Result{Request: e.ticket.request, Outcome: playback.Skipped})
			c.queue.advance()
		}
	case Stop:
		c.connected = false
		if c.active != nil {
			c.stopAfter = true
			c.interrupt(playback.Stopped)
		} else {
			c.settleAll(playback.Stopped)
			c.queue.reset()
			c.paused = false
		}
	case Disconnect:
		c.connected = false
		c.interrupt(playback.Disconnected)
	case Connect:
		if c.active != nil {
			return errors.New("cannot replace active output")
		}
		if op.Output == nil {
			return errors.New("output required")
		}
		c.output = op.Output
		c.connected = true
	case Clear:
		c.clearUpcoming()
	case Shuffle:
		return c.queue.shuffle()
	case Loop:
		c.loop = !c.loop
	case Remove:
		return c.remove(op.Position)
	case Move:
		return c.queue.move(op.Position, op.To)
	case Seek:
		return c.seek(op.Offset)
	case Volume, DuckTarget, Ducking, Speaking:
		return c.configure(op)
	default:
		return errors.New("unknown session operation")
	}
	return nil
}

func (c *coordinator) configure(op Operation) error {
	switch op.Action {
	case Volume, DuckTarget:
		if op.Position < 0 || op.Position > 100 {
			return errors.New("volume must be between 0 and 100")
		}
		if op.Action == Volume {
			c.volume = op.Position
		} else {
			c.duckTarget = op.Position
		}
		c.updateVolume()
	case Ducking:
		c.ducking = op.Enabled
		c.updateVolume()
	case Speaking:
		if op.Speaker == "" || len(op.Speaker) > 32 {
			return errors.New("invalid speaker")
		}
		if op.Enabled {
			if !c.speakers[op.Speaker] && len(c.speakers) >= 256 {
				return ErrBusy
			}
			c.speakers[op.Speaker] = true
		} else {
			delete(c.speakers, op.Speaker)
		}
		c.updateVolume()
	}
	return nil
}

func (c *coordinator) clearUpcoming() {
	current := c.queue.current()
	for i := range c.queue.entries {
		e := &c.queue.entries[i]
		if e != current {
			c.settle(e, playback.Result{Request: e.ticket.request, Outcome: playback.Removed})
		}
	}
	if current == nil {
		c.queue.reset()
		return
	}
	retained := *current
	c.queue.reset()
	c.queue.entries = append(c.queue.entries, retained)
	c.queue.index = 0
}

func (c *coordinator) remove(position int) error {
	if position < 0 || position >= len(c.queue.entries) {
		return errors.New("invalid queue position")
	}
	if position == c.queue.index && c.active != nil {
		c.interrupt(playback.Removed)
		return nil
	}
	e := &c.queue.entries[position]
	c.settle(e, playback.Result{Request: e.ticket.request, Outcome: playback.Removed})
	c.queue.remove(position)
	if c.queue.index >= len(c.queue.entries) {
		c.queue.reset()
	}
	return nil
}

func (c *coordinator) seek(offset time.Duration) error {
	e := c.queue.current()
	if e == nil {
		return errors.New("nothing is playing")
	}
	if e.track.Live || offset < 0 || e.track.Duration <= 0 || offset >= e.track.Duration {
		return errors.New("invalid seek position or unseekable track")
	}
	e.ticket.request.Offset = offset
	c.interrupt(playback.Seeked)
	return nil
}

func (c *coordinator) effectiveVolume() int {
	if c.ducking && len(c.speakers) > 0 {
		return min(c.volume, c.duckTarget)
	}
	return c.volume
}

func (c *coordinator) updateVolume() {
	if c.controls != nil {
		if err := c.controls.SetVolume(c.effectiveVolume()); err != nil {
			// configure validates every setting before mutation.
			panic("session volume invariant violated")
		}
	}
}

func timeForFrames(frames uint64) time.Duration {
	if frames > uint64(math.MaxInt64/playback.FrameDuration) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(frames) * playback.FrameDuration
}
