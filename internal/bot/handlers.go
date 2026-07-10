package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/player"
	"github.com/GrainedLotus515/gobard/internal/voiceconn"
	"github.com/GrainedLotus515/gobard/internal/youtube"
)

const backgroundCacheDownloadConcurrency = 1

var (
	backgroundCacheDownloadSlots   = make(chan struct{}, backgroundCacheDownloadConcurrency)
	backgroundCacheDownloadSlotsMu sync.RWMutex
)

// handlePlay handles the play command.
//
//nolint:gocyclo // Deferred Discord acknowledgement and join rollback must stay adjacent to queue admission.
func (b *Bot) handlePlay(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	userID, err := interactionUserID(i)
	if err != nil {
		return err
	}
	options := i.ApplicationCommandData().Options
	if len(options) != 1 || strings.TrimSpace(options[0].StringValue()) == "" {
		return fmt.Errorf("a song, YouTube URL, or search query is required")
	}
	query := strings.TrimSpace(options[0].StringValue())
	trace := logger.NewTrace(
		"play_command",
		"guild", i.GuildID,
		"user", userID,
		"query_kind", playQueryKind(query),
	)

	// Get user's voice channel
	trace.Step("Looking up caller voice channel")
	channelID, err := b.GetVoiceChannel(i.GuildID, userID)
	if err != nil {
		trace.Finish("Caller voice channel lookup failed", "err", err)
		return fmt.Errorf("you must be in a voice channel to play music")
	}
	trace.Step("Caller voice channel resolved", "channel_id", channelID)
	if b.PlayerManager == nil {
		return fmt.Errorf("playback is unavailable")
	}
	if existingPlayer := b.PlayerManager.GetPlayer(i.GuildID); existingPlayer.IsVoiceConnected() {
		if err := b.requirePlaybackControlAccess(i.GuildID, userID); err != nil {
			return err
		}
	}

	// Defer before voice join or metadata work. Voice joins can exceed
	// Discord's initial interaction deadline when another guild is busy.
	if err := b.deferInteractionResponse(s, i); err != nil {
		trace.Finish("Deferred interaction response failed", "err", err)
		return fmt.Errorf("could not acknowledge the command: %w", err)
	}
	trace.Step("Deferred interaction response sent")

	// Resolve before joining. yt-dlp can take several seconds and no failed
	// search should leave the bot connected to a channel.
	trace.Step("Resolving play query")
	tracks, err := b.resolveQuery(query, userID)
	if err != nil {
		trace.Finish("Play query resolution failed", "err", err)
		b.editDeferredEmbedComponents(s, i, botui.BuildStatusCard(botui.StatusCardSpec{
			Title:       "Command Failed",
			Description: err.Error(),
			Color:       botui.ColorError,
		}), nil)
		return nil
	}

	if len(tracks) == 0 {
		trace.Finish("Play query returned no tracks")
		b.editDeferredEmbedComponents(s, i, botui.BuildStatusCard(botui.StatusCardSpec{
			Title:       "Nothing Found",
			Description: "No songs matched that query.",
			Color:       botui.ColorError,
		}), nil)
		return nil
	}
	trace.Step("Play query resolved", "track_count", len(tracks))

	// The caller may have moved while metadata was resolving. Use their current
	// channel, not the stale channel from the start of the interaction.
	channelID, err = b.GetVoiceChannel(i.GuildID, userID)
	if err != nil {
		trace.Finish("Caller left voice while resolving", "err", err)
		b.editDeferredEmbedComponents(s, i, botui.BuildStatusCard(botui.StatusCardSpec{
			Title:       "Command Failed",
			Description: "You must remain in a voice channel to start playback.",
			Color:       botui.ColorError,
		}), nil)
		return nil
	}

	p := b.PlayerManager.GetPlayer(i.GuildID)
	joinLock := b.guildJoinLock(i.GuildID)
	joinLock.Lock()
	wasIdle := p.Queue.Current() == nil && p.Queue.IsEmpty()
	joinedHere := false
	var joinErr error
	if p.IsVoiceConnected() {
		joinErr = b.requirePlaybackControlAccess(i.GuildID, userID)
	} else {
		trace.Step("Joining voice channel", "channel_id", channelID)
		var vc voiceconn.Connection
		vc, joinErr = b.JoinVoiceChannel(i.GuildID, channelID)
		if joinErr == nil {
			p.SetVoiceConnection(vc)
			joinedHere = true
			trace.Step("Voice channel joined", "channel_id", channelID)
		}
	}
	joinLock.Unlock()
	if joinErr != nil {
		trace.Finish("Voice channel access or join failed", "channel_id", channelID, "err", joinErr)
		b.editDeferredEmbedComponents(s, i, botui.BuildStatusCard(botui.StatusCardSpec{
			Title:       "Command Failed",
			Description: joinErr.Error(),
			Color:       botui.ColorError,
		}), nil)
		return nil
	}

	// If a newly-created connection cannot be committed to a queue, do not
	// strand it in the user's channel. The only operations below are currently
	// in-memory, but keeping this rollback makes future validation safe too.
	playCommitted := false
	defer func() {
		if joinedHere && !playCommitted {
			disconnectPlayer(p, i.GuildID, "play command rollback")
		}
	}()

	fastPathTrack := len(tracks) == 1 && tracks[0].MetadataPending
	pendingMode := ""
	if fastPathTrack {
		logger.Info(
			"Fast URL path selected",
			"trace_id", trace.ID(),
			"canonical_url", tracks[0].URL,
			"video_id", tracks[0].ID,
		)
		if wasIdle {
			pendingMode = pendingInteractionModeStartingPlayback
		} else {
			pendingMode = pendingInteractionModeQueuedSingle
		}
	}

	// Add tracks to queue
	for _, track := range tracks {
		if track.RequestTraceID == "" {
			track.RequestTraceID = trace.ID()
		}
		if track.RequestedAt.IsZero() {
			track.RequestedAt = trace.StartedAt()
		}
		p.Queue.Add(track)
	}
	playCommitted = true

	if fastPathTrack {
		b.registerPendingInteractionResponse(
			trace.ID(),
			i.Interaction,
			i.GuildID,
			i.ChannelID,
			tracks[0],
			pendingMode,
		)
	}

	// Start playing if playback loop is not already running.
	shouldStartPlayback := p.StartLoopIfIdle()
	delayLoopStartForFastPath := fastPathTrack && wasIdle && shouldStartPlayback
	if shouldStartPlayback && !delayLoopStartForFastPath {
		trace.Step("Starting playback loop")
		b.startPlaybackLoop(i.GuildID, i.ChannelID)
	}

	queueLength := p.Queue.Length()
	loopEnabled := p.Queue.IsLoopEnabled()
	trace.Step("Tracks queued", "queue_length", queueLength, "was_idle", wasIdle)

	if wasIdle {
		contextValue := "Starting playback"
		if len(tracks) > 1 {
			contextValue = fmt.Sprintf("+%d more queued", len(tracks)-1)
		}

		embed, components := b.buildPlaybackCard(
			tracks[0],
			queueLength,
			false,
			loopEnabled,
			"Status",
			contextValue,
			true,
		)
		b.editDeferredEmbedComponents(s, i, embed, components)
		if delayLoopStartForFastPath {
			trace.Step("Starting playback loop")
			b.startPlaybackLoop(i.GuildID, i.ChannelID)
		}
		if fastPathTrack {
			b.hydrateFastURLTrackAsync(trace.ID(), tracks[0])
		}
		trace.Finish("Play command completed", "status", "starting_playback", "first_track", tracks[0].Title)
		return nil
	}

	if len(tracks) == 1 {
		embed, components := b.buildPlaybackCard(
			tracks[0],
			queueLength,
			false,
			loopEnabled,
			"Queued",
			fmt.Sprintf("Queue position #%d", queueLength),
			false,
		)
		b.editDeferredEmbedComponents(s, i, embed, components)
		if fastPathTrack {
			b.hydrateFastURLTrackAsync(trace.ID(), tracks[0])
		}
		trace.Finish("Play command completed", "status", "queued", "first_track", tracks[0].Title, "queue_position", queueLength)
		return nil
	}

	startPosition := queueLength - len(tracks) + 1
	b.editDeferredEmbedComponents(s, i, botui.BuildStatusCard(botui.StatusCardSpec{
		Title:       "Added to Queue",
		Description: fmt.Sprintf("Queued %d tracks starting at #%d; first added was %s.", len(tracks), startPosition, tracks[0].Title),
		Color:       botui.ColorSuccess,
	}), nil)
	trace.Finish("Play command completed", "status", "queued_batch", "track_count", len(tracks), "start_position", startPosition)

	return nil
}

