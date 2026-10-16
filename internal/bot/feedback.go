package bot

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/command"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

// Discord copy is allowlisted here. Never format an external cause, provider
// response, diagnostic identifier or URL into a user-facing failure.
func commandFailure(err error) *discordgo.MessageEmbed {
	title, message := "Couldn't apply that command", "Check the queue position or command values, then try again."
	switch {
	case errors.Is(err, errStalePlayer):
		title, message = "This player has moved", "Use the newest player card, or `/now-playing` to bring it here."
	case errors.Is(err, session.ErrVoiceAccess):
		title, message = "Join the music channel", "Join the bot's voice channel to change playback. If it isn't connected, join a voice channel and use `/play`."
	case errors.Is(err, session.ErrManageGuild):
		title, message = "Server permission needed", "You need **Manage Server** to change these settings."
	case errors.Is(err, session.ErrBusy), errors.Is(err, command.ErrBusy):
		title, message = "Still handling another request", "Wait a moment, then try again."
	case errors.Is(err, session.ErrFull):
		title, message = "The queue is full", "Remove some upcoming tracks or wait for the queue to advance, then add your music again."
	case errors.Is(err, session.ErrGuildLimit):
		title, message = "No session available", "The bot has reached its server-session limit. Ask the bot operator to review capacity."
	case errors.Is(err, session.ErrSuperseded):
		title, message = "Request canceled by a newer command", "Playback was stopped or disconnected while this request was being prepared. Use `/play` again if you still want it."
	case errors.Is(err, context.DeadlineExceeded):
		title, message = "The request took too long", "Check `/queue` before trying again: an accepted change may already have taken effect."
	case errors.Is(err, context.Canceled), errors.Is(err, session.ErrClosed), errors.Is(err, command.ErrClosed):
		title, message = "Request interrupted", "The session is closing or the request was canceled. Check `/now-playing` before sending it again."
	default:
		var f *failure.Error
		if errors.As(err, &f) {
			title, message = "Couldn't play this request", failureMessage(f)
		}
	}
	return botui.BuildStatusCard(botui.StatusCardSpec{Title: title, Description: message, Color: botui.ColorWarning})
}

func failureMessage(f *failure.Error) string {
	switch f.Category {
	case failure.Authentication:
		if f.Stage == "voice_join" || f.Stage == "discord_response" {
			return "Discord refused access. Ask a server moderator to check the bot's channel permissions."
		}
		return "The media provider refused access to this track. Try a different public track."
	case failure.Throttled:
		return "The service is limiting requests. Wait a while before trying again."
	case failure.Unavailable:
		return "This track is unavailable from its provider. Choose another track."
	case failure.Transport:
		return "The voice connection couldn't deliver audio. Check that the bot can connect and speak in your channel."
	case failure.ZeroFrames:
		return "The track returned no playable audio. Try another track."
	case failure.Codec, failure.InvalidFrame:
		return "The audio couldn't be processed. Try another track; if it keeps happening, let the bot operator know."
	case failure.Cleanup:
		return "Playback couldn't finish cleanly. If this repeats, let the bot operator know."
	}
	switch f.Reason {
	case failure.HTTPForbidden:
		return "The media provider refused the audio download. Choose another track."
	case failure.FormatUnavailable:
		return "The media provider offered no compatible audio format for this track."
	case failure.ConnectionReset:
		return "The media connection dropped."
	case failure.NetworkTimeout:
		return "The media provider stopped responding."
	default:
		return "The track couldn't be loaded. Try another track; if several tracks fail, let the bot operator know."
	}
}

func playbackFeedback(s session.Snapshot) *discordgo.MessageEmbedField {
	if s.Transition != "" {
		return nil
	}
	if s.Recovery == "voice_reconnect" {
		return &discordgo.MessageEmbedField{Name: "Reconnecting to voice", Value: "The connection was interrupted. Trying to reconnect; your queue is retained."}
	}
	if s.Recovery == "voice_exhausted" {
		return &discordgo.MessageEmbedField{Name: "Couldn't reconnect", Value: "Automatic reconnect attempts have ended. Your queue is retained. Check voice permissions, then use `/play` to reconnect."}
	}
	last := s.Last
	if last.Failure == nil || (last.Outcome != playback.SourceFailure && last.Outcome != playback.EncodeFailure && last.Outcome != playback.TransportFailure) {
		return nil
	}
	if s.RequestID != "" && s.RequestID == last.Request.ID && s.Active && s.Frames > 0 {
		return nil // Delivery resumed for this logical request, not just an open socket.
	}
	if s.Recovery == "source_resume" {
		return &discordgo.MessageEmbedField{Name: "Trying to resume", Value: "The media connection was interrupted. Trying once to resume from where it stopped."}
	}
	name, next := "Playback couldn't continue", "Use `/play` to choose another track."
	if s.RequestID != "" && s.RequestID != last.Request.ID {
		name, next = "Previous track couldn't finish", "The queue has moved on to the current selection."
	} else if last.Outcome == playback.TransportFailure {
		next = "Your queue is retained. Use `/play` to reconnect."
	}
	return &discordgo.MessageEmbedField{Name: name, Value: failureMessage(last.Failure) + "\n" + next}
}

func commandReceipt(m MappedInteraction, s session.Snapshot, accepted int) *discordgo.MessageEmbed {
	if m.Name == "config" {
		card, _ := runtimeCard(m, s, accepted)
		return card
	}
	title, message := "Player updated", "Your command was applied."
	switch m.Name {
	case "play":
		title, message = "Added to queue", actionNotice("play", accepted)
	case "stop":
		title, message = "Stop requested", "Clearing the queue and leaving voice."
	case "disconnect":
		title, message = "Disconnect requested", "Leaving voice with your queue retained for this session. Use `/play` to reconnect."
	case "pause", "resume":
		if s.Paused {
			title = "Playback paused"
		} else {
			title = "Playback resumed"
		}
	case "skip":
		title, message = "Skip requested", "Moving on from the current selection."
	case "seek", "fseek":
		title, message = "Seek requested", "Preparing playback at the requested position."
	case "volume":
		title, message = "Volume updated", fmt.Sprintf("Playback volume is **%d%%**.", s.Volume)
	case "loop":
		title, message = "Repeat off", "Tracks will advance normally."
		if s.Loop {
			title, message = "Repeat on", "The current track will repeat after it finishes."
		}
	case "clear", "shuffle", "move", "remove":
		title, message = "Queue updated", actionNotice(m.Name, accepted)
	case "queue":
		title = "Queue updated"
	}
	return botui.BuildStatusCard(botui.StatusCardSpec{Title: title, Description: message, Color: botui.ColorSuccess})
}
