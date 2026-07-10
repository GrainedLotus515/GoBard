package bot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/discordvoice"
	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/player"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/voiceconn"
	"github.com/GrainedLotus515/gobard/internal/youtube"
)

// Bot represents the Discord bot
type Bot struct {
	Session       *discordgo.Session
	Config        *config.Config
	PlayerManager *player.Manager
	VoiceManager  *discordvoice.Manager
	Cache         *cache.Cache
	YouTube       *youtube.Client
	Commands      []*discordgo.ApplicationCommand

	pendingInteractionResponses   map[string]*pendingInteractionResponse
	pendingInteractionResponsesMu sync.Mutex
	voiceJoinLocks                map[string]*sync.Mutex
	voiceJoinLocksMu              sync.Mutex
	botVoiceSessions              map[string]botVoiceSession
	botVoiceSessionsMu            sync.Mutex
	intentionalDisconnects        map[string]struct{}
	intentionalDisconnectsMu      sync.Mutex
	backgroundCacheMu             sync.Mutex
	backgroundCacheStopping       bool
	backgroundCacheWG             sync.WaitGroup
	asyncWorkMu                   sync.Mutex
	asyncWorkStopping             bool
	asyncWorkContext              context.Context
	asyncWorkCancel               context.CancelFunc
	asyncWorkWG                   sync.WaitGroup

	readinessMu       sync.RWMutex
	live              bool
	discordReady      bool
	commandsReady     bool
	voiceManagerReady bool

	playTrackFn               func(*player.GuildPlayer) error
	waitForCompletionFn       func(*player.GuildPlayer)
	cacheTrackFn              func(url, path string) error
	hydrateStreamInfoFn       func(*player.Track) error
	waitForPlaybackStartFn    func(*player.GuildPlayer) bool
	getVideoInfoFn            func(string) (*player.Track, error)
	interactionResponseEditFn func(*discordgo.Interaction, *discordgo.WebhookEdit) (*discordgo.Message, error)
	channelMessageSendFn      func(string, string) (*discordgo.Message, error)
	commandBulkOverwriteFn    func(string, string, []*discordgo.ApplicationCommand) ([]*discordgo.ApplicationCommand, error)
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

type botVoiceSession struct {
	channelID        string
	sessionID        string
	expectingSession bool
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

	// Configure one process budget for all yt-dlp paths (metadata, cache
	// downloads, and direct playback fallbacks) before any clients are created.
	processlimit.ConfigureGlobal(cfg.YTDLPMaxConcurrency)

	// Create YouTube client with the shared bounded external-tool concurrency.
	ytClient := youtube.NewClientWithOptions(youtube.Options{
		MaxPlaylistTracks: cfg.MaxPlaylistTracks,
		ProcessLimiter:    processlimit.Global(),
	})

	bot := &Bot{
		Session: session,
		Config:  cfg,
		PlayerManager: player.NewManagerWithDefaults(player.PlaybackDefaults{
			Volume:              cfg.DefaultVolume,
			ReduceOnVoice:       cfg.ReduceVolumeOnVoice,
			ReduceOnVoiceTarget: cfg.ReduceVolumeOnVoiceTarget,
		}),
		Cache:   cacheManager,
		YouTube: ytClient,
		nowFn:   time.Now,
	}

	// Register handlers
	session.AddHandler(bot.ready)
	session.AddHandler(bot.interactionCreate)
	session.AddHandler(bot.guildCreate)
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
		if closeErr := b.Session.Close(); closeErr != nil {
			logger.Warn("Failed to close Discord session after voice manager initialization failure", "err", closeErr)
		}
		return fmt.Errorf("failed to initialize voice manager: %w", err)
	}
	b.VoiceManager = voiceManager
	voiceManager.SetSpeakingHandler(b.voiceSpeakingUpdate)
	b.setReadiness(func() {
		b.live = true
		b.voiceManagerReady = true
	})

	logger.Info("🤖 Bot is now running. Press CTRL-C to exit.")
	return nil
}

