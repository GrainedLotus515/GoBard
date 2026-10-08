package bot

import (
	"fmt"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestGatewayStateBudgetsFailClosedAndDetachPayload(t *testing.T) {
	s := newGatewayState()
	g := &discordgo.Guild{ID: "guild", VoiceStates: []*discordgo.VoiceState{{UserID: "user", ChannelID: "voice"}}}
	if err := s.updateGuild(g); err != nil {
		t.Fatal(err)
	}
	g.VoiceStates[0].ChannelID = "mutated"
	if channel, err := s.access("guild", "user"); err != nil || channel != "voice" {
		t.Fatalf("retained external payload: %s %v", channel, err)
	}
	g.VoiceStates = make([]*discordgo.VoiceState, maxGuildVoiceStates+1)
	if err := s.updateGuild(g); err == nil {
		t.Fatal("oversized guild accepted")
	}
	if _, err := s.access("guild", "user"); err == nil {
		t.Fatal("stale authorization survived rejected snapshot")
	}
	for i := 1; i < maxGatewayGuilds; i++ {
		if err := s.updateGuild(&discordgo.Guild{ID: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.updateGuild(&discordgo.Guild{ID: "overflow"}); err == nil {
		t.Fatal("guild budget exceeded")
	}
	s.remove("guild")
	if err := s.updateGuild(&discordgo.Guild{ID: "replacement"}); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayStateConcurrentMovesAndRemoval(t *testing.T) {
	s := newGatewayState()
	if err := s.updateGuild(&discordgo.Guild{ID: "guild"}); err != nil {
		t.Fatal(err)
	}
	var work sync.WaitGroup
	for i := range 16 {
		work.Go(func() {
			user := fmt.Sprint(i)
			for range 50 {
				if err := s.voice(&discordgo.VoiceState{GuildID: "guild", UserID: user, ChannelID: "voice"}); err != nil {
					t.Error(err)
				}
				if channel, err := s.access("guild", user); err != nil || channel != "voice" {
					t.Errorf("access %s %v", channel, err)
				}
				if err := s.voice(&discordgo.VoiceState{GuildID: "guild", UserID: user}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	work.Wait()
	if s.voices != 0 {
		t.Fatalf("retained %d departed members", s.voices)
	}
	s.remove("guild")
	if _, err := s.access("guild", "1"); err == nil {
		t.Fatal("departed guild authorized")
	}
}

func TestGatewayStateGlobalVoiceBudget(t *testing.T) {
	s := newGatewayState()
	for g := range maxGatewayVoiceStates / maxGuildVoiceStates {
		guild := &discordgo.Guild{ID: fmt.Sprint(g), VoiceStates: make([]*discordgo.VoiceState, maxGuildVoiceStates)}
		for i := range guild.VoiceStates {
			guild.VoiceStates[i] = &discordgo.VoiceState{UserID: fmt.Sprint(i), ChannelID: "voice"}
		}
		if err := s.updateGuild(guild); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.updateGuild(&discordgo.Guild{ID: "overflow", VoiceStates: []*discordgo.VoiceState{{UserID: "user", ChannelID: "voice"}}}); err == nil {
		t.Fatal("global voice budget exceeded")
	}
	s.remove("0")
	if err := s.updateGuild(&discordgo.Guild{ID: "overflow", VoiceStates: []*discordgo.VoiceState{{UserID: "user", ChannelID: "voice"}}}); err != nil {
		t.Fatal(err)
	}
}
