package bot

import (
	"context"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/player"
	"github.com/GrainedLotus515/gobard/internal/voiceconn"
)

func TestGetDisplayPositionClampsForNonLiveTracks(t *testing.T) {
	track := &player.Track{
		Duration: 2 * time.Minute,
		IsLive:   false,
	}

	got := getDisplayPosition(track, 3*time.Minute)
	if got != 2*time.Minute {
		t.Fatalf("getDisplayPosition() = %v, want %v", got, 2*time.Minute)
	}
}

func TestGetDisplayPositionDoesNotClampLiveTracks(t *testing.T) {
	track := &player.Track{
		Duration: 2 * time.Minute,
		IsLive:   true,
	}

	got := getDisplayPosition(track, 3*time.Minute)
	if got != 3*time.Minute {
		t.Fatalf("getDisplayPosition() = %v, want %v", got, 3*time.Minute)
	}
}

func TestHandleRemoveDisconnectsAfterRemovingOnlyCurrentTrack(t *testing.T) {
	manager := player.NewManager()
	p := manager.GetPlayer("guild")
	connection := &countingVoiceConnection{}
	p.SetVoiceConnection(connection)
	p.Queue.Add(&player.Track{Title: "only track", URL: "https://www.youtube.com/watch?v=abc123XYZ89"})
	if p.Queue.Next() == nil {
		t.Fatal("Queue.Next() = nil, want current track")
	}

	b := &Bot{PlayerManager: manager}
	interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: "guild",
		Data: discordgo.ApplicationCommandInteractionData{
			Options: []*discordgo.ApplicationCommandInteractionDataOption{{
				Name:  "position",
				Type:  discordgo.ApplicationCommandOptionInteger,
				Value: 1.0,
			}},
		},
	}}

	if err := b.handleRemove(nil, interaction); err != nil {
		t.Fatalf("handleRemove() error = %v", err)
	}
	if p.IsVoiceConnected() {
		t.Fatal("player remains connected after removing its only current track")
	}
	if connection.disconnects != 1 {
		t.Fatalf("Disconnect() calls = %d, want 1", connection.disconnects)
	}
}

func TestHandlePlayStopCancelsBlockedJoinWithoutPublishingState(t *testing.T) {
	manager := player.NewManager()
	joinStarted := make(chan struct{})
	connection := &countingVoiceConnection{}
	b := &Bot{PlayerManager: manager}
	b.deferInteractionFn = func(*discordgo.Session, *discordgo.InteractionCreate) error { return nil }
	b.getVoiceChannelFn = func(string, string) (string, error) { return "voice", nil }
	b.joinVoiceChannelContextFn = func(ctx context.Context, _, _ string) (voiceconn.Connection, error) {
		close(joinStarted)
		<-ctx.Done()
		// Model a late join completion after cancellation. handlePlay must clean
		// it instead of publishing it to the guild player.
		return connection, nil
	}

	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand, GuildID: "guild", Member: &discordgo.Member{User: &discordgo.User{ID: "user"}},
		Data: discordgo.ApplicationCommandInteractionData{Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "query", Type: discordgo.ApplicationCommandOptionString, Value: "https://www.youtube.com/watch?v=abc123XYZ89"}}},
	}}
	done := make(chan error, 1)
	go func() { done <- b.handlePlay(nil, i) }()
	select {
	case <-joinStarted:
	case <-time.After(time.Second):
		t.Fatal("join did not begin")
	}
	if err := b.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handlePlay did not return after Stop")
	}
	p := manager.GetPlayer("guild")
	if p.IsVoiceConnected() || p.Queue.Length() != 0 || connection.disconnects != 1 {
		t.Fatalf("late canceled join state: connected=%v queue=%d disconnects=%d", p.IsVoiceConnected(), p.Queue.Length(), connection.disconnects)
	}
}

type countingVoiceConnection struct {
	disconnects int
}

func (*countingVoiceConnection) SetSpeaking(context.Context, bool) error { return nil }
func (*countingVoiceConnection) SendOpusFrame([]byte) error              { return nil }
func (c *countingVoiceConnection) Disconnect(context.Context) error {
	c.disconnects++
	return nil
}
