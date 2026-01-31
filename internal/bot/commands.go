package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/bwmarrin/discordgo"
	"golang.org/x/time/rate"
)

// Discord API rate limits:
// - Global: 50 requests/second
// - Application Commands: 200 requests/minute
// We use 10 requests/second to stay well within limits
const (
	requestsPerSecond = 10
	burstSize         = 5
)

// CommandRegistrar handles parallel command registration with rate limiting
type CommandRegistrar struct {
	bot         *Bot
	rateLimiter *rate.Limiter
	errors      []error
	errorMutex  sync.Mutex
}

// NewCommandRegistrar creates a new command registrar with rate limiting
func NewCommandRegistrar(bot *Bot) *CommandRegistrar {
	return &CommandRegistrar{
		bot:         bot,
		rateLimiter: rate.NewLimiter(rate.Limit(requestsPerSecond), burstSize),
		errors:      make([]error, 0),
	}
}

// addError adds an error to the error list in a thread-safe manner
func (cr *CommandRegistrar) addError(err error) {
	cr.errorMutex.Lock()
	defer cr.errorMutex.Unlock()
	cr.errors = append(cr.errors, err)
}

// getAggregatedError returns an aggregated error if any errors occurred
func (cr *CommandRegistrar) getAggregatedError() error {
	cr.errorMutex.Lock()
	defer cr.errorMutex.Unlock()

	if len(cr.errors) == 0 {
		return nil
	}

	var errMsg strings.Builder
	errMsg.WriteString(fmt.Sprintf("%d command registration errors:\n", len(cr.errors)))
	for i, err := range cr.errors {
		errMsg.WriteString(fmt.Sprintf("  %d. %v\n", i+1, err))
	}

	return fmt.Errorf("%s", errMsg.String())
}

// registerGlobally registers commands globally using parallel goroutines
func (cr *CommandRegistrar) registerGlobally(commands []*discordgo.ApplicationCommand) error {
	var wg sync.WaitGroup

	for _, cmd := range commands {
		wg.Add(1)
		go func(c *discordgo.ApplicationCommand) {
			defer wg.Done()

			// Wait for rate limiter
			if err := cr.rateLimiter.Wait(context.Background()); err != nil {
				cr.addError(fmt.Errorf("rate limiter error for command %s: %w", c.Name, err))
				return
			}

			// Register command
			_, err := cr.bot.Session.ApplicationCommandCreate(
				cr.bot.Session.State.User.ID, "", c)
			if err != nil {
				cr.addError(fmt.Errorf("failed to create command %s: %w", c.Name, err))
			} else {
				logger.Debug("Registered command globally", "command", c.Name)
			}
		}(cmd)
	}

	wg.Wait()
	return cr.getAggregatedError()
}

// registerPerGuild registers commands for each guild using parallel goroutines
func (cr *CommandRegistrar) registerPerGuild(commands []*discordgo.ApplicationCommand) error {
	var wg sync.WaitGroup
	guilds := cr.bot.Session.State.Guilds

	logger.Info("Found guilds for registration", "count", len(guilds))

	for _, guild := range guilds {
		for _, cmd := range commands {
			wg.Add(1)
			go func(guildID string, guildName string, c *discordgo.ApplicationCommand) {
				defer wg.Done()

				// Wait for rate limiter
				if err := cr.rateLimiter.Wait(context.Background()); err != nil {
					cr.addError(fmt.Errorf("rate limiter error for command %s in guild %s (%s): %w",
						c.Name, guildName, guildID, err))
					return
				}

				// Register command
				_, err := cr.bot.Session.ApplicationCommandCreate(
					cr.bot.Session.State.User.ID, guildID, c)
				if err != nil {
					cr.addError(fmt.Errorf("failed to create command %s in guild %s (%s): %w",
						c.Name, guildName, guildID, err))
				} else {
					logger.Debug("Registered command for guild", "command", c.Name, "guild", guildName)
				}
			}(guild.ID, guild.Name, cmd)
		}
	}

	wg.Wait()
	return cr.getAggregatedError()
}

