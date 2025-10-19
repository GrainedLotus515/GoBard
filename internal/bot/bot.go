package bot

import (
	"context"
	"fmt"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/player"
	"github.com/GrainedLotus515/gobard/internal/spotify"
	"github.com/GrainedLotus515/gobard/internal/youtube"
	"github.com/bwmarrin/discordgo"
)

// Bot represents the Discord bot
type Bot struct {
	Session       *discordgo.Session
	Config        *config.Config
	PlayerManager *player.Manager
	Cache         *cache.Cache
	YouTube       *youtube.Client
	Spotify       *spotify.Client
	Commands      []*discordgo.ApplicationCommand
}

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
	}

	// Register handlers
	session.AddHandler(bot.ready)
	session.AddHandler(bot.interactionCreate)
	session.AddHandler(bot.voiceStateUpdate)

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

	logger.Info("🤖 Bot is now running. Press CTRL-C to exit.")
	return nil
}

// Stop stops the bot
func (b *Bot) Stop() error {
	return b.Session.Close()
}

// ready is called when the bot is ready
func (b *Bot) ready(s *discordgo.Session, event *discordgo.Ready) {
	logger.Info("✅ Logged in", "user", fmt.Sprintf("%v#%v", s.State.User.Username, s.State.User.Discriminator))

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
func (b *Bot) JoinVoiceChannel(guildID, channelID string) (*discordgo.VoiceConnection, error) {
	// Join voice channel: mute=false, deaf=false
	// Bot needs to hear users for voice ducking feature
	ctx := context.Background()
	vc, err := b.Session.ChannelVoiceJoin(ctx, guildID, channelID, false, false)
	if err != nil {
		return nil, fmt.Errorf("failed to join voice channel: %w", err)
	}

	// Wait for voice connection to be ready
	// This ensures the encryption handshake is complete
	if err := vc.Speaking(true); err != nil {
		logger.Warn("Failed to set speaking state", "err", err)
	}

	return vc, nil
}