// Stop stops the bot
func (b *Bot) Stop() error {
	b.setReadiness(func() {
		b.live = false
		b.discordReady = false
		b.commandsReady = false
		b.voiceManagerReady = false
	})
	// Refuse new asynchronous work and cancel work that can honor a context
	// before tearing down the media and Discord dependencies underneath it.
	b.cancelAsyncWork()
	if b.PlayerManager != nil {
		// Stop active sessions before waiting: their done signals cancel any
		// playback-bound cache fills without leaving yt-dlp running on shutdown.
		b.PlayerManager.StopAll()
	}
	b.waitForBackgroundCacheTasks()
	b.waitForAsyncWork()
	if b.VoiceManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		b.VoiceManager.Close(ctx)
		cancel()
	}
	if b.Session == nil {
		return nil
	}
	return b.Session.Close()
}

// ready is called when the bot is ready
func (b *Bot) ready(s *discordgo.Session, event *discordgo.Ready) {
	b.setReadiness(func() {
		b.discordReady = true
		b.commandsReady = false
	})
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
	case "COMPETING":
		activityType = discordgo.ActivityTypeCompeting
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
		return
	}
	b.setReadiness(func() { b.commandsReady = true })
}

// guildCreate reconciles the command surface for servers joined after the
// Ready event. Global commands are already managed by their single overwrite.
func (b *Bot) guildCreate(s *discordgo.Session, event *discordgo.GuildCreate) {
	if b == nil || b.Config == nil || b.Config.RegisterGlobally || s == nil || s.State == nil || s.State.User == nil ||
		event == nil || event.ID == "" || len(b.Commands) == 0 {
		return
	}
	guildID := event.ID
	applicationID := s.State.User.ID
	commands := append([]*discordgo.ApplicationCommand(nil), b.Commands...)
	if !b.beginAsyncWork() {
		return
	}
	go func() {
		defer b.asyncWorkWG.Done()
		if err := b.bulkOverwriteCommands(applicationID, guildID, commands); err != nil {
			logger.Error("Failed to reconcile commands for newly joined guild", "guild", guildID, "err", err)
			return
		}
		logger.Info("Commands reconciled for newly joined guild", "guild", guildID)
	}()
}

// Live implements the process liveness portion of the health checker. It is
// intentionally independent of Discord readiness so a failed registration can
// be diagnosed without causing the process manager to restart it in a loop.
func (b *Bot) Live() error {
	if b == nil {
		return fmt.Errorf("bot is not initialized")
	}
	b.readinessMu.RLock()
	live := b.live
	b.readinessMu.RUnlock()
	if !live {
		return fmt.Errorf("bot is not running")
	}
	return nil
}

// Ready implements the readiness portion of the health checker. Command
// registration is included because a connected bot that cannot receive its
// command surface is not ready to serve users.
func (b *Bot) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b == nil {
		return fmt.Errorf("bot is not initialized")
	}
	b.readinessMu.RLock()
	live := b.live
	discordReady := b.discordReady
	commandsReady := b.commandsReady
	voiceManagerReady := b.voiceManagerReady
	b.readinessMu.RUnlock()
	if !live || !discordReady || !commandsReady || !voiceManagerReady || b.Cache == nil {
		return fmt.Errorf("bot is not ready")
	}
	return nil
}

func (b *Bot) setReadiness(update func()) {
	b.readinessMu.Lock()
	defer b.readinessMu.Unlock()
	update()
}