func playQueryKind(query string) string {
	if youtube.IsURLLike(query) {
		return "url"
	}
	return "search"
}

// resolveQuery resolves a query to tracks
func (b *Bot) resolveQuery(query, userID string) ([]*player.Track, error) {
	query = strings.TrimSpace(query)
	if youtube.IsURLLike(query) {
		canonicalURL, kind, err := youtube.ClassifyYouTubeURL(query)
		if err != nil {
			return nil, fmt.Errorf("only HTTPS YouTube video and playlist URLs are supported")
		}

		switch kind {
		case youtube.URLKindVideo:
			_, videoID, ok := youtube.NormalizeSingleVideoURL(canonicalURL)
			if !ok {
				return nil, fmt.Errorf("invalid YouTube video URL")
			}
			return []*player.Track{
				{
					ID:              videoID,
					Title:           "Loading track...",
					Artist:          "YouTube",
					URL:             canonicalURL,
					Duration:        0,
					Source:          player.SourceYouTube,
					Thumbnail:       "",
					IsLive:          false,
					RequestedBy:     userID,
					MetadataPending: true,
				},
			}, nil
		case youtube.URLKindPlaylist:
			if b == nil || b.YouTube == nil {
				return nil, fmt.Errorf("YouTube is not initialized")
			}
			tracks, err := b.YouTube.GetPlaylistInfo(canonicalURL)
			if err != nil {
				return nil, err
			}
			for _, track := range tracks {
				track.RequestedBy = userID
			}
			return tracks, nil
		default:
			return nil, fmt.Errorf("unsupported YouTube URL")
		}
	}

	// Otherwise, search YouTube
	if b == nil || b.YouTube == nil {
		return nil, fmt.Errorf("YouTube is not initialized")
	}
	tracks, err := b.YouTube.Search(query)
	if err != nil {
		return nil, err
	}
	for _, track := range tracks {
		track.RequestedBy = userID
	}
	return tracks, nil
}