// registerCommands registers all slash commands
func (b *Bot) registerCommands() error {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "play",
			Description: "Play a song or playlist",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "query",
					Description: "Song name, URL, or search query",
					Required:    true,
				},
			},
		},
		{
			Name:        "pause",
			Description: "Pause playback",
		},
		{
			Name:        "resume",
			Description: "Resume playback",
		},
		{
			Name:        "skip",
			Description: "Skip to the next song",
		},
		{
			Name:        "stop",
			Description: "Stop playback and clear the queue",
		},
		{
			Name:        "queue",
			Description: "Show the current queue",
		},
		{
			Name:        "now-playing",
			Description: "Show the currently playing song",
		},
		{
			Name:        "clear",
			Description: "Clear all songs from the queue except the current one",
		},
		{
			Name:        "disconnect",
			Description: "Disconnect from voice channel",
		},
		{
			Name:        "shuffle",
			Description: "Shuffle the queue",
		},
		{
			Name:        "loop",
			Description: "Toggle looping of the current song",
		},
		{
			Name:        "volume",
			Description: "Set the playback volume",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "level",
					Description: "Volume level (0-100)",
					Required:    true,
					MinValue:    func() *float64 { v := 0.0; return &v }(),
					MaxValue:    100,
				},
			},
		},
		{
			Name:        "seek",
			Description: "Seek to a position in the current song",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "position",
					Description: "Position (e.g., 1:30 or 90s)",
					Required:    true,
				},
			},
		},
		{
			Name:        "fseek",
			Description: "Fast seek forward by seconds",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "seconds",
					Description: "Number of seconds to skip forward",
					Required:    true,
				},
			},
		},
		{
			Name:        "move",
			Description: "Move a song in the queue",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "from",
					Description: "Position to move from",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "to",
					Description: "Position to move to",
					Required:    true,
				},
			},
		},
		{
			Name:        "remove",
			Description: "Remove a song from the queue",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "position",
					Description: "Position in queue to remove",
					Required:    true,
				},
			},
		},
		{
			Name:        "config",
			Description: "Configure bot settings",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "set-reduce-vol-when-voice",
					Description: "Enable/disable volume reduction when someone speaks",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionBoolean,
							Name:        "enabled",
							Description: "Enable or disable",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "set-reduce-vol-when-voice-target",
					Description: "Set target volume when someone speaks",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionInteger,
							Name:        "volume",
							Description: "Target volume (0-100)",
							Required:    true,
							MinValue:    func() *float64 { v := 0.0; return &v }(),
							MaxValue:    100,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "show",
					Description: "Show current configuration",
				},
			},
		},
	}

	b.Commands = commands

	// Create command registrar with rate limiting
	registrar := NewCommandRegistrar(b)

	if b.Config.RegisterGlobally {
		// Register globally using parallel goroutines
		logger.Info("📝 Registering commands globally (parallel)...")
		if err := registrar.registerGlobally(commands); err != nil {
			return err
		}
	} else {
		// Register for each guild using parallel goroutines
		logger.Info("📝 Registering commands per guild (parallel)...")
		if err := registrar.registerPerGuild(commands); err != nil {
			return err
		}
	}

	logger.Info("✅ Commands registered successfully")
	return nil
}

// interactionCreate handles slash command interactions
func (b *Bot) interactionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()

	var err error
	switch data.Name {
	case "play":
		err = b.handlePlay(s, i)
	case "pause":
		err = b.handlePause(s, i)
	case "resume":
		err = b.handleResume(s, i)
	case "skip":
		err = b.handleSkip(s, i)
	case "stop":
		err = b.handleStop(s, i)
	case "queue":
		err = b.handleQueue(s, i)
	case "now-playing":
		err = b.handleNowPlaying(s, i)
	case "clear":
		err = b.handleClear(s, i)
	case "disconnect":
		err = b.handleDisconnect(s, i)
	case "shuffle":
		err = b.handleShuffle(s, i)
	case "loop":
		err = b.handleLoop(s, i)
	case "volume":
		err = b.handleVolume(s, i)
	case "seek":
		err = b.handleSeek(s, i)
	case "fseek":
		err = b.handleFSeek(s, i)
	case "move":
		err = b.handleMove(s, i)
	case "remove":
		err = b.handleRemove(s, i)
	case "config":
		err = b.handleConfig(s, i)
	default:
		err = fmt.Errorf("unknown command")
	}

	if err != nil {
		b.respondError(s, i, err)
	}
}

// respondError sends an error response
func (b *Bot) respondError(s *discordgo.Session, i *discordgo.InteractionCreate, err error) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("🚫 ope: %v", err),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// respond sends a success response
func (b *Bot) respond(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
		},
	})
}

// respondEmbed sends an embed response
func (b *Bot) respondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}