// voiceStateUpdate handles voice state changes.
//
//nolint:gocyclo,nestif // Gateway voice-state processing has intentionally explicit stale/disconnect branches.
func (b *Bot) voiceStateUpdate(s *discordgo.Session, vsu *discordgo.VoiceStateUpdate) {
	if b == nil || s == nil || s.State == nil || s.State.User == nil || vsu == nil || vsu.VoiceState == nil {
		return
	}
	botUserID := s.State.User.ID
	isBotUpdate := vsu.UserID == botUserID
	if isBotUpdate && !b.acceptBotVoiceStateUpdate(vsu) {
		logger.Warn("Ignored stale bot voice state update", "guild", vsu.GuildID, "channel", vsu.ChannelID)
		return
	}

	if b.VoiceManager != nil {
		b.VoiceManager.HandleVoiceStateUpdate(vsu)
	}

	// Check if this is the bot being disconnected from voice
	if isBotUpdate {
		if vsu.ChannelID == "" {
			intentional := b.consumeIntentionalDisconnect(vsu.GuildID)
			logger.Info("Bot was disconnected from voice channel", "guild", vsu.GuildID, "intentional", intentional)
			if b.PlayerManager != nil {
				p := b.PlayerManager.GetPlayer(vsu.GuildID)
				// Preserve the queue for an explicit /disconnect and for a
				// recoverable transport loss. p.Disconnect already stops an
				// intentional session; ClearVoiceConnection handles external loss.
				if !intentional {
					p.ClearVoiceConnection()
				} else {
					p.SetLoopRunning(false)
				}
			}
		}
		return
	}

	// VoiceStateUpdate is not a speaking event. Treating mute/deaf changes as
	// speech permanently ducked the bot after ordinary leave/move events. The
	// voice transport owns real speaking notifications; here we only clear a
	// speaker that left or moved out of the bot's channel.
	if b.PlayerManager == nil {
		return
	}
	botChannelID, err := b.GetVoiceChannel(vsu.GuildID, botUserID)
	if err != nil || botChannelID == "" {
		return
	}
	wasInBotChannel := vsu.BeforeUpdate != nil && vsu.BeforeUpdate.ChannelID == botChannelID
	if vsu.ChannelID == "" || (wasInBotChannel && vsu.ChannelID != botChannelID) {
		b.PlayerManager.GetPlayer(vsu.GuildID).SpeakerStopped(vsu.UserID)
	}
}

// acceptBotVoiceStateUpdate permits disconnects and channel moves, and rejects
// only same-channel session changes that were not initiated by this process.
// This keeps disgo from being overwritten by an old Discord gateway event
// while still accepting the state needed for an intentional reconnect.
func (b *Bot) acceptBotVoiceStateUpdate(vsu *discordgo.VoiceStateUpdate) bool {
	b.botVoiceSessionsMu.Lock()
	defer b.botVoiceSessionsMu.Unlock()
	if b.botVoiceSessions == nil {
		b.botVoiceSessions = make(map[string]botVoiceSession)
	}
	if vsu.ChannelID == "" {
		delete(b.botVoiceSessions, vsu.GuildID)
		return true
	}

	previous, known := b.botVoiceSessions[vsu.GuildID]
	if !known || previous.channelID == "" || previous.channelID != vsu.ChannelID || previous.sessionID == "" ||
		previous.sessionID == vsu.SessionID || previous.expectingSession {
		b.botVoiceSessions[vsu.GuildID] = botVoiceSession{
			channelID: vsu.ChannelID,
			sessionID: vsu.SessionID,
		}
		return true
	}
	return false
}

func (b *Bot) expectBotVoiceSession(guildID, channelID string) {
	b.botVoiceSessionsMu.Lock()
	defer b.botVoiceSessionsMu.Unlock()
	if b.botVoiceSessions == nil {
		b.botVoiceSessions = make(map[string]botVoiceSession)
	}
	previous := b.botVoiceSessions[guildID]
	previous.channelID = channelID
	previous.expectingSession = true
	b.botVoiceSessions[guildID] = previous
}

func (b *Bot) cancelExpectedBotVoiceSession(guildID, channelID string) {
	b.botVoiceSessionsMu.Lock()
	defer b.botVoiceSessionsMu.Unlock()
	previous, ok := b.botVoiceSessions[guildID]
	if ok && previous.channelID == channelID {
		previous.expectingSession = false
		b.botVoiceSessions[guildID] = previous
	}
}

