package bot

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

// One observer per admitted guild coalesces UI failure notices. Production
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
		if result.Failure == nil {
			continue
		}
		r.mu.Lock()
		channel := r.routes[guild]
		r.mu.Unlock()
		if !validPathPart(channel, 32) {
			continue
		}
		ctx, cancel := context.WithTimeout(r.ctx, 8*time.Second)
		err = r.rest.send(ctx, http.MethodPost, "channels/"+channel+"/messages", &discordgo.MessageSend{Content: fmt.Sprintf("Playback failed after %d delivered frames. %s", result.Frames, commandMessage(result.Failure)), AllowedMentions: noAllowedMentions()}, true)
		cancel()
		if err != nil && r.ctx.Err() == nil {
			r.logger.Warn("Playback notice could not be delivered", "event", "discord.response", "attempt_id", last, "error", err)
		}
	}
}
