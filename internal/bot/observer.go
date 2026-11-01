package bot

import (
	"context"
	"time"

	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

// One observer per admitted guild samples health independently of HTTP. Its
// child owns automatic player edits; a capacity-one hint coalesces changes and
// stores no snapshot history. Closing the observer cancels and joins that child.
func (r *Runtime) observe(guild string, target *session.Session) {
	ctx, cancel := context.WithCancel(r.ctx)
	updates, joined := make(chan struct{}, 1), make(chan struct{})
	go func() {
		defer close(joined)
		for {
			select {
			case <-ctx.Done():
				return
			case <-updates:
			}
			observed := time.Now()
			if snapshot, err := target.Apply(ctx, session.Operation{Action: session.Inspect}); err == nil {
				r.updatePanel(ctx, guild, snapshot, observed)
			}
		}
	}()
	defer func() { cancel(); <-joined }()
	last := ""
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var previous uint64
	var previousRequest string
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
		snapshot, err := target.Apply(r.ctx, session.Operation{Action: session.Inspect})
		if err != nil {
			continue
		}
		active := snapshot.Active && !snapshot.Paused && snapshot.Frames > 0 && (snapshot.RequestID != previousRequest || snapshot.Frames > previous)
		previous = snapshot.Frames
		previousRequest = snapshot.RequestID
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
		select {
		case updates <- struct{}{}:
		default: // The editor fetches current state when ready.
		}
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
