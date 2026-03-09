package bot

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/player"
	"github.com/GrainedLotus515/gobard/internal/spotify"
	"github.com/GrainedLotus515/gobard/internal/youtube"
	"github.com/bwmarrin/discordgo"
)

// handlePlay handles the play command
func (b *Bot) handlePlay(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	query := i.ApplicationCommandData().Options[0].StringValue()

	// Get user's voice channel
	channelID, err := b.GetVoiceChannel(i.GuildID, i.Member.User.ID)
	if err != nil {
		return fmt.Errorf("you must be in a voice channel to play music")
	}

	// Get or create player
	p := b.PlayerManager.GetPlayer(i.GuildID)

	// Join voice channel if not already connected
	if !p.IsVoiceConnected() {
		vc, err := b.JoinVoiceChannel(i.GuildID, channelID)
		if err != nil {
			return err
		}
		p.SetVoiceConnection(vc)
	}

	// Defer the response since this might take a while
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Parse the query and get tracks
	tracks, err := b.resolveQuery(query, i.Member.User.ID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: ptrString(fmt.Sprintf("🚫 ope: %v", err)),
		})
		return nil
	}

	if len(tracks) == 0 {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: ptrString("🚫 ope: no songs found"),
		})
		return nil
	}

	// Add tracks to queue
	for _, track := range tracks {
		p.Queue.Add(track)
	}

	// Start playing if playback loop is not already running
	if !p.IsLoopRunning() {
		p.SetLoopRunning(true)
		go b.playLoop(i.GuildID, i.ChannelID)
	}

	// Send response
	if len(tracks) == 1 {
		embed := &discordgo.MessageEmbed{
			Title:       "Added to queue",
			Description: fmt.Sprintf("**%s**\nby %s", tracks[0].Title, tracks[0].Artist),
			Color:       0x00ff00,
			Thumbnail: &discordgo.MessageEmbedThumbnail{
				URL: tracks[0].Thumbnail,
			},
		}
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	} else {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: ptrString(fmt.Sprintf("✅ Added %d tracks to queue", len(tracks))),
		})
	}

	return nil
}

// resolveQuery resolves a query to tracks
func (b *Bot) resolveQuery(query, userID string) ([]*player.Track, error) {
	// Check if it's a Spotify URL
	if spotify.IsSpotifyURL(query) {
		if b.Spotify == nil {
			return nil, fmt.Errorf("Spotify integration is not configured")
		}

		spotifyType, id, err := spotify.ParseSpotifyURL(query)
		if err != nil {
			return nil, err
		}

		var spotifyTracks []*player.Track

		switch spotifyType {
		case "track":
			track, err := b.Spotify.GetTrackInfo(id)
			if err != nil {
				return nil, err
			}
			spotifyTracks = []*player.Track{track}
		case "playlist":
			tracks, err := b.Spotify.GetPlaylistTracks(id)
			if err != nil {
				return nil, err
			}
			spotifyTracks = tracks
		case "album":
			tracks, err := b.Spotify.GetAlbumTracks(id)
			if err != nil {
				return nil, err
			}
			spotifyTracks = tracks
		case "artist":
			tracks, err := b.Spotify.GetArtistTopTracks(id)
			if err != nil {
				return nil, err
			}
			spotifyTracks = tracks
		default:
			return nil, fmt.Errorf("unsupported Spotify type: %s", spotifyType)
		}

		// Convert Spotify tracks to YouTube
		tracks := make([]*player.Track, 0)
		for _, st := range spotifyTracks {
			searchQuery := fmt.Sprintf("%s %s", st.Artist, st.Title)
			ytTracks, err := b.YouTube.Search(searchQuery)
			if err != nil || len(ytTracks) == 0 {
				continue
			}
			ytTracks[0].RequestedBy = userID
			tracks = append(tracks, ytTracks[0])
		}

		return tracks, nil
	}

	// Check if it's a YouTube URL
	if youtube.IsYouTubeURL(query) {
		if youtube.IsPlaylist(query) {
			tracks, err := b.YouTube.GetPlaylistInfo(query)
			if err != nil {
				return nil, err
			}
			for _, track := range tracks {
				track.RequestedBy = userID
			}
			return tracks, nil
		} else {
			track, err := b.YouTube.GetVideoInfo(query)
			if err != nil {
				return nil, err
			}
			track.RequestedBy = userID
			return []*player.Track{track}, nil
		}
	}

	// Otherwise, search YouTube
	tracks, err := b.YouTube.Search(query)
	if err != nil {
		return nil, err
	}
	for _, track := range tracks {
		track.RequestedBy = userID
	}
	return tracks, nil
}