// playLoop handles the playback loop for a guild.
//
//nolint:gocyclo,nestif // This is the explicit playback state machine; splitting cases would obscure transitions.
func (b *Bot) playLoop(guildID string, channelID string) {
	logger.Debug("Starting playback loop", "guild", guildID)
	p := b.PlayerManager.GetPlayer(guildID)

	// Ensure we log when the loop ends
	defer func() {
		logger.Debug("Playback loop ended", "guild", guildID)
	}()
	sourceRetries := make(map[*player.Track]int)

	for {
		// Check if voice connection is still valid before processing next track
		if !p.IsVoiceConnected() {
			logger.Info("Voice connection lost, stopping playback loop", "guild", guildID)
			b.cleanupGuildPendingResponses(guildID)
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
				b.cleanupGuildPendingResponses(guildID)
				p.SetLoopRunning(false)
				disconnectPlayer(p, guildID, "queue empty before playback")
				return
			}
		}

		logger.Info("Processing track", "title", track.Title)
		trackSelectedAt := time.Now()
		startupTrace := logger.ResumeTrace(track.RequestTraceID, "track_startup", track.RequestedAt)
		if startupTrace != nil {
			startupTrace.Step(
				"Playback loop selected track",
				"title", track.Title,
				"url", track.URL,
				"queue_wait_ms", time.Since(track.RequestedAt).Milliseconds(),
			)
		}

		// Check if track is already cached
		cacheKey := cache.GenerateKey(track.URL)
		cacheMiss := false
		var cacheLease *cache.Lease
		releaseCacheLease := func() {
			if cacheLease != nil {
				cacheLease.Release()
				cacheLease = nil
			}
		}
		if lease, exists := b.Cache.Acquire(cacheKey); exists {
			cacheLease = lease
			cachedPath := lease.Path
			// Use cached file
			logger.Timing("Cache hit before playback", "title", track.Title, "key", cacheKey)
			logger.PlaybackCached(cachedPath)
			track.LocalPath = cachedPath
			if startupTrace != nil {
				startupTrace.Step("Cache hit before playback", "cache_key", cacheKey)
			}
		} else {
			// Not cached - stream immediately and defer download until playback begins
			logger.Timing("Cache miss before playback", "title", track.Title, "key", cacheKey)
			logger.Timing("Background cache deferred until playback start", "title", track.Title, "key", cacheKey)
			logger.Info("Track not cached, streaming immediately")
			track.LocalPath = "" // Empty path triggers streaming encoder
			cacheMiss = true
			if startupTrace != nil {
				startupTrace.Step("Cache miss before playback", "cache_key", cacheKey)
			}

			now := time.Now()
			if track.MetadataPending {
				logger.Info(
					"Skipping blocking metadata fetch for fast URL path",
					"trace_id", track.RequestTraceID,
					"title", track.Title,
					"url", track.URL,
					"reason", "metadata_pending_fast_path",
				)
				if startupTrace != nil {
					startupTrace.Step(
						"Skipping blocking metadata fetch for fast URL path",
						"reason", "metadata_pending_fast_path",
						"title", track.Title,
						"url", track.URL,
					)
				}
			} else if track.URL != "" && !track.IsLive && !track.CanUsePrefetchedStream(now, 0) {
				if skipRefresh, age := b.shouldSkipStreamMetadataRefresh(track, now); skipRefresh {
					logger.Info(
						"Skipping stream metadata refresh",
						"reason", "recent_resolution_no_stream_url",
						"age_ms", age.Milliseconds(),
						"trace_id", track.RequestTraceID,
						"title", track.Title,
						"url", track.URL,
					)
					if startupTrace != nil {
						startupTrace.Step(
							"Skipping stream metadata refresh",
							"reason", "recent_resolution_no_stream_url",
							"age_ms", age.Milliseconds(),
							"title", track.Title,
							"url", track.URL,
						)
					}
				} else {
					refreshStart := time.Now()
					logger.Info("Refreshing stream metadata before playback", "title", track.Title, "url", track.URL)
					if startupTrace != nil {
						startupTrace.Step("Refreshing stream metadata before playback", "url", track.URL)
					}
					if err := b.hydrateTrackStreamInfo(track); err != nil {
						logger.Warn("Stream metadata refresh failed before playback", "title", track.Title, "err", err)
						if startupTrace != nil {
							startupTrace.Step("Stream metadata refresh failed before playback", "err", err)
						}
					} else {
						logger.Timing(
							"Stream metadata refreshed before playback",
							"title", track.Title,
							"elapsed_ms", time.Since(refreshStart).Milliseconds(),
							"expires_at", track.StreamExpiresAt,
						)
						if startupTrace != nil {
							startupTrace.Step(
								"Stream metadata refreshed before playback",
								"refresh_ms", time.Since(refreshStart).Milliseconds(),
								"expires_at", track.StreamExpiresAt,
							)
						}
					}
				}
			}
		}

		// Play the track with retry logic
		logger.Info("Starting playback")
		if startupTrace != nil {
			startupTrace.Step("Invoking player playback", "cache_miss", cacheMiss)
		}
		err := b.playTrackForGuild(p)

		if err != nil {
			// Check if error is due to voice connection being lost
			if err.Error() == "not connected to voice channel" {
				logger.Error("Voice connection lost, cannot play track", "title", track.Title)
				if startupTrace != nil {
					startupTrace.Finish("Playback aborted because voice connection was lost", "err", err)
				}
				releaseCacheLease()
				p.SetLoopRunning(false)
				return
			}

			logger.Warn("First play attempt failed, retrying", "err", err, "title", track.Title)
			if startupTrace != nil {
				startupTrace.Step("First playback attempt failed", "err", err)
			}

			// Clear the pre-fetched stream metadata to force a fresh live resolution on retry.
			track.ClearPrefetchedStream()

			// Retry once
			err = b.playTrackForGuild(p)
			if err != nil {
				releaseCacheLease()
				b.failPendingInteractionResponse(track.RequestTraceID, track.Title, err)

				// Send failure notification to Discord. sendChannelMessage disables
				// mentions for untrusted track titles and extractor errors.
				if notifyErr := b.sendChannelMessage(channelID, trackFailureMessage(track, err)); notifyErr != nil {
					logger.Error("Failed to notify channel about startup failure", "err", notifyErr)
				}

				logger.Error("Track failed after retry", "title", track.Title, "err", err)
				if startupTrace != nil {
					startupTrace.Finish("Track startup failed after retry", "err", err)
				}
				if p.Queue.TryAdvanceBypassingLoop() == nil {
					b.cleanupGuildPendingResponses(guildID)
					p.SetLoopRunning(false)
					return
				}
				continue
			}
			if startupTrace != nil {
				startupTrace.Step("Playback retry succeeded")
			}
		}

		if cacheMiss {
			started, done := p.PlaybackSignals()
			b.deferBackgroundCacheUntilPlaybackStarts(p, track, cacheKey, trackSelectedAt, started, done)
		}

		// Wait for the session's typed terminal result. Queue transitions are
		// deliberately owned here rather than inferred from a closed channel.
		logger.Debug("Waiting for track to complete")
		result := b.waitForTrackResult(p)
		releaseCacheLease()
		logger.Info("Track completed", "title", track.Title, "reason", result.Reason, "err", result.Err)
		// Compatibility for the lightweight playback test hook, which does not
		// create a real session and therefore cannot return PlaybackEndSeeked.
		if result.Reason == player.PlaybackEndCompleted && p.ConsumeSeekRequest() {
			continue
		}

		switch result.Reason {
		case player.PlaybackEndSeeked:
			p.ConsumeSeekRequest()
			continue

		case player.PlaybackEndSkipped:
			p.ConsumeSkipRequest()
			if p.Queue.TryAdvanceBypassingLoop() == nil {
				b.cleanupGuildPendingResponses(guildID)
				p.SetLoopRunning(false)
				disconnectPlayer(p, guildID, "skip reached end of queue")
				return
			}
			continue

		case player.PlaybackEndSourceFailure:
			if sourceRetries[track] < 1 {
				sourceRetries[track]++
				track.ClearPrefetchedStream()
				if err := b.hydrateTrackStreamInfo(track); err != nil {
					logger.Warn("Fresh stream metadata resolution failed before source retry", "title", track.Title, "err", err)
				}
				logger.Warn("Retrying source failure", "title", track.Title, "err", result.Err)
				continue
			}
			b.failPendingInteractionResponse(track.RequestTraceID, track.Title, result.Err)
			if err := b.sendChannelMessage(channelID, trackFailureMessage(track, result.Err)); err != nil {
				logger.Error("Failed to notify channel about source failure", "err", err)
			}
			if p.Queue.TryAdvanceBypassingLoop() == nil {
				b.cleanupGuildPendingResponses(guildID)
				p.SetLoopRunning(false)
				disconnectPlayer(p, guildID, "source failure reached end of queue")
				return
			}
			continue

		case player.PlaybackEndTransportFailure:
			if b.reconnectForPlayback(guildID, p, track) {
				continue
			}
			b.cleanupGuildPendingResponses(guildID)
			p.SetLoopRunning(false)
			return

		case player.PlaybackEndStopped:
			// Current-track removal stops the session, but the queue has a
			// pending successor. /stop clears/disconnects and therefore exits.
			if !p.IsVoiceConnected() || (p.Queue.Current() == nil && p.Queue.IsEmpty()) {
				p.SetLoopRunning(false)
				return
			}
			if p.Queue.TryAdvanceBypassingLoop() == nil {
				b.cleanupGuildPendingResponses(guildID)
				p.SetLoopRunning(false)
				disconnectPlayer(p, guildID, "removed current track reached end of queue")
				return
			}
			continue

		case player.PlaybackEndDisconnected:
			// Intentional disconnects preserve the queue for the next /play.
			p.SetLoopRunning(false)
			return

		case player.PlaybackEndCompleted, player.PlaybackEndNone:
			if p.Queue.IsLoopEnabled() {
				continue
			}
			if p.Queue.TryAdvance() == nil {
				logger.Info("Queue finished, ending playback loop")
				b.cleanupGuildPendingResponses(guildID)
				p.SetLoopRunning(false)
				disconnectPlayer(p, guildID, "queue completed")
				return
			}
			continue

		default:
			logger.Warn("Unknown playback end reason", "reason", result.Reason, "title", track.Title)
			p.SetLoopRunning(false)
			return
		}
	}
}

