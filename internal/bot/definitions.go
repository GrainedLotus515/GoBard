package bot

import "github.com/bwmarrin/discordgo"

func commandDefinitions() []*discordgo.ApplicationCommand {
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
					MinValue:    func() *float64 { v := 0.0; return &v }(),
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
					Name:        "set-player-channel",
					Description: "Keep the player in this text channel, or follow play requests",
					Options:     []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionBoolean, Name: "enabled", Description: "Anchor here when enabled; follow play requests when disabled", Required: true}},
				},
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

	contexts := []discordgo.InteractionContextType{discordgo.InteractionContextGuild}
	for _, command := range commands {
		command.Contexts = &contexts
	}
	return commands
}
