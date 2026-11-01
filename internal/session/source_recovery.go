package session

import (
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

// A known transient network failure after delivered audio permits one resume
// per logical request. Authentication, empty streams, unknown failures and live
// media never retry. The existing opener remains cache-first; no provider swap
// or hidden lower-layer retry is introduced. Every attempt keeps its own result.
func (c *coordinator) resumeSource(e *entry, result playback.Result) bool {
	if e == nil || e.sourceRetries >= 1 || e.track.Live || e.track.Duration <= 0 || result.Frames == 0 || result.Failure == nil || c.session.ctx.Err() != nil {
		return false
	}
	if result.Failure.Category != failure.Source {
		return false
	}
	switch result.Failure.Reason {
	case failure.ConnectionReset, failure.NetworkTimeout:
	default:
		return false
	}
	remaining := e.track.Duration - e.ticket.request.Offset
	delivered := timeForFrames(result.Frames)
	if remaining <= 0 || delivered >= remaining {
		return false
	}
	e.sourceRetries++
	e.ticket.request.Offset += delivered
	c.recovery = "source_resume"
	if c.logger != nil {
		c.logger.Warn("Media connection interrupted; resuming once from delivered position", "event", "session.recovery", "session_id", c.id, "request_id", result.Request.ID, "attempt_id", result.Request.AttemptID, "recovery", c.recovery, "reason", result.Failure.Reason, "offset_ms", e.ticket.request.Offset.Milliseconds(), "attempts", e.sourceRetries)
	}
	return true
}