// playLoop handles the playback loop for a guild
func (b *Bot) playLoop(guildID string, channelID string) {
	logger.Debug("Starting playback loop", "guild", guildID)
	p := b.PlayerManager.GetPlayer(guildID)

	// Ensure we log when the loop ends
	defer func() {
		logger.Debug("Playback loop ended", "guild", guildID)
	}()

	for {
		// Check if voice connection is still valid before processing next track
		if !p.IsVoiceConnected() {
			logger.Info("Voice connection lost, stopping playback loop", "guild", guildID)
			p.Queue.ClearAll()
			p.SetLoopRunning(false)
			return
		}

		track := p.Queue.Current()
		if track == nil {
			track = p.Queue.Next()
			if track == nil {
				// Queue is empty, disconnect immediately to prevent stale voice connections
				// Discord automatically disconnects idle connections after ~2 minutes
				// Instead of waiting and risking a dead connection, disconnect now
				// so a fresh connection can be created when new songs are added
				logger.PlaybackQueueEmpty()
				p.Queue.ClearAll() // Clear all tracks when queue is empty
				p.SetLoopRunning(false)
				p.Disconnect()
				return
			}
		}

		logger.Info("Processing track", "title", track.Title)
		trackSelectedAt := time.Now()

		// Check if track is already cached
		cacheKey := cache.GenerateKey(track.URL)
		cacheMiss := false
		if cachedPath, exists := b.Cache.Get(cacheKey); exists {
			// Use cached file
			logger.Timing("Cache hit before playback", "title", track.Title, "key", cacheKey)
			logger.PlaybackCached(cachedPath)
			track.LocalPath = cachedPath
		} else {
			// Not cached - stream immediately and defer download until playback begins
			logger.Timing("Cache miss before playback", "title", track.Title, "key", cacheKey)
			logger.Timing("Background cache deferred until playback start", "title", track.Title, "key", cacheKey)
			logger.Info("Track not cached, streaming immediately")
			track.LocalPath = "" // Empty path triggers streaming encoder
			cacheMiss = true
		}

		// Play the track with retry logic
		logger.Info("Starting playback")
		err := b.playTrackForGuild(p)

		if err != nil {
			// Check if error is due to voice connection being lost
			if err.Error() == "not connected to voice channel" {
				logger.Error("Voice connection lost, cannot play track", "title", track.Title)
				p.Queue.ClearAll()
				p.SetLoopRunning(false)
				p.Disconnect()
				return
			}

			logger.Warn("First play attempt failed, retrying", "err", err, "title", track.Title)

			// Clear stream URL to force fresh fetch on retry
			track.StreamURL = ""

			// Retry once
			err = b.playTrackForGuild(p)
			if err != nil {
				// Send failure notification to Discord
				errMsg := fmt.Sprintf("❌ **Track Failed:** %s\n**Reason:** %v", track.Title, err)
				b.Session.ChannelMessageSend(channelID, errMsg)

				logger.Error("Track failed after retry", "title", track.Title, "err", err)
				p.Queue.Next()
				continue
			}
		}

		if cacheMiss {
			started, done := p.PlaybackSignals()
			b.deferBackgroundCacheUntilPlaybackStarts(p, track, cacheKey, trackSelectedAt, started, done)
		}

		// Wait for track to finish
		logger.Debug("Waiting for track to complete")
		b.waitForTrackCompletion(p)
		logger.Info("Track completed", "title", track.Title)

		// A seek request stops playback and replays the current track.
		if p.ConsumeSeekRequest() {
			continue
		}

		// Check if we should loop the current track
		if p.Queue.IsLoopEnabled() {
			// Verify voice connection is still valid before replaying
			if !p.IsVoiceConnected() {
				logger.Info("Voice connection lost during loop, stopping playback", "guild", guildID)
				p.Queue.ClearAll()
				p.SetLoopRunning(false)
				return
			}
			// Don't advance queue, just continue to replay
			continue
		}

		// Check if there are more tracks without advancing
		if p.Queue.Peek() == nil {
			logger.Info("Queue finished, ending playback loop")
			p.Queue.ClearAll() // Clear all tracks when queue finishes
			p.SetLoopRunning(false)
			p.Disconnect()
			return
		}

		// Advance to next track
		p.Queue.Next()
	}
}

