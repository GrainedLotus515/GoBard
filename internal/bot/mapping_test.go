package bot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

func interactionFixture(name string, options ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "guild", Member: &discordgo.Member{User: &discordgo.User{ID: "user"}}, Type: discordgo.InteractionApplicationCommand, Data: discordgo.ApplicationCommandInteractionData{Name: name, Options: options}}}
}
func intOption(name string, value float64) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionInteger, Value: value}
}
func stringOption(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionString, Value: value}
}

func TestMapEverySlashCommand(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options []*discordgo.ApplicationCommandInteractionDataOption
		action  session.Action
	}{
		{"play", []*discordgo.ApplicationCommandInteractionDataOption{stringOption("query", "test music")}, session.Inspect},
		{"queue", nil, session.Inspect}, {"now-playing", nil, session.Inspect},
		{"pause", nil, session.Pause}, {"resume", nil, session.Resume}, {"skip", nil, session.Skip}, {"stop", nil, session.Stop},
		{"disconnect", nil, session.Disconnect}, {"clear", nil, session.Clear}, {"shuffle", nil, session.Shuffle}, {"loop", nil, session.Loop},
		{"volume", []*discordgo.ApplicationCommandInteractionDataOption{intOption("level", 70)}, session.Volume},
		{"seek", []*discordgo.ApplicationCommandInteractionDataOption{stringOption("position", "1:30")}, session.Seek},
		{"fseek", []*discordgo.ApplicationCommandInteractionDataOption{intOption("seconds", 30)}, session.ForwardSeek},
		{"move", []*discordgo.ApplicationCommandInteractionDataOption{intOption("to", 3), intOption("from", 2)}, session.Move},
		{"remove", []*discordgo.ApplicationCommandInteractionDataOption{intOption("position", 2)}, session.Remove},
		{"config", []*discordgo.ApplicationCommandInteractionDataOption{{Name: "show", Type: discordgo.ApplicationCommandOptionSubCommand}}, session.Inspect},
		{"config", []*discordgo.ApplicationCommandInteractionDataOption{{Name: "set-reduce-vol-when-voice", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "enabled", Type: discordgo.ApplicationCommandOptionBoolean, Value: true}}}}, session.Ducking},
		{"config", []*discordgo.ApplicationCommandInteractionDataOption{{Name: "set-reduce-vol-when-voice-target", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{intOption("volume", 25)}}}, session.DuckTarget},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MapInteraction(interactionFixture(tt.name, tt.options...))
			if err != nil || got.Operation.Action != tt.action {
				t.Fatalf("mapped = %+v, %v", got, err)
			}
			if tt.name == "move" && (got.Operation.Position != 1 || got.Operation.To != 2) {
				t.Fatal("one-based queue conversion lost")
			}
		})
	}
}

func TestMapMalformedInteractionsWithoutPanics(t *testing.T) {
	dm := interactionFixture("queue")
	dm.GuildID = ""
	wrongData := interactionFixture("queue")
	wrongData.Data = discordgo.MessageComponentInteractionData{}
	for _, input := range []*discordgo.InteractionCreate{
		nil, {}, dm, wrongData, interactionFixture("missing"), interactionFixture("play"), interactionFixture("volume", nil),
		interactionFixture("pause", stringOption("unexpected", "value")),
		interactionFixture("play", stringOption("query", "a"), stringOption("query", "b")),
		interactionFixture("volume", stringOption("level", "100")),
		interactionFixture("volume", intOption("level", math.NaN())),
		interactionFixture("volume", intOption("level", math.Inf(1))),
		interactionFixture("volume", intOption("level", 10.5)),
		interactionFixture("fseek", intOption("seconds", math.MaxFloat64)),
		interactionFixture("config", &discordgo.ApplicationCommandInteractionDataOption{Name: "set-reduce-vol-when-voice", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{nil}}),
	} {
		if got, err := MapInteraction(input); err == nil {
			t.Errorf("malformed input accepted: %+v", got)
		}
	}
}

