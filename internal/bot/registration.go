package bot

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	registrationInitialBackoff = 5 * time.Second
	registrationMaximumBackoff = 5 * time.Minute
)

func (r *Runtime) registrationLoop() {
	var retry <-chan time.Time
	var stopRetry func()
	defer func() {
		if stopRetry != nil {
			stopRetry()
		}
	}()
	failures := 0
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.register:
			if retry != nil {
				continue
			}
		case <-retry:
			retry = nil
			stopRetry = nil
		}
		if !r.connected.Load() {
			r.registered.Store(false)
			failures = 0
			continue
		}
		r.registered.Store(false)
		// Coalesce the initial burst of GuildCreate events without retaining
		// another guild registry. IDs are copied from the SDK under its lock.
		if !waitContext(r.ctx, 250*time.Millisecond) {
			return
		}
		select {
		case <-r.register:
		default:
		}
		r.discord.State.RLock()
		app := ""
		if r.discord.State.User != nil {
			app = r.discord.State.User.ID
		}
		r.discord.State.RUnlock()
		guilds := r.state.guildIDs()
		if !validPathPart(app, 32) {
			guilds = nil
		} else if r.cfg.RegisterGlobally {
			guilds = []string{""}
		}
		ok := validPathPart(app, 32)
		var providerDelay time.Duration
		for _, guild := range guilds {
			retryAfter, success := r.registerGuild(app, guild)
			if !success {
				ok = false
			}
			providerDelay = max(providerDelay, retryAfter)
			if r.ctx.Err() != nil {
				return
			}
		}
		r.registered.Store(ok)
		if ok {
			failures = 0
			r.logger.Info("Commands reconciled", "event", "discord.commands", "guilds", len(guilds))
		} else {
			failures++
			delay := max(registrationBackoff(failures), providerDelay)
			retry, stopRetry = r.registrationTimer(delay)
			r.logger.Warn("Command reconciliation retry scheduled", "event", "discord.commands", "recovery", "retry", "pass", failures, "retry_after_seconds", delay.Seconds())
		}
		r.presence()
	}
}

func registrationBackoff(failures int) time.Duration {
	delay := registrationInitialBackoff
	for pass := 1; pass < failures && delay < registrationMaximumBackoff; pass++ {
		delay = min(delay*2, registrationMaximumBackoff)
	}
	return delay
}

func (r *Runtime) registerGuild(app, guild string) (time.Duration, bool) {
	path := "applications/" + app + "/commands"
	if guild != "" {
		if !validPathPart(guild, 32) {
			return 0, false
		}
		path = "applications/" + app + "/guilds/" + guild + "/commands"
	}
	ctx, cancel := context.WithTimeout(r.ctx, 8*time.Second)
	err := r.rest.send(ctx, http.MethodPut, path, commandDefinitions(), true)
	cancel()
	if err == nil {
		return 0, true
	}
	var retryAfter time.Duration
	var status *discordStatus
	if errors.As(err, &status) && status.code == http.StatusTooManyRequests {
		retryAfter = status.retryAfter
	}
	r.logger.Warn("Command registration failed", "event", "discord.commands", "guild_id", guild, "recovery", "scheduled", "retry_after_seconds", retryAfter.Seconds(), "error", err)
	return retryAfter, false
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
