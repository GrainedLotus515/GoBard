package bot

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/logger"
)

const (
	commandRegistrationAttempts = 3
	commandRegistrationBackoff  = time.Second
	commandRegistrationWorkers  = 4
)

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
	contexts := []discordgo.InteractionContextType{discordgo.InteractionContextGuild}
	for _, command := range commands {
		command.Contexts = &contexts
	}

	if b.Session == nil || b.Session.State == nil || b.Session.State.User == nil {
		return fmt.Errorf("cannot register commands before Discord identifies the bot user")
	}
	applicationID := b.Session.State.User.ID

	if b.Config.RegisterGlobally {
		logger.Info("📝 Reconciling global commands")
		if err := b.bulkOverwriteCommands(applicationID, "", commands); err != nil {
			return err
		}
	} else {
		logger.Info("📝 Reconciling commands per guild", "count", len(b.Session.State.Guilds))
		if err := b.bulkOverwriteGuildCommands(applicationID, commands); err != nil {
			return err
		}
	}

	logger.Info("✅ Commands registered successfully")
	return nil
}

func (b *Bot) bulkOverwriteGuildCommands(applicationID string, commands []*discordgo.ApplicationCommand) error {
	guilds := append([]*discordgo.Guild(nil), b.Session.State.Guilds...)
	if len(guilds) == 0 {
		return nil
	}

	errCh := make(chan error, len(guilds))
	workers := make(chan struct{}, commandRegistrationWorkers)
	var wg sync.WaitGroup
	for _, guild := range guilds {
		if guild == nil || guild.ID == "" {
			continue
		}
		wg.Add(1)
		go func(guildID, guildName string) {
			defer wg.Done()
			workers <- struct{}{}
			defer func() { <-workers }()
			if err := b.bulkOverwriteCommands(applicationID, guildID, commands); err != nil {
				errCh <- fmt.Errorf("reconcile commands for guild %s (%s): %w", guildName, guildID, err)
			}
		}(guild.ID, guild.Name)
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (b *Bot) bulkOverwriteCommands(applicationID, guildID string, commands []*discordgo.ApplicationCommand) error {
	var lastErr error
	for attempt := 0; attempt < commandRegistrationAttempts; attempt++ {
		if b.commandBulkOverwriteFn != nil {
			_, lastErr = b.commandBulkOverwriteFn(applicationID, guildID, commands)
		} else {
			_, lastErr = b.Session.ApplicationCommandBulkOverwrite(applicationID, guildID, commands)
		}
		if lastErr == nil {
			logger.Debug("Commands reconciled", "guild", guildID, "count", len(commands))
			return nil
		}
		if !isTransientCommandRegistrationError(lastErr) || attempt == commandRegistrationAttempts-1 {
			break
		}
		time.Sleep(commandRegistrationBackoff * time.Duration(1<<attempt))
	}
	return lastErr
}

func isTransientCommandRegistrationError(err error) bool {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		return restErr.Response.StatusCode == 429 || restErr.Response.StatusCode >= 500
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

// interactionCreate handles slash command interactions
func (b *Bot) interactionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		logger.Warn("Ignoring malformed interaction")
		return
	}
	if err := requireGuildMemberInteraction(i); err != nil {
		b.respondError(s, i, err)
		return
	}

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		b.handleApplicationCommand(s, i)

	case discordgo.InteractionMessageComponent:
		b.handleMessageComponent(s, i)
	}
}

type applicationCommandHandler func(*discordgo.Session, *discordgo.InteractionCreate) error

func noErrorCommandHandler(handler func(*discordgo.Session, *discordgo.InteractionCreate)) applicationCommandHandler {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) error {
		handler(s, i)
		return nil
	}
}

func (b *Bot) handleApplicationCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if err := b.authorizeApplicationCommand(i, data.Name); err != nil {
		b.respondError(s, i, err)
		return
	}

	handlers := map[string]applicationCommandHandler{
		"play":        b.handlePlay,
		"pause":       noErrorCommandHandler(b.handlePause),
		"resume":      noErrorCommandHandler(b.handleResume),
		"skip":        noErrorCommandHandler(b.handleSkip),
		"stop":        noErrorCommandHandler(b.handleStop),
		"queue":       noErrorCommandHandler(b.handleQueue),
		"now-playing": noErrorCommandHandler(b.handleNowPlaying),
		"clear":       noErrorCommandHandler(b.handleClear),
		"disconnect":  noErrorCommandHandler(b.handleDisconnect),
		"shuffle":     b.handleShuffle,
		"loop":        noErrorCommandHandler(b.handleLoop),
		"volume":      b.handleVolume,
		"seek":        b.handleSeek,
		"fseek":       b.handleFSeek,
		"move":        b.handleMove,
		"remove":      b.handleRemove,
		"config":      b.handleConfig,
	}
	handler, ok := handlers[data.Name]
	if !ok {
		b.respondError(s, i, fmt.Errorf("unknown command"))
		return
	}
	if err := handler(s, i); err != nil {
		b.respondError(s, i, err)
	}
}

// respondError sends an error response
func (b *Bot) respondError(s *discordgo.Session, i *discordgo.InteractionCreate, err error) {
	if s == nil || i == nil || i.Interaction == nil {
		logger.Error("Unable to respond to interaction error", "err", err)
		return
	}
	responseErr := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title:       "Command Failed",
					Description: err.Error(),
					Color:       0xDC2626,
				},
			},
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: noAllowedMentions(),
		},
	})
	if responseErr != nil {
		logger.Error("Failed to respond to interaction error", "err", responseErr, "command_err", err)
	}
}

func requireGuildMemberInteraction(i *discordgo.InteractionCreate) error {
	if i == nil || i.Interaction == nil || i.GuildID == "" || i.Member == nil || i.Member.User == nil || i.Member.User.ID == "" {
		return fmt.Errorf("this command can only be used in a server")
	}
	return nil
}

func interactionUserID(i *discordgo.InteractionCreate) (string, error) {
	if err := requireGuildMemberInteraction(i); err != nil {
		return "", err
	}
	return i.Member.User.ID, nil
}

func (b *Bot) authorizeApplicationCommand(i *discordgo.InteractionCreate, command string) error {
	userID, err := interactionUserID(i)
	if err != nil {
		return err
	}

	switch command {
	case "queue", "now-playing":
		return nil
	case "play":
		// handlePlay checks the caller's current voice channel before it does
		// any expensive work. There may not yet be a bot voice channel here.
		return nil
	case "config":
		options := i.ApplicationCommandData().Options
		if len(options) > 0 && options[0].Name == "show" {
			return nil
		}
		if !hasManageGuildPermission(i.Member) {
			return fmt.Errorf("you need Manage Server permission to change bot configuration")
		}
		if b.PlayerManager == nil {
			return fmt.Errorf("playback is unavailable")
		}
		if b.PlayerManager.GetPlayer(i.GuildID).IsVoiceConnected() {
			return b.requirePlaybackControlAccess(i.GuildID, userID)
		}
		return nil
	case "pause", "resume", "skip", "stop", "clear", "disconnect", "shuffle", "loop", "volume", "seek", "fseek", "move", "remove":
		return b.requirePlaybackControlAccess(i.GuildID, userID)
	default:
		return nil
	}
}

func hasManageGuildPermission(member *discordgo.Member) bool {
	if member == nil {
		return false
	}
	return member.Permissions&discordgo.PermissionAdministrator != 0 || member.Permissions&discordgo.PermissionManageGuild != 0
}
