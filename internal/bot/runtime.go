package bot

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/charmbracelet/log"
	"github.com/gorilla/websocket"

	"github.com/GrainedLotus515/gobard/internal/command"
	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/discordvoice"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/health"
	"github.com/GrainedLotus515/gobard/internal/session"
)

// Runtime owns the Discord boundary. Sessions and sources are injected by app
// composition and outlive Close. Event callbacks only admit bounded work.
type Runtime struct {
	ctx            context.Context
	cancel         context.CancelFunc
	cfg            *config.Config
	logger         *log.Logger
	discord        *discordgo.Session
	rest           *discordHTTP
	state          *gatewayState
	sessions       *session.Manager
	player         *command.Player
	voice          atomic.Pointer[discordvoice.Manager]
	connected      atomic.Bool
	registered     atomic.Bool
	sourceStatus   atomic.Uint32
	pipelineStatus atomic.Uint32
	delivering     atomic.Int32
	register       chan struct{}
	disconnected   chan struct{}
	slots          chan struct{}
	playSlots      chan struct{}
	rejections     chan interactionRef
	removals       chan string
	mu             sync.Mutex
	stopping       bool
	routes         map[string]string
	work           sync.WaitGroup
	once           sync.Once
}

func NewRuntime(parent context.Context, cfg *config.Config, sessions *session.Manager, resolver command.Resolver, logger *log.Logger) (*Runtime, error) {
	if parent == nil || cfg == nil || sessions == nil || resolver == nil || logger == nil {
		return nil, errors.New("invalid Discord runtime dependencies")
	}
	if cfg.PlayCommandConcurrency < 1 || cfg.PlayCommandConcurrency > 16 {
		return nil, errors.New("invalid play command budget")
	}
	// #nosec G118 -- Runtime.Close owns cancellation and joins admitted work.
	ctx, cancel := context.WithCancel(parent)
	d, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		cancel()
		return nil, failure.Wrap("discord_initialize", failure.Transport, err)
	}
	r := &Runtime{ctx: ctx, cancel: cancel, cfg: cfg, logger: logger, discord: d, rest: newDiscordHTTP(cfg.DiscordToken), state: newGatewayState(), removals: make(chan string, maxGatewayGuilds), sessions: sessions,
		register: make(chan struct{}, 1), disconnected: make(chan struct{}, 1), slots: make(chan struct{}, 32), playSlots: make(chan struct{}, cfg.PlayCommandConcurrency), rejections: make(chan interactionRef, 16), routes: make(map[string]string)}
	r.player, err = command.NewPlayer(ctx, resolver, r, logger, cfg.PlayCommandConcurrency)
	if err != nil {
		cancel()
		return nil, err
	}
	r.configureGateway()
	r.handlers()
	return r, nil
}

func (r *Runtime) configureGateway() {
	d := r.discord
	d.SyncEvents = true
	// The SDK caches whole guild payloads even with member/channel tracking
	// disabled. Retain only bounded authorization observations in our adapter.
	d.StateEnabled = false
	d.ShouldReconnectOnError = false
	d.ShouldRetryOnRateLimit = false
	d.MaxRestRetries = 0
	// SDK logging can include tokens and entire Gateway payloads. Boundary
	// operations report sanitized failures through Charm instead.
	d.LogLevel = -1
	d.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildVoiceStates
	d.State.TrackMembers, d.State.TrackRoles, d.State.TrackEmojis = false, false, false
	d.State.TrackChannels = false
	d.State.MaxMessageCount = 0
	d.Client = &http.Client{Timeout: 10 * time.Second}
	d.Dialer = &websocket.Dialer{HandshakeTimeout: 10 * time.Second, NetDialContext: func(_ context.Context, network, address string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
		defer cancel()
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return newGatewaySocket(r.ctx, conn), nil
	}}
}

// callback admits synchronous SDK work into the same shutdown barrier as
// commands. No new adapter resources can appear after Close starts waiting.
func (r *Runtime) callback(fn func()) {
	r.mu.Lock()
	if r.stopping || r.ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	r.work.Add(1)
	r.mu.Unlock()
	defer r.work.Done()
	fn()
}

