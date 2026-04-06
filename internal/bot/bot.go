package bot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/discordvoice"
	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/player"
	"github.com/GrainedLotus515/gobard/internal/spotify"
	"github.com/GrainedLotus515/gobard/internal/voiceconn"
	"github.com/GrainedLotus515/gobard/internal/youtube"
	"github.com/bwmarrin/discordgo"
)

// Bot represents the Discord bot
type Bot struct {
	Session       *discordgo.Session
	Config        *config.Config
	PlayerManager *player.Manager
	VoiceManager  *discordvoice.Manager
	Cache         *cache.Cache
	YouTube       *youtube.Client
	Spotify       *spotify.Client
	Commands      []*discordgo.ApplicationCommand

	pendingInteractionResponses   map[string]*pendingInteractionResponse
	pendingInteractionResponsesMu sync.Mutex

	playTrackFn               func(*player.GuildPlayer) error
	waitForCompletionFn       func(*player.GuildPlayer)
	cacheTrackFn              func(url, path string) error
	hydrateStreamInfoFn       func(*player.Track) error
	waitForPlaybackStartFn    func(*player.GuildPlayer) bool
	getVideoInfoFn            func(string) (*player.Track, error)
	interactionResponseEditFn func(*discordgo.Interaction, *discordgo.WebhookEdit) (*discordgo.Message, error)
	channelMessageSendFn      func(string, string) (*discordgo.Message, error)
	nowFn                     func() time.Time
}

type pendingInteractionResponse struct {
	Interaction *discordgo.Interaction
	GuildID     string
	ChannelID   string
	TrackRef    *player.Track
	Mode        string
	CreatedAt   time.Time
}

const (
	recentStreamMetadataSkipWindow = 15 * time.Second
	pendingInteractionResponseTTL  = 15 * time.Minute

	pendingInteractionModeStartingPlayback = "starting_playback"
	pendingInteractionModeQueuedSingle     = "queued_single"
)

// New creates a new bot instance
func New(cfg *config.Config) (*Bot, error) {
	// Create Discord session
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	// Create cache
	cacheManager, err := cache.NewCache(cfg.CacheDir, cfg.CacheLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	// Create YouTube client
	ytClient := youtube.NewClient(cfg.YouTubeAPIKey)

	// Create Spotify client (optional)
	var spotifyClient *spotify.Client
	if cfg.SpotifyClientID != "" && cfg.SpotifySecret != "" {
		spotifyClient, err = spotify.NewClient(cfg.SpotifyClientID, cfg.SpotifySecret)
		if err != nil {
			logger.Warn("Failed to create Spotify client", "err", err)
		}
	}

	bot := &Bot{
		Session:       session,
		Config:        cfg,
		PlayerManager: player.NewManager(),
		Cache:         cacheManager,
		YouTube:       ytClient,
		Spotify:       spotifyClient,
		nowFn:         time.Now,
	}

	// Register handlers
	session.AddHandler(bot.ready)
	session.AddHandler(bot.interactionCreate)
	session.AddHandler(bot.voiceStateUpdate)
	session.AddHandler(bot.voiceServerUpdate)

	// Set intents
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildVoiceStates |
		discordgo.IntentsGuildMessages

	return bot, nil
}

// Start starts the bot
func (b *Bot) Start() error {
	if err := b.Session.Open(); err != nil {
		return fmt.Errorf("failed to open Discord session: %w", err)
	}

	voiceManager, err := discordvoice.NewManager(b.Session)
	if err != nil {
		_ = b.Session.Close()
		return fmt.Errorf("failed to initialize voice manager: %w", err)
	}
	b.VoiceManager = voiceManager

	logger.Info("🤖 Bot is now running. Press CTRL-C to exit.")
	return nil
}

// Stop stops the bot
func (b *Bot) Stop() error {
	if b.VoiceManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		b.VoiceManager.Close(ctx)
		cancel()
	}
	return b.Session.Close()
}

