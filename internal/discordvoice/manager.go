package discordvoice

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/charmbracelet/log"
	disgodiscord "github.com/disgoorg/disgo/discord"
	disgogateway "github.com/disgoorg/disgo/gateway"
	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/golibdave"
	"github.com/disgoorg/snowflake/v2"
)

// Manager bridges discordgo gateway events into disgo's DAVE-capable voice stack.
type Manager struct {
	manager disgovoice.Manager
	logger  *log.Logger

	speakingMu      sync.RWMutex
	speakingHandler func(guildID, userID string, speaking bool)
}

// Connection adapts a disgo voice connection to the player-facing voice interface.
type Connection struct {
	conn disgovoice.Conn
}

// NewManager creates a DAVE-capable voice manager for an authenticated session.
func NewManager(session *discordgo.Session, logger *log.Logger) (*Manager, error) {
	if logger == nil {
		return nil, fmt.Errorf("voice logger required")
	}
	sdkLogger := slog.New(sdkLog{logger: logger})
	if session == nil || session.State == nil || session.State.User == nil {
		return nil, fmt.Errorf("discord session user is not available yet")
	}

	userID, err := snowflake.Parse(session.State.User.ID)
	if err != nil {
		return nil, fmt.Errorf("parse bot user id: %w", err)
	}

	voiceManager := &Manager{logger: logger}
	manager := disgovoice.NewManager(
		func(ctx context.Context, guildID snowflake.ID, channelID *snowflake.ID, selfMute, selfDeaf bool) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			channel := ""
			if channelID != nil {
				channel = channelID.String()
			}
			// VoiceStateUpdate in this SDK dereferences a nil socket after
			// disconnect. GatewayWriteStruct guards connection lifetime and
			// serializes writes; the runtime bounds its underlying socket I/O.
			var target *string
			if channel != "" {
				target = &channel
			}
			return session.GatewayWriteStruct(struct {
				Op   int `json:"op"`
				Data any `json:"d"`
			}{4, struct {
				Guild   string  `json:"guild_id"`
				Channel *string `json:"channel_id"`
				Mute    bool    `json:"self_mute"`
				Deaf    bool    `json:"self_deaf"`
			}{guildID.String(), target, selfMute, selfDeaf}})
		},
		userID,
		disgovoice.WithLogger(sdkLogger),
		disgovoice.WithDaveSessionLogger(sdkLogger),
		disgovoice.WithDaveSessionCreateFunc(golibdave.NewSession),
	)

	voiceManager.manager = manager
	return voiceManager, nil
}

// Join connects to a Discord voice channel using the DAVE-capable voice stack.
func (m *Manager) Join(ctx context.Context, guildID, channelID string, selfMute, selfDeaf bool) (*Connection, error) {
	if m == nil {
		return nil, fmt.Errorf("voice manager is not initialized")
	}

	guildSnowflake, err := snowflake.Parse(guildID)
	if err != nil {
		return nil, fmt.Errorf("parse guild id: %w", err)
	}
	channelSnowflake, err := snowflake.Parse(channelID)
	if err != nil {
		return nil, fmt.Errorf("parse channel id: %w", err)
	}

	conn := m.manager.CreateConn(guildSnowflake)
	conn.SetEventHandlerFunc(func(_ disgovoice.Gateway, opcode disgovoice.Opcode, _ int, data disgovoice.GatewayMessageData) {
		if opcode != disgovoice.OpcodeSpeaking {
			return
		}
		speaking, ok := data.(disgovoice.GatewayMessageDataSpeaking)
		if !ok || speaking.UserID == 0 {
			return
		}
		m.speakingMu.RLock()
		handler := m.speakingHandler
		m.speakingMu.RUnlock()
		if handler != nil {
			handler(guildID, speaking.UserID.String(), speaking.Speaking != disgovoice.SpeakingFlagNone)
		}
	})
	if err := conn.Open(ctx, channelSnowflake, selfMute, selfDeaf); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		conn.Close(cleanup)
		cancel()
		return nil, err
	}

	return &Connection{conn: conn}, nil
}

