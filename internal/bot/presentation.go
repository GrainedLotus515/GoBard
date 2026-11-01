package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/GrainedLotus515/gobard/internal/media"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

func playbackLabel(s session.Snapshot) string {
	switch {
	case s.Transition == playback.Stopped:
		return "Stopping"
	case s.Transition == playback.Skipped || s.Transition == playback.Removed:
		return "Changing track"
	case s.Transition == playback.Seeked:
		return "Seeking"
	case s.Connecting:
		return "Connecting to voice"
	case !s.Connected:
		return "Disconnected • queue saved"
	case s.Paused:
		return "Paused"
	case s.Active && s.Frames > 0:
		return "Now playing"
	case s.Recovery == "source_resume":
		return "Resuming after an interruption"
	case s.Active:
		return "Starting playback"
	default:
		return "Queued"
	}
}

func trackLength(t media.Track) string {
	if t.Live {
		return "Live"
	}
	if t.Duration <= 0 {
		return "Unknown length"
	}
	return runtimeDuration(t.Duration)
}

func runtimeProgress(position, duration time.Duration) string {
	if duration <= 0 {
		return runtimeDuration(position)
	}
	position = min(max(position, 0), duration)
	filled := int(float64(position) / float64(duration) * 12)
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 12-filled) + "\n" + runtimeDuration(position)
}

func actionNotice(name string, accepted int) string {
	switch name {
	case "play":
		if accepted == 1 {
			return "Added **1 track** to the queue."
		}
		return fmt.Sprintf("Added **%d tracks** to the queue.", accepted)
	case "clear":
		return "Upcoming tracks cleared."
	case "shuffle":
		return "Upcoming tracks shuffled."
	case "move":
		return "Queue order updated."
	case "remove":
		return "Track removal accepted."
	case "volume":
		return "Volume updated."
	default:
		return ""
	}
}
