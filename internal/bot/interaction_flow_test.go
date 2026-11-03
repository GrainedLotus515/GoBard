package bot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/media"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

type interactionSource struct{}
type interactionStream struct{ sent bool }

func (interactionSource) Open(context.Context, playback.Request) (playback.Stream, error) {
	return &interactionStream{}, nil
}
func (s *interactionStream) Next(ctx context.Context, packet []byte) (int, error) {
	if !s.sent {
		s.sent = true
		packet[0] = 1
		return 1, nil
	}
	<-ctx.Done()
	return 0, ctx.Err()
}
func (*interactionStream) Close() error { return nil }

type interactionResolver struct{}

// Read after the synchronous handler returns or the concurrent handlers join.
type interactionRecord struct {
	mu                      sync.Mutex
	lastCard, lastReceipt   discordgo.MessageEmbed
	publicMessages, private int
}

func (r *interactionRecord) respond(req *http.Request) (*http.Response, error) {
	var body struct {
		Embeds []discordgo.MessageEmbed `json:"embeds"`
		Flags  discordgo.MessageFlags   `json:"flags"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(body.Embeds) > 0 {
		if strings.Contains(req.URL.Path, "/channels/") || (strings.Contains(req.URL.Path, "@original") && body.Embeds[0].Author != nil) {
			r.lastCard = body.Embeds[0]
		} else {
			r.lastReceipt = body.Embeds[0]
		}
	}
	if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/channels/") {
		r.publicMessages++
	}
	if body.Flags&discordgo.MessageFlagsEphemeral != 0 {
		r.private++
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"panel"}`))}, nil
}

type interactionVoice struct{}

func (interactionVoice) Connect(context.Context, string) (playback.Output, error) {
	return mappingOutput{}, nil
}
func (interactionVoice) Disconnect(context.Context) error { return nil }

func (interactionResolver) Resolve(context.Context, string) ([]media.Track, error) {
	return []media.Track{{Provider: "fixture", MediaID: "one", Title: "First track", Duration: time.Minute}, {Provider: "fixture", MediaID: "two", Title: "Second track", Duration: time.Minute}}, nil
}

func TestInteractionFlowPlayConcurrentControlsAndStop(t *testing.T) {
	logger := log.New(io.Discard)
	engine, err := playback.New(interactionSource{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewManager(context.Background(), engine, 2, 2, 10, session.Defaults{Volume: 100})
	if err != nil {
		t.Fatal(err)
	}
	target, err := manager.CreateVoice("guild", interactionVoice{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(context.Background(), &config.Config{DiscordToken: "test", PlayCommandConcurrency: 2, MaxGuildSessions: 2}, manager, interactionResolver{}, logger)
	if err != nil {
		manager.Close()
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.state.updateGuild(&discordgo.Guild{ID: "guild", VoiceStates: []*discordgo.VoiceState{{UserID: "user", ChannelID: "voice"}}}); err != nil {
		t.Fatal(err)
	}
	record := &interactionRecord{}
	r.rest.client.Transport = httpRoundTrip(record.respond)
	execute := func(i *discordgo.InteractionCreate) {
		m, err := MapInteraction(i)
		if err != nil {
			t.Error(err)
			return
		}
		m.Channel = "channel"
		r.executeInteraction(interactionRef{id: "id", app: "app", token: "token"}, m)
	}
	execute(interactionFixture("play", stringOption("query", "fixture")))
	if record.lastCard.Title != "First track" || !strings.Contains(record.lastReceipt.Description, "2 tracks") {
		t.Fatalf("play receipt: %+v", record.lastCard)
	}
	button := func(id string) *discordgo.InteractionCreate {
		i := interactionFixture("pause")
		i.Type = discordgo.InteractionMessageComponent
		i.Data = discordgo.MessageComponentInteractionData{CustomID: id}
		i.Message = &discordgo.Message{ID: "panel"}
		return i
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); execute(button(string(botui.ActionPlaybackTogglePause))) }()
	}
	wg.Wait()
	s, err := target.Apply(context.Background(), session.Operation{Action: session.Inspect})
	if err != nil || s.Paused {
		t.Fatal("concurrent toggles lost an operation", err)
	}
	if record.lastCard.Author.Name == "Paused" {
		t.Fatal("older response overwrote current state")
	}
	execute(interactionFixture("play", stringOption("query", "more music")))
	execute(interactionFixture("queue"))
	execute(interactionFixture("now-playing"))
	if record.publicMessages != 1 || record.lastCard.Title != "First track" {
		t.Fatal("routine commands created duplicate channel cards")
	}
	// A different voice member cannot mutate the session or destroy the panel.
	if err = r.state.voice(&discordgo.VoiceState{GuildID: "guild", UserID: "user", ChannelID: "other"}); err != nil {
		t.Fatal(err)
	}
	execute(button(string(botui.ActionPlaybackStop)))
	if record.private != 1 || record.lastCard.Title != "First track" {
		t.Fatal("rejected control changed the player")
	}
	if err = r.state.voice(&discordgo.VoiceState{GuildID: "guild", UserID: "user", ChannelID: "voice"}); err != nil {
		t.Fatal(err)
	}
	execute(button(string(botui.ActionPlaybackStop)))
	if record.lastReceipt.Title != "Stopping playback" {
		t.Fatal("stop acknowledgement missing")
	}
	execute(interactionFixture("disconnect"))
	execute(interactionFixture("play", stringOption("query", "back again")))
	if record.publicMessages != 1 || record.lastCard.Title != "First track" {
		t.Fatal("disconnect/rejoin created a new card or failed to resume")
	}
	r.Close()
}
