package discordvoice

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/charmbracelet/log"
)

func TestClosedManagerRejectsNewVoiceWork(t *testing.T) {
	s, err := discordgo.New("Bot fixture")
	if err != nil {
		t.Fatal(err)
	}
	s.State.User = &discordgo.User{ID: "123"}
	m, err := NewManager(context.Background(), s, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	m.Close(context.Background())
	m.Close(context.Background())
	if _, err = m.Join(context.Background(), "456", "789", false, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

//nolint:gocyclo // Each assertion documents a distinct field preserved by the adapter.
func TestToVoiceStateUpdate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	got, err := toVoiceStateUpdate(&discordgo.VoiceStateUpdate{
		VoiceState: &discordgo.VoiceState{
			GuildID:                 "1",
			ChannelID:               "2",
			UserID:                  "3",
			SessionID:               "session-id",
			Deaf:                    true,
			Mute:                    true,
			SelfDeaf:                true,
			SelfMute:                true,
			SelfStream:              true,
			SelfVideo:               true,
			Suppress:                true,
			RequestToSpeakTimestamp: &now,
		},
	})
	if err != nil {
		t.Fatalf("toVoiceStateUpdate() error = %v", err)
	}

	if got.GuildID.String() != "1" {
		t.Fatalf("GuildID = %s, want 1", got.GuildID)
	}
	if got.ChannelID == nil || got.ChannelID.String() != "2" {
		t.Fatalf("ChannelID = %v, want 2", got.ChannelID)
	}
	if got.UserID.String() != "3" {
		t.Fatalf("UserID = %s, want 3", got.UserID)
	}
	if got.SessionID != "session-id" {
		t.Fatalf("SessionID = %q, want session-id", got.SessionID)
	}
	if !got.GuildDeaf || !got.GuildMute || !got.SelfDeaf || !got.SelfMute || !got.SelfStream || !got.SelfVideo || !got.Suppress {
		t.Fatal("expected all voice state flags to be preserved")
	}
	if got.RequestToSpeakTimestamp == nil || !got.RequestToSpeakTimestamp.Equal(now) {
		t.Fatalf("RequestToSpeakTimestamp = %v, want %v", got.RequestToSpeakTimestamp, now)
	}
}

func TestToVoiceServerUpdate(t *testing.T) {
	endpoint := "voice.example.com"

	got, err := toVoiceServerUpdate(&discordgo.VoiceServerUpdate{
		Token:    "token",
		GuildID:  "42",
		Endpoint: &endpoint,
	})
	if err != nil {
		t.Fatalf("toVoiceServerUpdate() error = %v", err)
	}

	if got.Token != "token" {
		t.Fatalf("Token = %q, want token", got.Token)
	}
	if got.GuildID.String() != "42" {
		t.Fatalf("GuildID = %s, want 42", got.GuildID)
	}
	if got.Endpoint == nil || *got.Endpoint != endpoint {
		t.Fatalf("Endpoint = %v, want %q", got.Endpoint, endpoint)
	}
}

func TestToVoiceStateUpdateRejectsInvalidIDs(t *testing.T) {
	_, err := toVoiceStateUpdate(&discordgo.VoiceStateUpdate{
		VoiceState: &discordgo.VoiceState{
			GuildID: "invalid",
			UserID:  "3",
		},
	})
	if err == nil {
		t.Fatal("toVoiceStateUpdate() error = nil, want parse failure")
	}
}

func TestCleanupLeaveClaimsOneAcknowledgementWithinWindow(t *testing.T) {
	m := &Manager{}
	if m.ConsumeCleanupLeave("1") {
		t.Fatal("unmarked empty voice state treated as own cleanup")
	}
	m.markCleanupLeave("1")
	if m.ConsumeCleanupLeave("2") {
		t.Fatal("cleanup mark leaked across guilds")
	}
	if !m.ConsumeCleanupLeave("1") {
		t.Fatal("own cleanup acknowledgement not recognized")
	}
	if m.ConsumeCleanupLeave("1") {
		t.Fatal("one cleanup claimed a second empty voice state (a later removal)")
	}
	m.markCleanupLeave("1")
	m.cleanupLeaves["1"] = time.Now().Add(-time.Millisecond)
	if m.ConsumeCleanupLeave("1") {
		t.Fatal("stale cleanup mark hid a removal")
	}
	var none *Manager
	if none.ConsumeCleanupLeave("1") {
		t.Fatal("nil manager claimed a cleanup")
	}
}
