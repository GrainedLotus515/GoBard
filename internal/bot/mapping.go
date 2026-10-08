package bot

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/session"
)

// MappedInteraction is an adapter-owned command value. SDK objects never cross
// into command/session packages. Name is one of the registered command names;
// Operation is applied through Session.ApplyAs, not the trusted lifecycle API.
type MappedInteraction struct {
	Guild, User string
	Name        string
	Query       string
	Operation   session.Operation
	ManageGuild bool
	Component   bool
	Page        int
}

// MapInteraction validates the full option grammar before calling any SDK value
// helpers (which panic on mismatched types). Returned errors contain no input.
func MapInteraction(i *discordgo.InteractionCreate) (MappedInteraction, error) {
	var out MappedInteraction
	if err := requireGuildMemberInteraction(i); err != nil {
		return out, err
	}
	if len(i.GuildID) > 32 || len(i.Member.User.ID) > 32 {
		return out, errors.New("invalid interaction identity")
	}
	out.Guild, out.User, out.ManageGuild = i.GuildID, i.Member.User.ID, hasManageGuildPermission(i.Member)
	out.Operation.Action = session.Inspect
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		data, ok := i.Data.(discordgo.MessageComponentInteractionData)
		if !ok || len(data.CustomID) > 100 {
			return out, errors.New("invalid component")
		}
		action, meta, err := botui.ParseCustomID(data.CustomID)
		if err != nil {
			return out, errors.New("unrecognized control")
		}
		out.Component = true
		switch action {
		case botui.ActionPlaybackTogglePause:
			out.Name = "pause"
			out.Operation.Action = session.TogglePause
		case botui.ActionPlaybackSkip:
			out.Name = "skip"
			out.Operation.Action = session.Skip
		case botui.ActionPlaybackToggleLoop:
			out.Name = "loop"
			out.Operation.Action = session.Loop
		case botui.ActionPlaybackStop:
			out.Name = "stop"
			out.Operation.Action = session.Stop
		case botui.ActionQueuePage:
			out.Name = "queue"
			out.Page = meta.Page
		default:
			return out, errors.New("unrecognized control")
		}
		return out, nil
	case discordgo.InteractionApplicationCommand:
		data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
		if !ok {
			return out, errors.New("invalid command data")
		}
		out.Name = data.Name
		return mapOptions(out, data.Options)
	default:
		return out, errors.New("unsupported interaction")
	}
}

func mapOptions(out MappedInteraction, options []*discordgo.ApplicationCommandInteractionDataOption) (MappedInteraction, error) {
	if len(options) > 2 {
		return out, errors.New("invalid command options")
	}
	byName := make(commandOptions, len(options))
	for _, option := range options {
		if option == nil || len(option.Name) > 40 || byName[option.Name] != nil {
			return out, errors.New("invalid command options")
		}
		byName[option.Name] = option
	}
	if out.Name == "config" {
		if len(options) != 1 || options[0].Type != discordgo.ApplicationCommandOptionSubCommand {
			return out, errors.New("invalid config subcommand")
		}
		return mapConfiguration(out, options[0])
	}
	out, err := mapCommandOptions(out, byName)
	if err != nil {
		return out, err
	}
	if len(byName) != 0 {
		return out, errors.New("unexpected command option")
	}
	return out, nil
}

type commandOptions map[string]*discordgo.ApplicationCommandInteractionDataOption

func (options commandOptions) text(name string) (string, error) {
	o := options[name]
	if o == nil || o.Type != discordgo.ApplicationCommandOptionString || len(o.Options) != 0 {
		return "", errors.New("invalid text option")
	}
	v, ok := o.Value.(string)
	if !ok || len(v) == 0 || len(v) > 512 || !utf8.ValidString(v) {
		return "", errors.New("invalid text option")
	}
	delete(options, name)
	return v, nil
}

func (options commandOptions) integer(name string, minValue, maxValue int) (int, error) {
	o := options[name]
	if o == nil || o.Type != discordgo.ApplicationCommandOptionInteger || len(o.Options) != 0 {
		return 0, errors.New("invalid integer option")
	}
	// Discord JSON numbers decode as float64. Enforce exact integer bounds
	// before conversion to avoid overflow, NaN and fractional coercion.
	v, ok := o.Value.(float64)
	if !ok || math.IsNaN(v) || v < float64(minValue) || v > float64(maxValue) || math.Trunc(v) != v {
		return 0, errors.New("integer option outside bounds")
	}
	delete(options, name)
	return int(v), nil
}

