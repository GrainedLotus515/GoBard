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

func TestAudienceIgnoresBotsAndOtherChannels(t *testing.T) {
	s := newGatewayState()
	bot := &discordgo.Member{User: &discordgo.User{ID: "200", Bot: true}}
	if err := s.updateGuild(&discordgo.Guild{ID: "1", Members: []*discordgo.Member{bot},
		VoiceStates: []*discordgo.VoiceState{{UserID: "100", ChannelID: "10"}, {UserID: "200", ChannelID: "10"}, {UserID: "300", ChannelID: "11"}}}); err != nil {
		t.Fatal(err)
	}
	if channel, alone, ok := s.audience("1", "100"); !ok || channel != "10" || !alone {
		t.Fatalf("with only a bot: channel=%q alone=%v ok=%v", channel, alone, ok)
	}
	// An unidentified member counts as a listener.
	if err := s.voice(&discordgo.VoiceState{GuildID: "1", UserID: "300", ChannelID: "10"}); err != nil {
		t.Fatal(err)
	}
	if _, alone, _ := s.audience("1", "100"); alone {
		t.Fatal("listener in channel reported alone")
	}
	if err := s.voice(&discordgo.VoiceState{GuildID: "1", UserID: "300", ChannelID: ""}); err != nil {
		t.Fatal(err)
	}
	if _, alone, _ := s.audience("1", "100"); !alone {
		t.Fatal("departed listener still counted")
	}
	if channel, alone, ok := s.audience("1", "999"); !ok || channel != "" || alone {
		t.Fatalf("not in voice: channel=%q alone=%v ok=%v", channel, alone, ok)
	}
	if _, _, ok := s.audience("2", "100"); ok {
		t.Fatal("unknown guild reported audience")
	}
}