// ready is called when the bot is ready
func (b *Bot) ready(s *discordgo.Session, event *discordgo.Ready) {
	logger.Info("✅ Logged in", "user", fmt.Sprintf("%v#%v", s.State.User.Username, s.State.User.Discriminator))
	inviteURL := fmt.Sprintf("https://discord.com/api/oauth2/authorize?client_id=%s&permissions=0&scope=bot%%20applications.commands", s.State.User.ID)
	logger.Info("Invite the bot using this link", "url", inviteURL)

	// Set bot status
	status := b.Config.BotStatus
	if status == "" {
		status = "online"
	}

	activityType := discordgo.ActivityTypeListening
	switch b.Config.BotActivityType {
	case "PLAYING":
		activityType = discordgo.ActivityTypeGame
	case "STREAMING":
		activityType = discordgo.ActivityTypeStreaming
	case "WATCHING":
		activityType = discordgo.ActivityTypeWatching
	}

	err := s.UpdateStatusComplex(discordgo.UpdateStatusData{
		Status: status,
		Activities: []*discordgo.Activity{
			{
				Name: b.Config.BotActivity,
				Type: activityType,
				URL:  b.Config.BotActivityURL,
			},
		},
	})
	if err != nil {
		logger.Error("Error setting status", "err", err)
	}

	// Register commands
	if err := b.registerCommands(); err != nil {
		logger.Error("Error registering commands", "err", err)
	}
}

// voiceStateUpdate handles voice state changes
func (b *Bot) voiceStateUpdate(s *discordgo.Session, vsu *discordgo.VoiceStateUpdate) {
	if b.VoiceManager != nil {
		b.VoiceManager.HandleVoiceStateUpdate(vsu)
	}

	// Check if this is the bot being disconnected from voice
	if vsu.UserID == s.State.User.ID {
		if vsu.ChannelID == "" {
			// Bot was disconnected from voice channel
			logger.Info("Bot was disconnected from voice channel", "guild", vsu.GuildID)
			p := b.PlayerManager.GetPlayer(vsu.GuildID)
			if p != nil {
				p.Stop()
				p.SetLoopRunning(false)
				p.Queue.ClearAll()
				p.ClearVoiceConnection()
			}
		}
		return
	}

	// Handle volume reduction when someone speaks
	if vsu.VoiceState.SelfMute || vsu.VoiceState.SelfDeaf {
		return
	}

	p := b.PlayerManager.GetPlayer(vsu.GuildID)
	if p == nil {
		return
	}

	// If user is speaking, reduce volume
	if !vsu.VoiceState.Mute && !vsu.VoiceState.Deaf {
		p.ReduceVolume()
	} else {
		p.RestoreVolume()
	}
}

func (b *Bot) voiceServerUpdate(_ *discordgo.Session, update *discordgo.VoiceServerUpdate) {
	if b.VoiceManager != nil {
		b.VoiceManager.HandleVoiceServerUpdate(update)
	}
}

// GetVoiceChannel gets the voice channel a user is in
func (b *Bot) GetVoiceChannel(guildID, userID string) (string, error) {
	guild, err := b.Session.State.Guild(guildID)
	if err != nil {
		return "", err
	}

	for _, vs := range guild.VoiceStates {
		if vs.UserID == userID {
			return vs.ChannelID, nil
		}
	}

	return "", fmt.Errorf("user not in voice channel")
}