func (r *Runtime) handlers() {
	r.discord.AddHandler(func(_ *discordgo.Session, e *discordgo.Ready) {
		r.callback(func() {
			if err := r.state.ready(e.Guilds); err != nil {
				r.logger.Error("Gateway state admission failed", "event", "discord.state", "error", err)
			}
			r.readyRuntime()
		})
	})
	r.discord.AddHandler(func(_ *discordgo.Session, _ *discordgo.Resumed) { r.callback(func() { r.connected.Store(true) }) })
	r.discord.AddHandler(func(_ *discordgo.Session, _ *discordgo.Disconnect) {
		r.callback(func() {
			r.connected.Store(false)
			select {
			case r.disconnected <- struct{}{}:
			default:
			}
		})
	})
	r.discord.AddHandler(func(_ *discordgo.Session, e *discordgo.GuildCreate) {
		r.callback(func() {
			if e == nil {
				return
			}
			if err := r.state.updateGuild(e.Guild); err != nil {
				r.logger.Warn("Guild state admission failed", "event", "discord.state", "error", err)
			}
			r.signalRegistration()
		})
	})
	r.discord.AddHandler(func(_ *discordgo.Session, e *discordgo.GuildDelete) {
		r.callback(func() {
			if e == nil || e.Guild == nil || e.Unavailable {
				return
			}
			r.state.remove(e.ID)
			select {
			case r.removals <- e.ID:
			default:
				r.logger.Error("Guild cleanup queue saturated", "event", "discord.state", "recovery", "shutdown_required")
			}
		})
	})
	r.discord.AddHandler(func(_ *discordgo.Session, e *discordgo.VoiceStateUpdate) { r.callback(func() { r.voiceState(e) }) })
	r.discord.AddHandler(func(_ *discordgo.Session, e *discordgo.VoiceServerUpdate) {
		r.callback(func() {
			if v := r.voice.Load(); v != nil {
				v.HandleVoiceServerUpdate(e)
			}
		})
	})
	r.discord.AddHandler(func(_ *discordgo.Session, e *discordgo.InteractionCreate) { r.interaction(e) })
}

func (r *Runtime) readyRuntime() {
	if r.voice.Load() == nil {
		manager, err := discordvoice.NewManager(r.discord, r.logger)
		if err != nil {
			r.logger.Error("Voice initialization failed", "event", "voice.initialize", "category", failure.Transport)
			return
		}
		manager.SetSpeakingHandler(func(guild, user string, speaking bool) { r.callback(func() { r.speaking(guild, user, speaking) }) })
		r.voice.Store(manager)
	}
	r.connected.Store(true)
	r.signalRegistration()
}

func (r *Runtime) voiceState(e *discordgo.VoiceStateUpdate) {
	if e == nil || e.VoiceState == nil {
		return
	}
	if err := r.state.voice(e.VoiceState); err != nil {
		r.logger.Warn("Voice authorization state unavailable", "event", "discord.state", "error", err)
	}
	if v := r.voice.Load(); v != nil {
		v.HandleVoiceStateUpdate(e)
	}
	r.discord.State.RLock()
	self := r.discord.State.User != nil && r.discord.State.User.ID == e.UserID
	r.discord.State.RUnlock()
	if !self {
		if target, ok := r.sessions.Get(e.GuildID); ok {
			if _, err := target.Apply(r.ctx, session.Operation{Action: session.VoiceMember, Speaker: e.UserID, Channel: e.ChannelID}); err != nil && r.ctx.Err() == nil {
				r.logger.Debug("Voice member event dropped", "event", "voice.member", "reason", "session_busy")
			}
		}
		return
	}
	if target, ok := r.sessions.Get(e.GuildID); ok {
		if _, err := target.Apply(r.ctx, session.Operation{Action: session.VoiceLost, Channel: e.ChannelID}); err != nil && r.ctx.Err() == nil {
			r.logger.Warn("Voice-state transition rejected", "event", "voice.event", "reason", "session_busy")
		}
	}
}

func (r *Runtime) Start() error {
	// Start and Close are composition-owned, not concurrent public operations.
	r.work.Add(5)
	go func() { defer r.work.Done(); r.removalLoop() }()
	go func() { defer r.work.Done(); r.registrationLoop() }()
	go func() { defer r.work.Done(); r.gatewayLoop() }()
	for range 2 {
		go func() { defer r.work.Done(); r.rejectionLoop() }()
	}
	if err := r.discord.Open(); err != nil {
		return failure.Wrap("discord_connect", failure.Transport, err)
	}
	r.logger.Info("Discord Gateway connected", "event", "discord.connected")
	return nil
}