// SetSpeakingHandler registers the consumer for Discord Voice Gateway opcode
// 5 events. Unlike guild VoiceStateUpdate, these events describe actual audio
// activity and are therefore safe to use for volume ducking.
func (m *Manager) SetSpeakingHandler(handler func(guildID, userID string, speaking bool)) {
	if m == nil {
		return
	}
	m.speakingMu.Lock()
	m.speakingHandler = handler
	m.speakingMu.Unlock()
}

// HandleVoiceStateUpdate forwards a discordgo voice state event to the disgo voice manager.
func (m *Manager) HandleVoiceStateUpdate(update *discordgo.VoiceStateUpdate) {
	if m == nil || update == nil || update.VoiceState == nil {
		return
	}

	event, err := toVoiceStateUpdate(update)
	if err != nil {
		m.logger.Warn("Invalid voice state update", "event", "voice.event", "kind", "state")
		return
	}

	m.manager.HandleVoiceStateUpdate(event)
}

// HandleVoiceServerUpdate forwards a discordgo voice server event to the disgo voice manager.
func (m *Manager) HandleVoiceServerUpdate(update *discordgo.VoiceServerUpdate) {
	if m == nil || update == nil {
		return
	}

	event, err := toVoiceServerUpdate(update)
	if err != nil {
		m.logger.Warn("Invalid voice server update", "event", "voice.event", "kind", "server")
		return
	}

	m.manager.HandleVoiceServerUpdate(event)
}

// Close closes all managed voice connections.
func (m *Manager) Close(ctx context.Context) {
	if m == nil {
		return
	}
	m.manager.Close(ctx)
}

func (c *Connection) SetSpeaking(ctx context.Context, speaking bool) error {
	flags := disgovoice.SpeakingFlagNone
	if speaking {
		flags = disgovoice.SpeakingFlagMicrophone
	}
	return c.conn.SetSpeaking(ctx, flags)
}

func (c *Connection) Disconnect(ctx context.Context) error {
	c.conn.Close(ctx)
	return nil
}

func toVoiceStateUpdate(update *discordgo.VoiceStateUpdate) (disgogateway.EventVoiceStateUpdate, error) {
	guildID, err := snowflake.Parse(update.GuildID)
	if err != nil {
		return disgogateway.EventVoiceStateUpdate{}, fmt.Errorf("parse guild id: %w", err)
	}
	userID, err := snowflake.Parse(update.UserID)
	if err != nil {
		return disgogateway.EventVoiceStateUpdate{}, fmt.Errorf("parse user id: %w", err)
	}

	var channelID *snowflake.ID
	if update.ChannelID != "" {
		channelSnowflake, err := snowflake.Parse(update.ChannelID)
		if err != nil {
			return disgogateway.EventVoiceStateUpdate{}, fmt.Errorf("parse channel id: %w", err)
		}
		channelID = &channelSnowflake
	}

	return disgogateway.EventVoiceStateUpdate{
		VoiceState: disgodiscord.VoiceState{
			GuildID:                 guildID,
			ChannelID:               channelID,
			UserID:                  userID,
			SessionID:               update.SessionID,
			GuildDeaf:               update.Deaf,
			GuildMute:               update.Mute,
			SelfDeaf:                update.SelfDeaf,
			SelfMute:                update.SelfMute,
			SelfStream:              update.SelfStream,
			SelfVideo:               update.SelfVideo,
			Suppress:                update.Suppress,
			RequestToSpeakTimestamp: update.RequestToSpeakTimestamp,
		},
	}, nil
}

func toVoiceServerUpdate(update *discordgo.VoiceServerUpdate) (disgogateway.EventVoiceServerUpdate, error) {
	guildID, err := snowflake.Parse(update.GuildID)
	if err != nil {
		return disgogateway.EventVoiceServerUpdate{}, fmt.Errorf("parse guild id: %w", err)
	}

	return disgogateway.EventVoiceServerUpdate{
		Token:    update.Token,
		GuildID:  guildID,
		Endpoint: update.Endpoint,
	}, nil
}