func mapCommandOptions(out MappedInteraction, options commandOptions) (MappedInteraction, error) {
	var err error
	switch out.Name {
	case "play":
		out.Query, err = options.text("query")
	case "queue", "now-playing":
	case "pause":
		out.Operation.Action = session.Pause
	case "resume":
		out.Operation.Action = session.Resume
	case "skip":
		out.Operation.Action = session.Skip
	case "stop":
		out.Operation.Action = session.Stop
	case "disconnect":
		out.Operation.Action = session.Disconnect
	case "clear":
		out.Operation.Action = session.Clear
	case "shuffle":
		out.Operation.Action = session.Shuffle
	case "loop":
		out.Operation.Action = session.Loop
	case "volume":
		out.Operation.Action = session.Volume
		out.Operation.Position, err = options.integer("level", 0, 100)
	case "remove":
		out.Operation.Action = session.Remove
		out.Operation.Position, err = options.integer("position", 1, session.MaxQueue)
		out.Operation.Position--
	case "move":
		out.Operation.Action = session.Move
		out.Operation.Position, err = options.integer("from", 1, session.MaxQueue)
		if err == nil {
			out.Operation.To, err = options.integer("to", 1, session.MaxQueue)
		}
		out.Operation.Position--
		out.Operation.To--
	case "seek":
		out.Operation.Action = session.Seek
		var value string
		value, err = options.text("position")
		if err == nil {
			out.Operation.Offset, err = parseSeekPosition(value)
		}
	case "fseek":
		out.Operation.Action = session.ForwardSeek
		var seconds int
		seconds, err = options.integer("seconds", 0, 7*24*3600)
		out.Operation.Offset = time.Duration(seconds) * time.Second
	default:
		return out, errors.New("unknown command")
	}
	return out, err
}

func mapConfiguration(out MappedInteraction, sub *discordgo.ApplicationCommandInteractionDataOption) (MappedInteraction, error) {
	switch sub.Name {
	case "show":
		if len(sub.Options) != 0 {
			return out, errors.New("unexpected configuration options")
		}
	case "set-reduce-vol-when-voice":
		if len(sub.Options) != 1 || sub.Options[0] == nil {
			return out, errors.New("invalid configuration options")
		}
		o := sub.Options[0]
		value, ok := o.Value.(bool)
		if o.Name != "enabled" || o.Type != discordgo.ApplicationCommandOptionBoolean || !ok || len(o.Options) != 0 {
			return out, errors.New("invalid configuration value")
		}
		out.Operation = session.Operation{Action: session.Ducking, Enabled: value}
	case "set-reduce-vol-when-voice-target":
		if len(sub.Options) != 1 || sub.Options[0] == nil {
			return out, errors.New("invalid configuration options")
		}
		o := sub.Options[0]
		value, err := (commandOptions{o.Name: o}).integer("volume", 0, 100)
		if err != nil || o.Name != "volume" {
			return out, errors.New("invalid configuration value")
		}
		out.Operation = session.Operation{Action: session.DuckTarget, Position: value}
	default:
		return out, errors.New("unknown config subcommand")
	}
	return out, nil
}

func parseSeekPosition(value string) (time.Duration, error) {
	invalid := errors.New("invalid seek position")
	if len(value) > 64 {
		return 0, invalid
	}
	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return 0, invalid
		}
		minutes, err := strconv.ParseUint(parts[0], 10, 32)
		if err != nil {
			return 0, invalid
		}
		seconds, err := strconv.ParseUint(parts[1], 10, 32)
		if err != nil || minutes > 7*24*60 || seconds >= 60 {
			return 0, invalid
		}
		value = strconv.FormatUint(minutes*60+seconds, 10) + "s"
	} else if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		value = strconv.FormatUint(seconds, 10) + "s"
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 || duration > 7*24*time.Hour {
		return 0, invalid
	}
	return duration, nil
}