func (b *Bot) reconnectForPlayback(guildID string, p *player.GuildPlayer, track *player.Track) bool {
	for attempt := 0; attempt < 3; attempt++ {
		if p == nil || p.Queue.Current() != track || !p.IsLoopRunning() {
			return false
		}
		if attempt > 0 {
			time.Sleep(time.Second << (attempt - 1))
		}
		if p.Queue.Current() != track || !p.IsLoopRunning() {
			return false
		}
		logger.Info("Attempting voice transport recovery", "guild", guildID, "attempt", attempt+1, "title", track.Title)
		vc, err := b.rejoinVoiceChannel(guildID)
		if err != nil {
			logger.Warn("Voice transport recovery attempt failed", "guild", guildID, "attempt", attempt+1, "err", err)
			continue
		}
		if p.Queue.Current() != track || !p.IsLoopRunning() {
			if disconnectErr := vc.Disconnect(context.Background()); disconnectErr != nil {
				logger.Warn("Failed to close superseded voice connection", "guild", guildID, "err", disconnectErr)
			}
			return false
		}
		p.SetVoiceConnection(vc)
		logger.Info("Voice transport recovered; replaying current track", "guild", guildID, "title", track.Title)
		return true
	}
	return false
}

func trackFailureMessage(track *player.Track, err error) string {
	title := "Unknown track"
	if track != nil && track.Title != "" {
		title = track.Title
	}
	if err == nil {
		return fmt.Sprintf("Track failed and was skipped: %s", title)
	}
	return fmt.Sprintf("Track failed and was skipped: %s (reason: %v)", title, err)
}