func (b *Bot) deferBackgroundCacheUntilPlaybackStarts(
	p *player.GuildPlayer,
	track *player.Track,
	cacheKey string,
	trackSelectedAt time.Time,
	started <-chan struct{},
	done <-chan struct{},
) {
	url := track.URL
	title := track.Title

	go func() {
		if !b.waitForPlaybackStart(p, started, done) {
			logger.Timing(
				"Background cache skipped because playback ended before start",
				"title", title,
				"key", cacheKey,
				"elapsed_ms", time.Since(trackSelectedAt).Milliseconds(),
			)
			return
		}

		logger.Timing(
			"Background cache started after playback began",
			"title", title,
			"key", cacheKey,
			"elapsed_ms", time.Since(trackSelectedAt).Milliseconds(),
		)
		logger.PlaybackDownloading(title)

		_, err := b.Cache.GetOrCreate(cacheKey, func(path string) error {
			return b.cacheTrack(url, path)
		})
		if err != nil {
			logger.Error("Background download failed", "title", title, "err", err)
			return
		}

		logger.Info("Background download completed", "title", title)
	}()
}

// handlePause handles the pause command
func (b *Bot) handlePause(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	p.Pause()
	b.respond(s, i, "⏸️ Paused")
	return nil
}

// handleResume handles the resume command
func (b *Bot) handleResume(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	p.Resume()
	b.respond(s, i, "▶️ Resumed")
	return nil
}

// handleSkip handles the skip command
func (b *Bot) handleSkip(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	next := p.Skip()

	if next == nil {
		b.respond(s, i, "⏭️ Skipped (queue is now empty)")
	} else {
		b.respond(s, i, fmt.Sprintf("⏭️ Skipped to: **%s**", next.Title))
	}
	return nil
}

// handleStop handles the stop command
func (b *Bot) handleStop(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	p.Stop()
	p.Queue.ClearAll()
	p.Disconnect()
	b.respond(s, i, "⏹️ Stopped and cleared queue")
	return nil
}

// handleQueue handles the queue command
func (b *Bot) handleQueue(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)

	if p.Queue.IsEmpty() {
		b.respond(s, i, "Queue is empty")
		return nil
	}

	var builder strings.Builder
	builder.WriteString("**Current Queue:**\n\n")

	tracks, currentIndex := p.Queue.Snapshot()
	for idx, track := range tracks {
		prefix := fmt.Sprintf("%d. ", idx+1)
		if idx == currentIndex {
			prefix = "▶️ "
		}
		builder.WriteString(fmt.Sprintf("%s**%s** - %s\n", prefix, track.Title, track.Artist))
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Queue",
		Description: builder.String(),
		Color:       0x0099ff,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("%d tracks", p.Queue.Length()),
		},
	}

	b.respondEmbed(s, i, embed)
	return nil
}

// handleNowPlaying handles the now-playing command
func (b *Bot) handleNowPlaying(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	track := p.Queue.Current()

	if track == nil {
		b.respond(s, i, "Nothing is currently playing")
		return nil
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Now Playing",
		Description: fmt.Sprintf("**%s**\nby %s", track.Title, track.Artist),
		Color:       0x00ff00,
		Thumbnail: &discordgo.MessageEmbedThumbnail{
			URL: track.Thumbnail,
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Duration",
				Value:  formatDuration(track.Duration),
				Inline: true,
			},
			{
				Name:   "Position",
				Value:  formatDuration(getDisplayPosition(track, p.GetCurrentPosition())),
				Inline: true,
			},
		},
	}

	b.respondEmbed(s, i, embed)
	return nil
}

// handleClear handles the clear command
func (b *Bot) handleClear(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	p.Queue.Clear()
	b.respond(s, i, "🗑️ Cleared queue")
	return nil
}

// handleDisconnect handles the disconnect command
func (b *Bot) handleDisconnect(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	p.Disconnect()
	b.respond(s, i, "👋 Disconnected")
	return nil
}

// handleShuffle handles the shuffle command
func (b *Bot) handleShuffle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)

	if p.Queue.Length() <= 1 {
		return fmt.Errorf("not enough tracks to shuffle")
	}

	if !p.Queue.ShuffleUpcoming() {
		return fmt.Errorf("not enough tracks to shuffle")
	}

	b.respond(s, i, "🔀 Shuffled queue")
	return nil
}

// handleLoop handles the loop command
func (b *Bot) handleLoop(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	enabled := p.Queue.ToggleLoop()

	if enabled {
		b.respond(s, i, "🔂 Looping enabled")
	} else {
		b.respond(s, i, "▶️ Looping disabled")
	}
	return nil
}