// JoinVoiceChannel joins a voice channel
func (b *Bot) JoinVoiceChannel(guildID, channelID string) (voiceconn.Connection, error) {
	// Join voice channel: mute=false, deaf=false
	// Bot needs to hear users for voice ducking feature
	if b.VoiceManager == nil {
		return nil, fmt.Errorf("voice manager is not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	vc, err := b.VoiceManager.Join(ctx, guildID, channelID, false, false)
	if err != nil {
		logger.VoiceConnectionError(err)
		return nil, wrapVoiceJoinError(err)
	}

	return vc, nil
}

func (b *Bot) playTrackForGuild(p *player.GuildPlayer) error {
	if b.playTrackFn != nil {
		return b.playTrackFn(p)
	}
	return p.Play()
}

func (b *Bot) waitForTrackCompletion(p *player.GuildPlayer) {
	if b.waitForCompletionFn != nil {
		b.waitForCompletionFn(p)
		return
	}
	p.WaitForCompletion()
}

func (b *Bot) cacheTrack(url, path string) error {
	if b.cacheTrackFn != nil {
		return b.cacheTrackFn(url, path)
	}
	return b.YouTube.Download(url, path)
}

func (b *Bot) currentTime() time.Time {
	if b != nil && b.nowFn != nil {
		return b.nowFn()
	}
	return time.Now()
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}

	return dst
}

func webhookEditForEmbedComponents(embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) *discordgo.WebhookEdit {
	embeds := []*discordgo.MessageEmbed{embed}
	return &discordgo.WebhookEdit{
		Content:    ptrString(""),
		Embeds:     &embeds,
		Components: &components,
	}
}

func (b *Bot) getVideoInfo(url string) (*player.Track, error) {
	if b.getVideoInfoFn != nil {
		return b.getVideoInfoFn(url)
	}
	if b.YouTube == nil {
		return nil, fmt.Errorf("youtube client is not initialized")
	}
	return b.YouTube.GetVideoInfo(url)
}

func (b *Bot) editInteractionResponse(interaction *discordgo.Interaction, edit *discordgo.WebhookEdit) error {
	if b.interactionResponseEditFn != nil {
		_, err := b.interactionResponseEditFn(interaction, edit)
		return err
	}
	if b.Session == nil {
		return fmt.Errorf("discord session is not initialized")
	}
	_, err := b.Session.InteractionResponseEdit(interaction, edit)
	return err
}

func (b *Bot) sendChannelMessage(channelID, content string) error {
	if b.channelMessageSendFn != nil {
		_, err := b.channelMessageSendFn(channelID, content)
		return err
	}
	if b.Session == nil {
		return fmt.Errorf("discord session is not initialized")
	}
	_, err := b.Session.ChannelMessageSend(channelID, content)
	return err
}

func (b *Bot) cleanupExpiredPendingInteractionResponses(now time.Time) {
	b.pendingInteractionResponsesMu.Lock()
	defer b.pendingInteractionResponsesMu.Unlock()
	b.cleanupExpiredPendingInteractionResponsesLocked(now)
}

func (b *Bot) cleanupExpiredPendingInteractionResponsesLocked(now time.Time) {
	if len(b.pendingInteractionResponses) == 0 {
		return
	}

	for traceID, pending := range b.pendingInteractionResponses {
		if pending == nil {
			delete(b.pendingInteractionResponses, traceID)
			continue
		}

		age := now.Sub(pending.CreatedAt)
		if age < 0 {
			age = 0
		}
		if age > pendingInteractionResponseTTL {
			delete(b.pendingInteractionResponses, traceID)
		}
	}
}

func (b *Bot) registerPendingInteractionResponse(
	traceID string,
	interaction *discordgo.Interaction,
	guildID string,
	channelID string,
	track *player.Track,
	mode string,
) {
	if traceID == "" || interaction == nil || track == nil || mode == "" {
		return
	}

	now := b.currentTime()

	b.pendingInteractionResponsesMu.Lock()
	defer b.pendingInteractionResponsesMu.Unlock()

	if b.pendingInteractionResponses == nil {
		b.pendingInteractionResponses = make(map[string]*pendingInteractionResponse)
	}
	b.cleanupExpiredPendingInteractionResponsesLocked(now)
	b.pendingInteractionResponses[traceID] = &pendingInteractionResponse{
		Interaction: interaction,
		GuildID:     guildID,
		ChannelID:   channelID,
		TrackRef:    track,
		Mode:        mode,
		CreatedAt:   now,
	}
}

func (b *Bot) getPendingInteractionResponse(traceID string) (*pendingInteractionResponse, bool) {
	if traceID == "" {
		return nil, false
	}

	now := b.currentTime()

	b.pendingInteractionResponsesMu.Lock()
	defer b.pendingInteractionResponsesMu.Unlock()

	b.cleanupExpiredPendingInteractionResponsesLocked(now)
	pending, ok := b.pendingInteractionResponses[traceID]
	if !ok {
		return nil, false
	}

	return pending, true
}

func (b *Bot) removePendingInteractionResponse(traceID string) {
	if traceID == "" {
		return
	}

	b.pendingInteractionResponsesMu.Lock()
	defer b.pendingInteractionResponsesMu.Unlock()
	delete(b.pendingInteractionResponses, traceID)
}

func (b *Bot) updatePendingInteractionResponseTrackRef(traceID string, track *player.Track) {
	if traceID == "" || track == nil {
		return
	}

	b.pendingInteractionResponsesMu.Lock()
	defer b.pendingInteractionResponsesMu.Unlock()

	if pending, ok := b.pendingInteractionResponses[traceID]; ok && pending != nil {
		pending.TrackRef = track
	}
}

func buildHydratedFastURLTrack(placeholder *player.Track, refreshed *player.Track) *player.Track {
	if placeholder == nil {
		return nil
	}

	replacement := *placeholder
	replacement.MetadataPending = false

	if refreshed == nil {
		return &replacement
	}

	if refreshed.ID != "" {
		replacement.ID = refreshed.ID
	}
	if refreshed.Title != "" {
		replacement.Title = refreshed.Title
	}
	if refreshed.Artist != "" {
		replacement.Artist = refreshed.Artist
	}
	if refreshed.Duration > 0 {
		replacement.Duration = refreshed.Duration
	}
	if refreshed.Thumbnail != "" {
		replacement.Thumbnail = refreshed.Thumbnail
	}

	replacement.IsLive = refreshed.IsLive
	replacement.StreamURL = refreshed.StreamURL
	replacement.StreamHeaders = cloneStringMap(refreshed.StreamHeaders)
	replacement.StreamExpiresAt = refreshed.StreamExpiresAt
	replacement.MetadataFetchedAt = refreshed.MetadataFetchedAt
	replacement.DirectStreamUnavailable = refreshed.DirectStreamUnavailable

	return &replacement
}

func (b *Bot) hydrateFastURLTrackAsync(traceID string, placeholder *player.Track) {
	if traceID == "" || placeholder == nil || placeholder.URL == "" {
		return
	}

	go func() {
		logger.Info("Background URL metadata hydration started", "trace_id", traceID, "url", placeholder.URL)

		refreshed, err := b.getVideoInfo(placeholder.URL)
		if err != nil {
			logger.Warn("Background URL metadata hydration failed", "trace_id", traceID, "url", placeholder.URL, "err", err)
			return
		}

		replacement := buildHydratedFastURLTrack(placeholder, refreshed)
		logger.Info(
			"Background URL metadata hydration succeeded",
			"trace_id", traceID,
			"url", placeholder.URL,
			"title", replacement.Title,
			"has_stream_url", replacement.StreamURL != "",
		)

		pending, ok := b.getPendingInteractionResponse(traceID)
		if !ok {
			return
		}

		guildPlayer := b.PlayerManager.GetPlayer(pending.GuildID)
		if !guildPlayer.Queue.ReplaceTrack(placeholder, replacement) {
			logger.Info("Skipping response update because track is no longer queued", "trace_id", traceID, "mode", pending.Mode)
			b.removePendingInteractionResponse(traceID)
			return
		}

		b.updatePendingInteractionResponseTrackRef(traceID, replacement)
		b.updatePendingInteractionResponseAfterHydration(traceID, replacement)
	}()
}

func (b *Bot) updatePendingInteractionResponseAfterHydration(traceID string, hydratedTrack *player.Track) {
	if traceID == "" || hydratedTrack == nil {
		return
	}

	pending, ok := b.getPendingInteractionResponse(traceID)
	if !ok {
		return
	}

	guildPlayer := b.PlayerManager.GetPlayer(pending.GuildID)
	index, isCurrent, ok := guildPlayer.Queue.FindTrack(hydratedTrack)
	if !ok {
		logger.Info("Skipping response update because track is no longer queued", "trace_id", traceID, "mode", pending.Mode)
		b.removePendingInteractionResponse(traceID)
		return
	}

	state := b.getPlaybackState(pending.GuildID)
	paused := state.Paused && isCurrent

	var (
		embed      *discordgo.MessageEmbed
		components []discordgo.MessageComponent
	)

	switch pending.Mode {
	case pendingInteractionModeStartingPlayback:
		embed, components = b.buildPlaybackCard(
			hydratedTrack,
			state.TotalTracks,
			paused,
			state.LoopEnabled,
			"Status",
			"Starting playback",
			true,
		)
	case pendingInteractionModeQueuedSingle:
		contextLabel := "Queued"
		contextValue := fmt.Sprintf("Queue position #%d", index+1)
		if isCurrent {
			contextLabel = "Status"
			contextValue = "Now playing"
		}
		embed, components = b.buildPlaybackCard(
			hydratedTrack,
			state.TotalTracks,
			paused,
			state.LoopEnabled,
			contextLabel,
			contextValue,
			false,
		)
	default:
		b.removePendingInteractionResponse(traceID)
		return
	}

	if err := b.editInteractionResponse(pending.Interaction, webhookEditForEmbedComponents(embed, components)); err != nil {
		logger.Warn("Failed to update original interaction response after hydration", "trace_id", traceID, "mode", pending.Mode, "err", err)
		b.removePendingInteractionResponse(traceID)
		return
	}

	logger.Info("Original interaction response updated after hydration", "trace_id", traceID, "mode", pending.Mode, "title", hydratedTrack.Title)
	b.removePendingInteractionResponse(traceID)
}

func (b *Bot) failPendingInteractionResponse(traceID string, title string, err error) {
	if traceID == "" || err == nil {
		return
	}

	pending, ok := b.getPendingInteractionResponse(traceID)
	if !ok {
		return
	}

	embed := botui.BuildStatusCard(botui.StatusCardSpec{
		Title:       "Command Failed",
		Description: err.Error(),
		Color:       botui.ColorError,
	})

	if editErr := b.editInteractionResponse(pending.Interaction, webhookEditForEmbedComponents(embed, nil)); editErr != nil {
		logger.Warn("Failed to update original interaction response after playback failure", "trace_id", traceID, "title", title, "err", editErr)
		b.removePendingInteractionResponse(traceID)
		return
	}

	logger.Info("Original interaction response updated after playback failure", "trace_id", traceID, "err", err)
	b.removePendingInteractionResponse(traceID)
}

func (b *Bot) shouldSkipStreamMetadataRefresh(track *player.Track, now time.Time) (bool, time.Duration) {
	if track == nil || track.IsLive || !track.DirectStreamUnavailable || track.MetadataFetchedAt.IsZero() {
		return false, 0
	}

	age := now.Sub(track.MetadataFetchedAt)
	if age < 0 {
		age = 0
	}

	return age <= recentStreamMetadataSkipWindow, age
}

func (b *Bot) hydrateTrackStreamInfo(track *player.Track) error {
	if b.hydrateStreamInfoFn != nil {
		return b.hydrateStreamInfoFn(track)
	}

	refreshed, err := b.getVideoInfo(track.URL)
	if err != nil {
		return err
	}

	track.SetPrefetchedStream(refreshed.StreamURL, refreshed.StreamHeaders, refreshed.StreamExpiresAt)
	if track.Title == "" && refreshed.Title != "" {
		track.Title = refreshed.Title
	}
	if track.Artist == "" && refreshed.Artist != "" {
		track.Artist = refreshed.Artist
	}
	if track.Duration == 0 && refreshed.Duration > 0 {
		track.Duration = refreshed.Duration
	}
	if track.Thumbnail == "" && refreshed.Thumbnail != "" {
		track.Thumbnail = refreshed.Thumbnail
	}
	track.IsLive = track.IsLive || refreshed.IsLive

	if track.StreamURL == "" {
		return fmt.Errorf("no direct stream URL found")
	}

	return nil
}

func (b *Bot) waitForPlaybackStart(p *player.GuildPlayer, started <-chan struct{}, done <-chan struct{}) bool {
	if b.waitForPlaybackStartFn != nil {
		return b.waitForPlaybackStartFn(p)
	}

	select {
	case <-started:
		return true
	case <-done:
		return false
	}
}
