package bot

import (
	"fmt"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/bwmarrin/discordgo"
)

func (b *Bot) handleMessageComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	action, metadata, err := botui.ParseCustomID(data.CustomID)
	if err != nil {
		logger.Warn("Rejected unknown component action", "custom_id", data.CustomID, "guild", i.GuildID, "user", i.Member.User.ID)
		b.respondComponentError(s, i, "Controls Unavailable", "That control is no longer recognized.")
		return
	}

	if action == botui.ActionQueuePage {
		logger.Debug("Queue page requested", "guild", i.GuildID, "user", i.Member.User.ID, "action", action, "page", metadata.Page)
		state := b.getPlaybackState(i.GuildID)
		embed, components := b.buildQueueCard(state, metadata.Page)
		b.updateComponentMessage(s, i, embed, components)
		return
	}

	state := b.getPlaybackState(i.GuildID)
	if state.Track == nil {
		logger.Info("Component action updated stale playback card", "guild", i.GuildID, "user", i.Member.User.ID, "action", action, "allowed", true)
		b.updateComponentMessage(s, i, idleStatusCard("Playback Is Idle", "Nothing is currently playing."), nil)
		return
	}

	if err := b.requirePlaybackControlAccess(i.GuildID, i.Member.User.ID); err != nil {
		logger.Info("Rejected component action", "guild", i.GuildID, "user", i.Member.User.ID, "action", action, "allowed", false)
		b.respondComponentError(s, i, "Join Voice First", err.Error())
		return
	}

	logger.Info("Accepted component action", "guild", i.GuildID, "user", i.Member.User.ID, "action", action, "allowed", true)

	p := b.PlayerManager.GetPlayer(i.GuildID)
	switch action {
	case botui.ActionPlaybackTogglePause:
		if state.Paused {
			p.Resume()
		} else {
			p.Pause()
		}

		updated := b.getPlaybackState(i.GuildID)
		if updated.Track == nil {
			b.updateComponentMessage(s, i, idleStatusCard("Playback Is Idle", "Nothing is currently playing."), nil)
			return
		}

		label, value := playbackProgressContext(updated.Track, updated.Position, updated.Paused)
		embed, components := b.buildPlaybackCard(
			updated.Track,
			updated.TotalTracks,
			updated.Paused,
			updated.LoopEnabled,
			label,
			value,
			true,
		)
		b.updateComponentMessage(s, i, embed, components)

	case botui.ActionPlaybackSkip:
		next := p.Skip()
		if next == nil {
			b.updateComponentMessage(s, i, idleStatusCard("Playback Is Idle", "Queue is now empty."), nil)
			return
		}

		state = b.getPlaybackState(i.GuildID)
		embed, components := b.buildPlaybackCard(
			next,
			state.TotalTracks,
			false,
			state.LoopEnabled,
			"Progress",
			progressText(next, 0),
			true,
		)
		b.updateComponentMessage(s, i, embed, components)

	case botui.ActionPlaybackToggleLoop:
		p.Queue.ToggleLoop()
		updated := b.getPlaybackState(i.GuildID)
		if updated.Track == nil {
			b.updateComponentMessage(s, i, idleStatusCard("Playback Is Idle", "Nothing is currently playing."), nil)
			return
		}

		label, value := playbackProgressContext(updated.Track, updated.Position, updated.Paused)
		embed, components := b.buildPlaybackCard(
			updated.Track,
			updated.TotalTracks,
			updated.Paused,
			updated.LoopEnabled,
			label,
			value,
			true,
		)
		b.updateComponentMessage(s, i, embed, components)

	case botui.ActionPlaybackStop:
		p.Stop()
		p.Queue.ClearAll()
		p.Disconnect()
		b.updateComponentMessage(s, i, botui.BuildStatusCard(botui.StatusCardSpec{
			Title:       "Playback Stopped",
			Description: "Disconnected and cleared the queue.",
			Color:       botui.ColorWarning,
		}), nil)

	default:
		b.respondComponentError(s, i, "Controls Unavailable", fmt.Sprintf("Unsupported action %q.", action))
	}
}

func (b *Bot) requirePlaybackControlAccess(guildID, userID string) error {
	userChannelID, err := b.GetVoiceChannel(guildID, userID)
	if err != nil {
		return fmt.Errorf("join the bot's voice channel to use playback controls")
	}

	botChannelID, err := b.GetVoiceChannel(guildID, b.Session.State.User.ID)
	if err != nil {
		return fmt.Errorf("join the bot's voice channel to use playback controls")
	}

	if userChannelID != botChannelID {
		return fmt.Errorf("join the bot's voice channel to use playback controls")
	}

	return nil
}
