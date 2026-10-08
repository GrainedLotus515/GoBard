package bot

import (
	"context"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
)

func (r *Runtime) registrationLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.register:
		}
		if !r.connected.Load() {
			continue
		}
		r.registered.Store(false)
		// Coalesce the initial burst of GuildCreate events without retaining
		// another guild registry. IDs are copied from the SDK under its lock.
		if !waitContext(r.ctx, 250*time.Millisecond) {
			return
		}
		r.discord.State.RLock()
		app := ""
		if r.discord.State.User != nil {
			app = r.discord.State.User.ID
		}
		guilds := make([]string, 0, len(r.discord.State.Guilds))
		for _, g := range r.discord.State.Guilds {
			if g != nil {
				guilds = append(guilds, g.ID)
			}
		}
		r.discord.State.RUnlock()
		if !validPathPart(app, 32) {
			continue
		}
		if r.cfg.RegisterGlobally {
			guilds = []string{""}
		}
		ok := true
		for _, guild := range guilds {
			if !r.registerGuild(app, guild) {
				ok = false
			}
			if r.ctx.Err() != nil {
				return
			}
		}
		r.registered.Store(ok)
		if ok {
			r.logger.Info("Commands reconciled", "event", "discord.commands", "guilds", len(guilds))
		}
		r.presence()
	}
}

func (r *Runtime) registerGuild(app, guild string) bool {
	path := "applications/" + app + "/commands"
	if guild != "" {
		if !validPathPart(guild, 32) {
			return false
		}
		path = "applications/" + app + "/guilds/" + guild + "/commands"
	}
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(r.ctx, 8*time.Second)
		err := r.rest.send(ctx, http.MethodPut, path, commandDefinitions(), true)
		cancel()
		if err == nil {
			return true
		}
		r.logger.Warn("Command registration failed", "event", "discord.commands", "guild_id", guild, "attempt", attempt, "error", err)
		if !waitContext(r.ctx, time.Duration(attempt)*time.Second) {
			return false
		}
	}
	return false
}

func (r *Runtime) presence() {
	types := map[string]discordgo.ActivityType{"PLAYING": discordgo.ActivityTypeGame, "STREAMING": discordgo.ActivityTypeStreaming, "LISTENING": discordgo.ActivityTypeListening, "WATCHING": discordgo.ActivityTypeWatching, "COMPETING": discordgo.ActivityTypeCompeting}
	// GatewayWriteStruct is the SDK's nil-safe, serialized capability.
	err := r.discord.GatewayWriteStruct(struct {
		Op   int                        `json:"op"`
		Data discordgo.UpdateStatusData `json:"d"`
	}{3, discordgo.UpdateStatusData{Status: r.cfg.BotStatus, Activities: []*discordgo.Activity{{Name: r.cfg.BotActivity, Type: types[r.cfg.BotActivityType], URL: r.cfg.BotActivityURL}}}})
	if err != nil && r.ctx.Err() == nil {
		r.logger.Warn("Presence update failed", "event", "discord.presence", "category", "transport")
	}
}
