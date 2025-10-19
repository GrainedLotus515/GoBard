package bot

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/lotus/gobard/internal/logger"
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

	if b.Config.RegisterGlobally {
		// Register globally
		logger.Info("📝 Registering commands globally...")
		for _, cmd := range commands {
			_, err := b.Session.ApplicationCommandCreate(b.Session.State.User.ID, "", cmd)
			if err != nil {
				return fmt.Errorf("failed to create command %s: %w", cmd.Name, err)
			}
		}
	} else {
		// Register for each guild
		logger.Info("📝 Registering commands per guild...")
		guilds := b.Session.State.Guilds
		for _, guild := range guilds {
			for _, cmd := range commands {
				_, err := b.Session.ApplicationCommandCreate(b.Session.State.User.ID, guild.ID, cmd)
				if err != nil {
					logger.Error("Failed to create command", "cmd", cmd.Name, "guild", guild.ID, "err", err)
				}
			}
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
