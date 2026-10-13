package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/session"
)

// Only these bounded identity fields survive callback return. The token is
// retained until the response completes, never in core state or logs.
type interactionRef struct{ id, app, token string }

func reference(i *discordgo.InteractionCreate) (interactionRef, bool) {
	if i == nil || i.Interaction == nil || !validPathPart(i.ID, 32) || !validPathPart(i.AppID, 32) || !validPathPart(i.Token, 2048) {
		return interactionRef{}, false
	}
	return interactionRef{strings.Clone(i.ID), strings.Clone(i.AppID), strings.Clone(i.Token)}, true
}

func (r *Runtime) interaction(i *discordgo.InteractionCreate) {
	ref, ok := reference(i)
	if !ok {
		return
	}
	mapped, err := MapInteraction(i)
	if err != nil {
		r.reject(ref)
		return
	}
	r.mu.Lock()
	if r.stopping || r.ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	if !r.admit(mapped.Name == "play") {
		r.mu.Unlock()
		r.reject(ref)
		return
	}
	r.work.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.work.Done()
		defer func() {
			<-r.slots
			if mapped.Name == "play" {
				<-r.playSlots
			}
		}()
		r.executeInteraction(ref, mapped)
	}()
}

func (r *Runtime) admit(play bool) bool {
	select {
	case r.slots <- struct{}{}:
	default:
		return false
	}
	if play {
		select {
		case r.playSlots <- struct{}{}:
		default:
			<-r.slots
			return false
		}
	}
	return true
}

func (r *Runtime) reject(ref interactionRef) {
	select {
	case r.rejections <- ref:
	default:
		r.logger.Warn("Interaction rejection queue saturated", "event", "command.saturation", "recovery", "drop")
	}
}
func (r *Runtime) rejectionLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case ref := <-r.rejections:
			ctx, cancel := context.WithTimeout(r.ctx, 2*time.Second)
			err := r.respond(ctx, ref, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Command unavailable or busy. Please try again shortly.", Flags: discordgo.MessageFlagsEphemeral, AllowedMentions: noAllowedMentions()}})
			cancel()
			if err != nil && r.ctx.Err() == nil {
				r.logger.Warn("Interaction rejection could not be delivered", "event", "discord.response", "error", err)
			}
		}
	}
}

func (r *Runtime) respond(ctx context.Context, ref interactionRef, response *discordgo.InteractionResponse) error {
	return r.rest.send(ctx, http.MethodPost, "interactions/"+ref.id+"/"+ref.token+"/callback", response, false)
}

func (r *Runtime) executeInteraction(ref interactionRef, m MappedInteraction) {
	ctx, cancel := context.WithTimeout(r.ctx, 50*time.Second)
	defer cancel()
	kind := discordgo.InteractionResponseDeferredChannelMessageWithSource
	if m.Component {
		kind = discordgo.InteractionResponseDeferredMessageUpdate
	}
	ack, ackCancel := context.WithTimeout(ctx, 2*time.Second)
	err := r.respond(ack, ref, &discordgo.InteractionResponse{Type: kind, Data: &discordgo.InteractionResponseData{AllowedMentions: noAllowedMentions()}})
	ackCancel()
	if err != nil {
		r.logger.Warn("Interaction acknowledgement failed", "event", "discord.response", "error", err)
		return
	}
	var snapshot session.Snapshot
	var accepted int
	err = r.checkPlayer(ctx, m)
	if err == nil {
		snapshot, accepted, err = r.execute(ctx, m)
	}
	if err != nil && m.Component {
		// A rejected control must not replace the shared player or its buttons.
		err = r.rest.send(ctx, http.MethodPost, "webhooks/"+ref.app+"/"+ref.token, &discordgo.WebhookParams{Content: commandMessage(err), Flags: discordgo.MessageFlagsEphemeral, AllowedMentions: noAllowedMentions()}, false)
		if err != nil && r.ctx.Err() == nil {
			r.logger.Warn("Control response could not be delivered", "event", "discord.response", "error", err)
		}
		return
	}
	err = r.renderInteraction(ctx, ref, m, snapshot, accepted, err)
	if err != nil && r.ctx.Err() == nil {
		r.logger.Warn("Command response could not be delivered", "event", "discord.response", "error", err)
	}
}

func (r *Runtime) execute(ctx context.Context, m MappedInteraction) (session.Snapshot, int, error) {
	if m.Name == "play" {
		return r.play(ctx, m)
	}
	target, ok := r.sessions.Get(m.Guild)
	if !ok {
		if m.Operation.Action == session.Inspect {
			return session.Snapshot{Current: -1, Volume: r.cfg.DefaultVolume, Ducking: r.cfg.ReduceVolumeOnVoice, DuckTarget: r.cfg.ReduceVolumeOnVoiceTarget}, 0, nil
		}
		if m.Name != "config" || !m.ManageGuild {
			return session.Snapshot{}, 0, session.ErrVoiceAccess
		}
		var err error
		target, err = r.guild(m.Guild)
		if err != nil {
			return session.Snapshot{}, 0, err
		}
	}
	access, err := r.Access(ctx, m.Guild, m.User)
	if err != nil {
		return session.Snapshot{}, 0, err
	}
	access.ManageGuild = m.ManageGuild
	snapshot, err := target.ApplyAs(ctx, access, m.Operation)
	return snapshot, 0, err
}