func (b *Bot) markIntentionalDisconnect(guildID string) {
	if b == nil || guildID == "" {
		return
	}
	b.intentionalDisconnectsMu.Lock()
	if b.intentionalDisconnects == nil {
		b.intentionalDisconnects = make(map[string]struct{})
	}
	b.intentionalDisconnects[guildID] = struct{}{}
	b.intentionalDisconnectsMu.Unlock()
}

func (b *Bot) consumeIntentionalDisconnect(guildID string) bool {
	if b == nil || guildID == "" {
		return false
	}
	b.intentionalDisconnectsMu.Lock()
	defer b.intentionalDisconnectsMu.Unlock()
	_, intentional := b.intentionalDisconnects[guildID]
	delete(b.intentionalDisconnects, guildID)
	return intentional
}

func (b *Bot) clearIntentionalDisconnect(guildID string) {
	if b == nil || guildID == "" {
		return
	}
	b.intentionalDisconnectsMu.Lock()
	delete(b.intentionalDisconnects, guildID)
	b.intentionalDisconnectsMu.Unlock()
}

func (b *Bot) voiceServerUpdate(_ *discordgo.Session, update *discordgo.VoiceServerUpdate) {
	if b.VoiceManager != nil {
		b.VoiceManager.HandleVoiceServerUpdate(update)
	}
}

// voiceSpeakingUpdate is driven by the Discord Voice Gateway's opcode-5
// speaking events, not guild mute/deaf state updates. The extra channel check
// makes a delayed event harmless after the speaker moves elsewhere.
func (b *Bot) voiceSpeakingUpdate(guildID, userID string, speaking bool) {
	if b == nil || b.PlayerManager == nil || b.Session == nil || b.Session.State == nil || b.Session.State.User == nil || userID == "" {
		return
	}
	botUserID := b.Session.State.User.ID
	if userID == botUserID {
		return
	}
	botChannelID, err := b.GetVoiceChannel(guildID, botUserID)
	if err != nil || botChannelID == "" {
		return
	}
	userChannelID, err := b.GetVoiceChannel(guildID, userID)
	player := b.PlayerManager.GetPlayer(guildID)
	if err != nil || userChannelID != botChannelID {
		player.SpeakerStopped(userID)
		return
	}
	if speaking {
		player.SpeakerStarted(userID)
		return
	}
	player.SpeakerStopped(userID)
}