// disconnectPlayer records a failed best-effort disconnect without allowing a
// stale voice connection to survive a terminal queue transition.
func disconnectPlayer(p *player.GuildPlayer, guildID, reason string) {
	if p == nil {
		return
	}
	if err := p.Disconnect(); err != nil {
		logger.Error("Failed to disconnect voice player", "guild", guildID, "reason", reason, "err", err)
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
	if b == nil || track == nil || !b.beginBackgroundCacheTask() {
		return
	}
	url := track.URL
	title := track.Title

	go func() {
		defer b.backgroundCacheWG.Done()
		if !b.waitForPlaybackStart(p, started, done) {
			logger.Timing(
				"Background cache skipped because playback ended before start",
				"title", title,
				"key", cacheKey,
				"elapsed_ms", time.Since(trackSelectedAt).Milliseconds(),
			)
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if b.waitForCompletionFn == nil {
			stopWatch := make(chan struct{})
			go func() {
				select {
				case <-done:
					cancel()
				case <-stopWatch:
				}
			}()
			defer close(stopWatch)
		}

		releaseCacheSlot, acquired := acquireBackgroundCacheDownloadSlotContext(ctx)
		if !acquired {
			logger.Timing(
				"Background cache skipped after playback ended",
				"title", title,
				"key", cacheKey,
				"elapsed_ms", time.Since(trackSelectedAt).Milliseconds(),
			)
			return
		}
		defer releaseCacheSlot()

		logger.Timing(
			"Background cache started after playback began",
			"title", title,
			"key", cacheKey,
			"elapsed_ms", time.Since(trackSelectedAt).Milliseconds(),
		)
		logger.PlaybackDownloading(title)

		_, err := b.Cache.GetOrCreate(cacheKey, func(path string) error {
			return b.cacheTrackContext(ctx, url, path)
		})
		if err != nil {
			logger.Error("Background download failed", "title", title, "err", err)
			return
		}

		logger.Info("Background download completed", "title", title)
	}()
}

func acquireBackgroundCacheDownloadSlotContext(ctx context.Context) (func(), bool) {
	slots := currentBackgroundCacheDownloadSlots()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, false
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			<-slots
		})
	}, true
}