func TestMapComponents(t *testing.T) {
	for id, action := range map[string]session.Action{"music:playback:toggle_pause": session.TogglePause, "music:playback:skip": session.Skip, "music:playback:stop": session.Stop, "music:playback:toggle_loop": session.Loop, "music:queue:page:2": session.Inspect} {
		i := interactionFixture("")
		i.Type = discordgo.InteractionMessageComponent
		i.Data = discordgo.MessageComponentInteractionData{CustomID: id}
		mapped, err := MapInteraction(i)
		if err != nil || !mapped.Component || mapped.Operation.Action != action {
			t.Fatalf("component %s = %+v %v", id, mapped, err)
		}
	}
}

type mappingOpener struct{}

func (mappingOpener) Open(context.Context, playback.Request) (playback.Stream, error) {
	return nil, errors.New("not used")
}

type mappingOutput struct{}

func (mappingOutput) Send(context.Context, []byte) error { return nil }

func TestFakeInteractionAppliesRealSessionPermissionContract(t *testing.T) {
	engine, err := playback.New(mappingOpener{}, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	target, err := session.New(context.Background(), "guild", engine, mappingOutput{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if _, err := target.Apply(context.Background(), session.Operation{Action: session.Connect, Channel: "voice", Output: mappingOutput{}}); err != nil {
		t.Fatal(err)
	}
	i := interactionFixture("volume", intOption("level", 25))
	input, err := MapInteraction(i)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyAs(context.Background(), session.Access{Channel: "other", ManageGuild: input.ManageGuild}, input.Operation); !errors.Is(err, session.ErrVoiceAccess) {
		t.Fatalf("wrong channel = %v", err)
	}
	snap, err := target.ApplyAs(context.Background(), session.Access{Channel: "voice"}, input.Operation)
	if err != nil || snap.Volume != 25 {
		t.Fatalf("volume = %d %v", snap.Volume, err)
	}
	// The same core boundary blocks configuration without Manage Guild.
	i = interactionFixture("config", &discordgo.ApplicationCommandInteractionDataOption{Name: "set-reduce-vol-when-voice", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "enabled", Type: discordgo.ApplicationCommandOptionBoolean, Value: true}}})
	input, err = MapInteraction(i)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyAs(context.Background(), session.Access{Channel: "voice", ManageGuild: input.ManageGuild}, input.Operation); !errors.Is(err, session.ErrManageGuild) {
		t.Fatalf("configuration permission = %v", err)
	}
	i.Member.Permissions = discordgo.PermissionManageGuild
	input, err = MapInteraction(i)
	if err != nil {
		t.Fatal(err)
	}
	snap, err = target.ApplyAs(context.Background(), session.Access{Channel: "voice", ManageGuild: input.ManageGuild}, input.Operation)
	if err != nil || !snap.Ducking {
		t.Fatalf("configuration not applied: %+v %v", snap, err)
	}
}

func TestSeekGrammarAndBounds(t *testing.T) {
	for _, value := range []string{"1:30", "90", "90s", "1m30s"} {
		got, err := parseSeekPosition(value)
		if err != nil || got != 90*time.Second {
			t.Fatalf("%s = %v %v", value, got, err)
		}
	}
	for _, value := range []string{"-1", "-1s", "1:90", "99999999999999999999:00", "999999h", ""} {
		if _, err := parseSeekPosition(value); err == nil {
			t.Errorf("invalid seek %s accepted", value)
		}
	}
}

func FuzzMapInteraction(f *testing.F) {
	f.Add([]byte(`{"type":2,"guild_id":"1","member":{"user":{"id":"2"}},"data":{"name":"volume","options":[{"name":"level","type":4,"value":50}]}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 16384 {
			return
		}
		var interaction discordgo.Interaction
		if json.Unmarshal(data, &interaction) != nil {
			return
		}
		if _, err := MapInteraction(&discordgo.InteractionCreate{Interaction: &interaction}); err != nil {
			return
		}
	})
}

func TestMapPlayerChannelSetting(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		option := &discordgo.ApplicationCommandInteractionDataOption{Name: "enabled", Type: discordgo.ApplicationCommandOptionBoolean, Value: enabled}
		i := interactionFixture("config", &discordgo.ApplicationCommandInteractionDataOption{Name: "set-player-channel", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{option}})
		i.ChannelID = "music"
		got, err := MapInteraction(i)
		if err != nil || !got.PlayerChannelSetting || got.AnchorPlayer != enabled {
			t.Fatal(got, err)
		}
		option.Value = "true"
		if _, err := MapInteraction(i); err == nil {
			t.Fatal("invalid setting accepted")
		}
	}
}
