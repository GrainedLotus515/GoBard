package bot

import (
	"errors"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

const maxGatewayGuilds = 1024
const maxGuildVoiceStates = 1024
const maxGatewayVoiceStates = 16384

type observedGuild struct {
	voices   map[string]string
	complete bool
}

// gatewayState retains only authorization observations, never full SDK guilds,
// channels, members or message history. Over-budget snapshots fail closed.
type gatewayState struct {
	mu     sync.Mutex
	guilds map[string]*observedGuild
	voices int
}

func newGatewayState() *gatewayState { return &gatewayState{guilds: make(map[string]*observedGuild)} }

func (s *gatewayState) ready(guilds []*discordgo.Guild) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.guilds)
	s.voices = 0
	for _, g := range guilds {
		if g != nil && validPathPart(g.ID, 32) {
			if len(s.guilds) >= maxGatewayGuilds {
				return errors.New("gateway guild budget exceeded")
			}
			s.guilds[strings.Clone(g.ID)] = &observedGuild{}
		}
	}
	return nil
}

func (s *gatewayState) updateGuild(g *discordgo.Guild) error {
	if g == nil || !validPathPart(g.ID, 32) {
		return errors.New("invalid guild event")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.guilds[g.ID]
	if !ok {
		if len(s.guilds) >= maxGatewayGuilds {
			return errors.New("gateway guild budget exceeded")
		}
		old = &observedGuild{}
		s.guilds[strings.Clone(g.ID)] = old
	}
	s.voices -= len(old.voices)
	old.voices = nil
	old.complete = false
	if g.Unavailable {
		return nil
	}
	if len(g.VoiceStates) > maxGuildVoiceStates || s.voices+len(g.VoiceStates) > maxGatewayVoiceStates {
		return errors.New("gateway voice-state budget exceeded")
	}
	old.voices = make(map[string]string, len(g.VoiceStates))
	for _, v := range g.VoiceStates {
		if v != nil && validPathPart(v.UserID, 32) && validPathPart(v.ChannelID, 32) {
			old.voices[strings.Clone(v.UserID)] = strings.Clone(v.ChannelID)
		}
	}
	s.voices += len(old.voices)
	old.complete = true
	return nil
}

func (s *gatewayState) voice(v *discordgo.VoiceState) error {
	if v == nil || !validPathPart(v.GuildID, 32) || !validPathPart(v.UserID, 32) || v.ChannelID != "" && !validPathPart(v.ChannelID, 32) {
		return errors.New("invalid voice-state event")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guilds[v.GuildID]
	if !ok || !g.complete {
		return errors.New("guild voice state unavailable")
	}
	_, exists := g.voices[v.UserID]
	if v.ChannelID == "" {
		if exists {
			delete(g.voices, v.UserID)
			s.voices--
		}
		return nil
	}
	if !exists {
		if len(g.voices) >= maxGuildVoiceStates || s.voices >= maxGatewayVoiceStates {
			return errors.New("gateway voice-state budget exceeded")
		}
		s.voices++
	}
	g.voices[strings.Clone(v.UserID)] = strings.Clone(v.ChannelID)
	return nil
}

func (s *gatewayState) access(guild, user string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guilds[guild]
	if !ok || !g.complete {
		return "", errors.New("guild voice state unavailable")
	}
	return g.voices[user], nil
}
func (s *gatewayState) guildIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.guilds))
	for id := range s.guilds {
		ids = append(ids, id)
	}
	return ids
}
func (s *gatewayState) remove(guild string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.guilds[guild]; g != nil {
		s.voices -= len(g.voices)
		delete(s.guilds, guild)
	}
}