func currentBackgroundCacheDownloadSlots() chan struct{} {
	backgroundCacheDownloadSlotsMu.RLock()
	defer backgroundCacheDownloadSlotsMu.RUnlock()
	return backgroundCacheDownloadSlots
}

// handlePause handles the pause command
func (b *Bot) handlePause(s *discordgo.Session, i *discordgo.InteractionCreate) {
	state := b.getPlaybackState(i.GuildID)
	if state.Track == nil {
		b.respondEmbed(s, i, idleStatusCard("Nothing is currently playing."))
		return
	}

	p := b.PlayerManager.GetPlayer(i.GuildID)
	p.Pause()
	b.respondStatus(s, i, "Playback Paused", fmt.Sprintf("Paused %s.", state.Track.Title), botui.ColorInfo)
}

// handleResume handles the resume command
func (b *Bot) handleResume(s *discordgo.Session, i *discordgo.InteractionCreate) {
	state := b.getPlaybackState(i.GuildID)
	if state.Track == nil {
		b.respondEmbed(s, i, idleStatusCard("Nothing is currently playing."))
		return
	}

	p := b.PlayerManager.GetPlayer(i.GuildID)
	p.Resume()
	b.respondStatus(s, i, "Playback Resumed", fmt.Sprintf("Resumed %s.", state.Track.Title), botui.ColorSuccess)
}