// GetVoiceChannel gets the voice channel a user is in
func (b *Bot) GetVoiceChannel(guildID, userID string) (string, error) {
	if b == nil || b.Session == nil || b.Session.State == nil {
		return "", fmt.Errorf("discord session state is not available")
	}
	if guildID == "" || userID == "" {
		return "", fmt.Errorf("guild and user are required")
	}
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

func (b *Bot) guildJoinLock(guildID string) *sync.Mutex {
	b.voiceJoinLocksMu.Lock()
	defer b.voiceJoinLocksMu.Unlock()
	if b.voiceJoinLocks == nil {
		b.voiceJoinLocks = make(map[string]*sync.Mutex)
	}
	lock := b.voiceJoinLocks[guildID]
	if lock == nil {
		lock = &sync.Mutex{}
		b.voiceJoinLocks[guildID] = lock
	}
	return lock
}

// JoinVoiceChannel joins a voice channel
func (b *Bot) JoinVoiceChannel(guildID, channelID string) (voiceconn.Connection, error) {
	// Join voice channel: mute=false, deaf=true
	// Voice ducking uses Voice Gateway opcode-5 speaking events, not guild
	// mute/deaf state changes. Deafening avoids receiving unprocessed audio
	// packets while leaving speaking notifications available.
	if b.VoiceManager == nil {
		return nil, fmt.Errorf("voice manager is not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	b.expectBotVoiceSession(guildID, channelID)
	vc, err := b.VoiceManager.Join(ctx, guildID, channelID, false, true)
	if err != nil {
		b.cancelExpectedBotVoiceSession(guildID, channelID)
		logger.VoiceConnectionError(err)
		return nil, wrapVoiceJoinError(err)
	}
	b.clearIntentionalDisconnect(guildID)

	return vc, nil
}

// rejoinVoiceChannel finds the bot's current voice channel from Discord state
// and rejoins it.  Used to recover from voice gateway disconnects (e.g. 4006).
func (b *Bot) rejoinVoiceChannel(guildID string) (voiceconn.Connection, error) {
	if b.Session == nil || b.Session.State == nil || b.Session.State.User == nil {
		return nil, fmt.Errorf("discord session is not available")
	}
	channelID, err := b.GetVoiceChannel(guildID, b.Session.State.User.ID)
	if err != nil {
		return nil, fmt.Errorf("could not determine voice channel to rejoin: %w", err)
	}
	return b.JoinVoiceChannel(guildID, channelID)
}

func (b *Bot) playTrackForGuild(p *player.GuildPlayer) error {
	if b.playTrackFn != nil {
		return b.playTrackFn(p)
	}
	return p.Play()
}

func (b *Bot) waitForTrackResult(p *player.GuildPlayer) player.PlaybackResult {
	if b.waitForCompletionFn != nil {
		b.waitForCompletionFn(p)
		result := p.LastPlaybackResult()
		if result.Reason == player.PlaybackEndNone {
			// Test hooks intentionally bypass GuildPlayer.Play; preserve their
			// historical natural-completion semantics while production always
			// waits for the typed session result below.
			return player.PlaybackResult{Reason: player.PlaybackEndCompleted}
		}
		return result
	}
	return p.WaitForCompletionResult()
}

func (b *Bot) cacheTrackContext(ctx context.Context, url, path string) error {
	if b.cacheTrackFn != nil {
		return b.cacheTrackFn(url, path)
	}
	if b.YouTube == nil {
		return fmt.Errorf("YouTube is not initialized")
	}
	return b.YouTube.DownloadContext(ctx, url, path)
}

// beginBackgroundCacheTask atomically refuses new cache fills once shutdown
// starts. It pairs every accepted task with waitForBackgroundCacheTasks.
func (b *Bot) beginBackgroundCacheTask() bool {
	if b == nil {
		return false
	}
	b.backgroundCacheMu.Lock()
	defer b.backgroundCacheMu.Unlock()
	if b.backgroundCacheStopping {
		return false
	}
	b.backgroundCacheWG.Add(1)
	return true
}

// waitForBackgroundCacheTasks is used by shutdown/tests to ensure temporary
// cache creators are no longer touching a cache directory before it is torn
// down. Playback itself never waits for cache population.
func (b *Bot) waitForBackgroundCacheTasks() {
	if b == nil {
		return
	}
	b.backgroundCacheMu.Lock()
	b.backgroundCacheStopping = true
	b.backgroundCacheMu.Unlock()
	b.backgroundCacheWG.Wait()
}

// beginAsyncWork enrolls short-lived non-cache work in the shutdown barrier.
// It is deliberately separate from cache work because cache fills have their
// own playback-bound cancellation path. The mutex prevents WaitGroup.Add from
// racing with shutdown's Wait call.
func (b *Bot) beginAsyncWork() bool {
	if b == nil {
		return false
	}
	b.asyncWorkMu.Lock()
	defer b.asyncWorkMu.Unlock()
	if b.asyncWorkStopping {
		return false
	}
	if b.asyncWorkContext == nil {
		b.asyncWorkContext, b.asyncWorkCancel = context.WithCancel(context.Background())
	}
	b.asyncWorkWG.Add(1)
	return true
}

func (b *Bot) asyncContext() context.Context {
	if b == nil {
		return context.Background()
	}
	b.asyncWorkMu.Lock()
	defer b.asyncWorkMu.Unlock()
	if b.asyncWorkContext == nil {
		b.asyncWorkContext, b.asyncWorkCancel = context.WithCancel(context.Background())
	}
	return b.asyncWorkContext
}

func (b *Bot) cancelAsyncWork() {
	if b == nil {
		return
	}
	b.asyncWorkMu.Lock()
	b.asyncWorkStopping = true
	cancel := b.asyncWorkCancel
	b.asyncWorkMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// waitForAsyncWork joins playback-loop launches, command reconciliation, and
// cancellable metadata hydration before the Discord session is closed.
func (b *Bot) waitForAsyncWork() {
	if b == nil {
		return
	}
	b.asyncWorkWG.Wait()
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
		Content:         ptrString(""),
		Embeds:          &embeds,
		Components:      &components,
		AllowedMentions: noAllowedMentions(),
	}
}

func (b *Bot) getVideoInfo(url string) (*player.Track, error) {
	return b.getVideoInfoContext(context.Background(), url)
}

func (b *Bot) getVideoInfoContext(ctx context.Context, url string) (*player.Track, error) {
	if b.getVideoInfoFn != nil {
		return b.getVideoInfoFn(url)
	}
	if b.YouTube == nil {
		return nil, fmt.Errorf("youtube client is not initialized")
	}
	return b.YouTube.GetVideoInfoContext(ctx, url)
}

// startPlaybackLoop enrolls each guild loop in Bot.Stop's shutdown barrier.
// A loop may only be started while the bot is accepting asynchronous work.
func (b *Bot) startPlaybackLoop(guildID, channelID string) {
	if b == nil || !b.beginAsyncWork() {
		if b != nil && b.PlayerManager != nil {
			b.PlayerManager.GetPlayer(guildID).SetLoopRunning(false)
		}
		return
	}
	go func() {
		defer b.asyncWorkWG.Done()
		b.playLoop(guildID, channelID)
	}()
}

func (b *Bot) editInteractionResponse(interaction *discordgo.Interaction, edit *discordgo.WebhookEdit) error {
	if b.interactionResponseEditFn != nil {
		_, err := b.interactionResponseEditFn(interaction, edit)
		if err != nil {
			logger.Error("Failed to edit interaction response", "err", err)
		}
		return err
	}
	if b.Session == nil {
		return fmt.Errorf("discord session is not initialized")
	}
	_, err := b.Session.InteractionResponseEdit(interaction, edit)
	if err != nil {
		logger.Error("Failed to edit interaction response", "err", err)
	}
	return err
}

func (b *Bot) sendChannelMessage(channelID, content string) error {
	if b.channelMessageSendFn != nil {
		_, err := b.channelMessageSendFn(channelID, content)
		if err != nil {
			logger.Error("Failed to send channel message", "channel", channelID, "err", err)
		}
		return err
	}
	if b.Session == nil {
		return fmt.Errorf("discord session is not initialized")
	}
	_, err := b.Session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         content,
		AllowedMentions: noAllowedMentions(),
	})
	if err != nil {
		logger.Error("Failed to send channel message", "channel", channelID, "err", err)
	}
	return err
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

func (b *Bot) cleanupGuildPendingResponses(guildID string) {
	if guildID == "" {
		return
	}

	b.pendingInteractionResponsesMu.Lock()
	defer b.pendingInteractionResponsesMu.Unlock()

	for traceID, pending := range b.pendingInteractionResponses {
		if pending != nil && pending.GuildID == guildID {
			delete(b.pendingInteractionResponses, traceID)
		}
	}
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
	if !b.beginAsyncWork() {
		return
	}
	ctx := b.asyncContext()

	go func() {
		defer b.asyncWorkWG.Done()
		logger.Info("Background URL metadata hydration started", "trace_id", traceID, "url", placeholder.URL)

		refreshed, err := b.getVideoInfoContext(ctx, placeholder.URL)
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