// handleVolume handles the volume command
func (b *Bot) handleVolume(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	volume := int(i.ApplicationCommandData().Options[0].IntValue())

	p := b.PlayerManager.GetPlayer(i.GuildID)
	if err := p.SetVolume(volume); err != nil {
		return err
	}

	b.respond(s, i, fmt.Sprintf("🔊 Volume set to %d%%", volume))
	return nil
}

// handleSeek handles the seek command
func (b *Bot) handleSeek(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	position := i.ApplicationCommandData().Options[0].StringValue()

	duration, err := parseDuration(position)
	if err != nil {
		return err
	}

	p := b.PlayerManager.GetPlayer(i.GuildID)
	if err := p.Seek(duration); err != nil {
		return err
	}

	b.respond(s, i, fmt.Sprintf("⏩ Seeked to %s", formatDuration(duration)))
	return nil
}

// handleFSeek handles the fseek command
func (b *Bot) handleFSeek(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	seconds := int(i.ApplicationCommandData().Options[0].IntValue())

	p := b.PlayerManager.GetPlayer(i.GuildID)
	newPosition := p.GetCurrentPosition() + time.Duration(seconds)*time.Second

	if err := p.Seek(newPosition); err != nil {
		return err
	}

	b.respond(s, i, fmt.Sprintf("⏩ Seeked forward %d seconds", seconds))
	return nil
}

// handleMove handles the move command
func (b *Bot) handleMove(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	from := int(i.ApplicationCommandData().Options[0].IntValue()) - 1
	to := int(i.ApplicationCommandData().Options[1].IntValue()) - 1

	p := b.PlayerManager.GetPlayer(i.GuildID)
	if !p.Queue.Move(from, to) {
		return fmt.Errorf("invalid positions")
	}

	b.respond(s, i, fmt.Sprintf("↔️ Moved track from position %d to %d", from+1, to+1))
	return nil
}

// handleRemove handles the remove command
func (b *Bot) handleRemove(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	position := int(i.ApplicationCommandData().Options[0].IntValue()) - 1

	p := b.PlayerManager.GetPlayer(i.GuildID)
	if !p.Queue.Remove(position) {
		return fmt.Errorf("invalid position")
	}

	b.respond(s, i, fmt.Sprintf("🗑️ Removed track at position %d", position+1))
	return nil
}

// handleConfig handles the config command
func (b *Bot) handleConfig(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		return fmt.Errorf("no subcommand provided")
	}

	subCmd := options[0]
	p := b.PlayerManager.GetPlayer(i.GuildID)

	switch subCmd.Name {
	case "set-reduce-vol-when-voice":
		enabled := subCmd.Options[0].BoolValue()
		p.SetVoiceReductionEnabled(enabled)
		if enabled {
			b.respond(s, i, "✅ Volume reduction enabled")
		} else {
			b.respond(s, i, "❌ Volume reduction disabled")
		}

	case "set-reduce-vol-when-voice-target":
		volume := int(subCmd.Options[0].IntValue())
		if err := p.SetVoiceReductionTarget(volume); err != nil {
			return err
		}
		b.respond(s, i, fmt.Sprintf("✅ Volume reduction target set to %d%%", volume))

	case "show":
		enabled, target := p.GetVoiceReductionConfig()
		embed := &discordgo.MessageEmbed{
			Title: "Configuration",
			Fields: []*discordgo.MessageEmbedField{
				{
					Name:   "Reduce volume on voice",
					Value:  fmt.Sprintf("%v", enabled),
					Inline: true,
				},
				{
					Name:   "Voice reduction target",
					Value:  fmt.Sprintf("%d%%", target),
					Inline: true,
				},
			},
			Color: 0x0099ff,
		}
		b.respondEmbed(s, i, embed)

	default:
		return fmt.Errorf("unknown subcommand")
	}

	return nil
}

// Helper functions

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func parseDuration(s string) (time.Duration, error) {
	// Support formats: "1:30", "90", "90s", "1m30s"
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) != 2 {
			return 0, fmt.Errorf("invalid duration format")
		}

		minutes, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, err
		}

		seconds, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, err
		}

		return time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, nil
	}

	// Try parsing as duration string
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Try parsing as seconds
	if seconds, err := strconv.Atoi(strings.TrimSuffix(s, "s")); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}

	return 0, fmt.Errorf("invalid duration format")
}

func ptrString(s string) *string {
	return &s
}

func getDisplayPosition(track *player.Track, position time.Duration) time.Duration {
	if position < 0 {
		return 0
	}
	if !track.IsLive && track.Duration > 0 && position > track.Duration {
		return track.Duration
	}
	return position
}