// handleSkip handles the skip command
func (b *Bot) handleSkip(s *discordgo.Session, i *discordgo.InteractionCreate) {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	next := p.Skip()

	if next == nil {
		b.respondStatus(s, i, "Track Skipped", "Queue is now empty.", botui.ColorInfo)
	} else {
		b.respondStatus(s, i, "Track Skipped", fmt.Sprintf("Skipped to %s.", next.Title), botui.ColorSuccess)
	}
}

// handleStop handles the stop command
func (b *Bot) handleStop(s *discordgo.Session, i *discordgo.InteractionCreate) {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	p.Stop()
	p.Queue.ClearAll()
	disconnectPlayer(p, i.GuildID, "stop command")
	b.respondStatus(s, i, "Playback Stopped", "Disconnected and cleared the queue.", botui.ColorWarning)
}

// handleQueue handles the queue command
func (b *Bot) handleQueue(s *discordgo.Session, i *discordgo.InteractionCreate) {
	state := b.getPlaybackState(i.GuildID)
	embed, components := b.buildQueueCard(state, 1)
	b.respondEmbedComponents(s, i, embed, components)
}

// handleNowPlaying handles the now-playing command
func (b *Bot) handleNowPlaying(s *discordgo.Session, i *discordgo.InteractionCreate) {
	state := b.getPlaybackState(i.GuildID)
	if state.Track == nil {
		b.respondEmbed(s, i, idleStatusCard("Nothing is currently playing."))
		return
	}

	label, value := playbackProgressContext(state.Track, state.Position, state.Paused)
	embed, components := b.buildPlaybackCard(
		state.Track,
		state.TotalTracks,
		state.Paused,
		state.LoopEnabled,
		label,
		value,
		true,
	)
	b.respondEmbedComponents(s, i, embed, components)
}