func (r *Runtime) gatewayLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.disconnected:
		}
		if r.ctx.Err() != nil {
			return
		}
		for attempt := 1; attempt <= 3; attempt++ {
			if !waitContext(r.ctx, time.Duration(attempt)*time.Second) {
				return
			}
			if r.connected.Load() {
				break
			}
			if err := r.discord.Open(); err == nil {
				r.logger.Info("Gateway reconnected", "event", "discord.recovery", "attempt", attempt)
				break
			}
			r.logger.Warn("Gateway reconnect failed", "event", "discord.recovery", "attempt", attempt, "category", failure.Transport)
		}
		if !r.connected.Load() {
			r.logger.Error("Gateway recovery exhausted", "event", "discord.recovery", "recovery", "exhausted")
			return
		}
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (r *Runtime) signalRegistration() {
	select {
	case r.register <- struct{}{}:
	default:
	}
}

func (r *Runtime) Access(ctx context.Context, guild, user string) (session.Access, error) {
	if err := ctx.Err(); err != nil {
		return session.Access{}, err
	}
	channel, err := r.state.access(guild, user)
	if err != nil {
		return session.Access{}, failure.Wrap("voice_state", failure.Transport, err)
	}
	return session.Access{Channel: channel}, nil
}

func (r *Runtime) removalLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case guild := <-r.removals:
			if err := r.sessions.Remove(guild); err != nil && r.ctx.Err() == nil {
				r.logger.Warn("Guild cleanup failed", "event", "session.remove", "error", err)
			}
			r.mu.Lock()
			delete(r.routes, guild)
			r.mu.Unlock()
		}
	}
}

func (r *Runtime) speaking(guild, user string, speaking bool) {
	r.discord.State.RLock()
	self := r.discord.State.User != nil && r.discord.State.User.ID == user
	r.discord.State.RUnlock()
	if self {
		return
	}
	if target, ok := r.sessions.Get(guild); ok {
		_, err := target.Apply(r.ctx, session.Operation{Action: session.Speaking, Speaker: user, Enabled: speaking})
		if err != nil && r.ctx.Err() == nil {
			r.logger.Debug("Speaking event dropped", "event", "voice.speaking", "reason", "session_busy")
		}
	}
}

func (r *Runtime) guild(guild string) (*session.Session, error) {
	if target, ok := r.sessions.Get(guild); ok {
		return target, nil
	}
	voice := r.voice.Load()
	if voice == nil {
		return nil, errors.New("voice adapter not ready")
	}
	target, err := r.sessions.CreateVoice(guild, voice.Guild(guild), r.logger)
	if errors.Is(err, session.ErrExists) {
		if target, ok := r.sessions.Get(guild); ok {
			return target, nil
		}
		return nil, session.ErrBusy
	}
	if err == nil {
		r.mu.Lock()
		if !r.stopping {
			r.work.Add(1)
			go func() { defer r.work.Done(); r.observe(guild, target) }()
		}
		r.mu.Unlock()
	}
	return target, err
}

func (r *Runtime) Live() error {
	if r.ctx.Err() != nil {
		return errors.New("shutting down")
	}
	return nil
}

func (r *Runtime) Dimensions() health.Dimensions {
	states := [3]string{"unknown", "last_success", "last_failure"}
	return health.Dimensions{DiscordConnected: r.connected.Load(), CommandsReady: r.registered.Load(), Source: states[r.sourceStatus.Load()], Pipeline: states[r.pipelineStatus.Load()], ActiveDelivery: r.delivering.Load()}
}
func (r *Runtime) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.Live(); err != nil {
		return err
	}
	if !r.connected.Load() {
		return errors.New("discord Gateway unavailable")
	}
	if !r.registered.Load() {
		return errors.New("command registration incomplete")
	}
	if r.voice.Load() == nil {
		return errors.New("voice adapter unavailable")
	}
	return nil
}

func (r *Runtime) Close() {
	r.once.Do(func() {
		r.mu.Lock()
		r.stopping = true
		r.mu.Unlock()
		r.cancel()
		r.player.Close()
		r.work.Wait()
		// Core shutdown precedes clearing the SDK socket so voice cleanup is
		// joined. The root socket is already canceled and fails writes promptly.
		r.sessions.Close()
		if err := r.discord.Close(); err != nil {
			r.logger.Debug("Gateway socket closed", "event", "discord.close")
		}
		if voice := r.voice.Load(); voice != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			voice.Close(ctx)
			cancel()
		}
		r.rest.client.CloseIdleConnections()
		r.discord.Client.CloseIdleConnections()
	})
}