func (r *Runtime) play(ctx context.Context, m MappedInteraction) (session.Snapshot, int, error) {
	access, err := r.Access(ctx, m.Guild, m.User)
	if err != nil {
		return session.Snapshot{}, 0, err
	}
	if access.Channel == "" {
		return session.Snapshot{}, 0, session.ErrVoiceAccess
	}
	target, err := r.guild(m.Guild)
	if err != nil {
		return session.Snapshot{}, 0, err
	}
	if _, err = target.Join(ctx, access.Channel); err != nil {
		return session.Snapshot{}, 0, err
	}
	r.mu.Lock()
	r.routes[m.Guild] = m.Channel
	r.mu.Unlock()
	admission, err := r.player.Play(ctx, target, m.Guild, m.User, m.Query)
	if err != nil {
		return session.Snapshot{}, 0, err
	}
	snapshot, err := target.Apply(ctx, session.Operation{Action: session.Inspect})
	return snapshot, len(admission.Tickets), err
}

func commandMessage(err error) string {
	if errors.Is(err, errStalePlayer) {
		return "This player has been replaced. Use the newest player message or /now-playing."
	}
	for _, safe := range []error{session.ErrBusy, session.ErrFull, session.ErrVoiceAccess, session.ErrManageGuild, session.ErrSuperseded, session.ErrGuildLimit} {
		if errors.Is(err, safe) {
			return safe.Error()
		}
	}
	var classified *failure.Error
	if errors.As(err, &classified) {
		switch classified.Reason {
		case failure.ConnectionReset:
			return "The media connection was reset before playback finished."
		case failure.NetworkTimeout:
			return "The media connection timed out before playback finished."
		}
		switch classified.Category {
		case failure.Authentication:
			return "This track or connection is currently unavailable. Try another track or reconnect the bot."
		case failure.Throttled:
			return "The external service is rate limiting requests. Try again later."
		case failure.Unavailable:
			return "The requested media is unavailable."
		}
		return "Something interrupted playback. Try again, or choose another track."
	}
	return "The operation could not be applied. Check the queue, voice channel, and command values."
}

func runtimeCard(m MappedInteraction, s session.Snapshot, accepted int) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	if m.Name == "queue" {
		return runtimeQueueCard(s, m.Page)
	}
	if m.Name == "config" {
		ducking := "Off"
		if s.Ducking {
			ducking = "On"
		}
		return botui.BuildStatusCard(botui.StatusCardSpec{Title: "Server playback settings", Description: fmt.Sprintf("Volume: **%d%%**\nLower music while people speak: **%s**\nSpeaking volume: **%d%%**", s.Volume, ducking, s.DuckTarget), Color: botui.ColorInfo, Footer: "Settings apply until the bot restarts."}), nil
	}
	if m.Name == "stop" {
		return botui.BuildStatusCard(botui.StatusCardSpec{Title: "Stopping playback", Description: "Clearing the queue and leaving voice. Use `/play` to start again.", Color: botui.ColorInfo}), nil
	}
	if m.Name == "disconnect" {
		return botui.BuildStatusCard(botui.StatusCardSpec{Title: "Leaving voice", Description: "Your queue is saved for this session. Use `/play` to reconnect.", Color: botui.ColorInfo}), nil
	}
	if s.Current >= 0 && s.Current < len(s.Tracks) {
		t := s.Tracks[s.Current]
		spec := botui.PlaybackCardSpec{Title: t.Title, URL: t.URL, Artist: t.Artist, ThumbnailURL: t.Thumbnail, Length: trackLength(t), RequestedBy: t.RequestedBy, ContextLabel: playbackLabel(s), ContextValue: runtimeProgress(s.Position, t.Duration), Footer: fmt.Sprintf("Volume %d%% • %d up next • Progress updates every 15s", s.Volume, len(s.Tracks)-s.Current-1), ShowControls: s.Connected, Paused: s.Paused, LoopEnabled: s.Loop, Notice: actionNotice(m.Name, accepted)}
		if t.Live {
			spec.ContextValue = runtimeDuration(s.Position)
		}
		if s.Current+1 < len(s.Tracks) {
			next := s.Tracks[s.Current+1]
			spec.NextTitle, spec.NextURL = next.Title, next.URL
		}
		return botui.BuildPlaybackCard(spec)
	}
	return botui.BuildStatusCard(botui.StatusCardSpec{Title: "Nothing playing", Description: "Join a voice channel and use `/play` with a song name or YouTube link.", Color: botui.ColorInfo}), nil
}

func runtimeDuration(d time.Duration) string {
	d = max(d, 0)
	if d >= time.Hour {
		return fmt.Sprintf("%d:%02d:%02d", int(d/time.Hour), int(d/time.Minute)%60, int(d/time.Second)%60)
	}
	return fmt.Sprintf("%d:%02d", int(d/time.Minute), int(d/time.Second)%60)
}
func runtimeQueueCard(s session.Snapshot, page int) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	start := max(0, s.Current+1)
	count := len(s.Tracks) - start
	pages := max(1, (count+7)/8)
	page = min(max(page, 1), pages)
	spec := botui.QueueCardSpec{Page: page, TotalPages: pages, TotalTracks: count, LoopEnabled: s.Loop}
	if s.Current >= 0 && s.Current < len(s.Tracks) {
		t := s.Tracks[s.Current]
		spec.NowPlayingStatus = playbackLabel(s)
		spec.NowPlayingTitle = t.Title
		spec.NowPlayingURL = t.URL
		spec.NowPlayingArtist = t.Artist
		spec.NowPlayingProgress = runtimeDuration(s.Position) + " / " + trackLength(t)
		spec.TotalTracks++
	}
	for i := start + (page-1)*8; i < min(start+page*8, len(s.Tracks)); i++ {
		t := s.Tracks[i]
		spec.Upcoming = append(spec.Upcoming, botui.QueueEntrySpec{Position: i + 1, Title: t.Title, URL: t.URL, Artist: t.Artist})
	}
	return botui.BuildQueueCard(spec)
}