// handleClear handles the clear command
func (b *Bot) handleClear(s *discordgo.Session, i *discordgo.InteractionCreate) {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	p.Queue.Clear()
	b.respondStatus(s, i, "Queue Cleared", "Removed queued tracks and kept the current song.", botui.ColorInfo)
}

// handleDisconnect handles the disconnect command
func (b *Bot) handleDisconnect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	if p.IsVoiceConnected() {
		b.markIntentionalDisconnect(i.GuildID)
	}
	disconnectPlayer(p, i.GuildID, "disconnect command")
	b.respondStatus(s, i, "Disconnected", "Left the voice channel. Your queue will resume from the current track next time you play.", botui.ColorWarning)
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

	b.respondStatus(s, i, "Queue Shuffled", "Reordered the upcoming tracks.", botui.ColorSuccess)
	return nil
}

// handleLoop handles the loop command
func (b *Bot) handleLoop(s *discordgo.Session, i *discordgo.InteractionCreate) {
	p := b.PlayerManager.GetPlayer(i.GuildID)
	enabled := p.Queue.ToggleLoop()

	if enabled {
		b.respondStatus(s, i, "Loop Enabled", "The current track will repeat.", botui.ColorSuccess)
	} else {
		b.respondStatus(s, i, "Loop Disabled", "The current track will no longer repeat.", botui.ColorInfo)
	}
}

// handleVolume handles the volume command
func (b *Bot) handleVolume(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	volume := int(i.ApplicationCommandData().Options[0].IntValue())

	p := b.PlayerManager.GetPlayer(i.GuildID)
	if err := p.SetVolume(volume); err != nil {
		return err
	}

	b.respondStatus(s, i, "Volume Updated", fmt.Sprintf("Playback volume is now %d%%.", volume), botui.ColorSuccess)
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

	b.respondStatus(s, i, "Seek Updated", fmt.Sprintf("Jumped to %s.", formatDuration(duration)), botui.ColorSuccess)
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

	b.respondStatus(s, i, "Seek Updated", fmt.Sprintf("Advanced playback by %d seconds.", seconds), botui.ColorSuccess)
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

	b.respondStatus(s, i, "Queue Updated", fmt.Sprintf("Moved track from #%d to #%d.", from+1, to+1), botui.ColorSuccess)
	return nil
}

// handleRemove handles the remove command
func (b *Bot) handleRemove(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	position := int(i.ApplicationCommandData().Options[0].IntValue()) - 1

	p := b.PlayerManager.GetPlayer(i.GuildID)
	removed, wasCurrent := p.Queue.Remove(position)
	if !removed {
		return fmt.Errorf("invalid position")
	}

	// If the currently-playing track was removed, stop playback so the
	// playLoop advances to the next track immediately.
	if wasCurrent {
		p.Stop()
		if p.Queue.IsEmpty() {
			disconnectPlayer(p, i.GuildID, "removed final current track")
		}
	}

	b.respondStatus(s, i, "Track Removed", fmt.Sprintf("Removed track #%d from the queue.", position+1), botui.ColorWarning)
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
			b.respondStatus(s, i, "Voice Ducking Enabled", "Volume will drop when someone speaks.", botui.ColorSuccess)
		} else {
			b.respondStatus(s, i, "Voice Ducking Disabled", "Volume will stay unchanged when someone speaks.", botui.ColorInfo)
		}

	case "set-reduce-vol-when-voice-target":
		volume := int(subCmd.Options[0].IntValue())
		if err := p.SetVoiceReductionTarget(volume); err != nil {
			return err
		}
		b.respondStatus(s, i, "Voice Ducking Target Updated", fmt.Sprintf("Voice ducking now targets %d%% volume.", volume), botui.ColorSuccess)

	case "show":
		enabled, target := p.GetVoiceReductionConfig()
		embed := &discordgo.MessageEmbed{
			Title:       "Configuration",
			Description: "Current voice ducking settings.",
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
			Color: botui.ColorInfo,
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
