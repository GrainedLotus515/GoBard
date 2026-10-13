package bot

import (
	"time"

	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

// One observer per admitted guild coalesces player updates. Production
// terminal accounting remains in Engine; slow Discord responses cannot hold up
// queue transitions. No per-attempt goroutine or unbounded notification history.
func (r *Runtime) observe(guild string, target *session.Session) {
	last := ""
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var previous uint64
	delivering := false
	defer func() {
		if delivering {
			r.delivering.Add(-1)
		}
	}()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-target.Done():
			return
		case <-target.Changes():
		case <-ticker.C:
		}
		observed := time.Now()
		snapshot, err := target.Apply(r.ctx, session.Operation{Action: session.Inspect})
		if err != nil {
			continue
		}
		active := snapshot.Active && !snapshot.Paused && snapshot.Frames > previous
		previous = snapshot.Frames
		if active != delivering {
			if active {
				r.delivering.Add(1)
			} else {
				r.delivering.Add(-1)
			}
			delivering = active
		}
		if snapshot.Frames > 0 {
			r.sourceStatus.Store(1)
			r.pipelineStatus.Store(1)
		}
		// Use the observation's start time: a command rendered since then
		// has a later update deadline and must not receive this older snapshot.
		r.updatePanel(guild, snapshot, observed)
		result := snapshot.Last
		if result.Request.AttemptID == last {
			continue
		}
		last = result.Request.AttemptID
		switch result.Outcome {
		case playback.SourceFailure:
			r.sourceStatus.Store(2)
		case playback.EncodeFailure:
			r.pipelineStatus.Store(2)
		}
	}
}
