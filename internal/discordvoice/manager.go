package discordvoice

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/voiceconn"
	"github.com/bwmarrin/discordgo"
	disgodiscord "github.com/disgoorg/disgo/discord"
	disgogateway "github.com/disgoorg/disgo/gateway"
	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/golibdave"
	"github.com/disgoorg/snowflake/v2"
)

var _ voiceconn.Connection = (*Connection)(nil)

// Manager bridges discordgo gateway events into disgo's DAVE-capable voice stack.
type Manager struct {
	manager disgovoice.Manager
}

// Connection adapts a disgo voice connection to the player-facing voice interface.
type Connection struct {
	conn disgovoice.Conn
}

// NewManager creates a DAVE-capable voice manager for an authenticated discordgo session.
func NewManager(session *discordgo.Session) (*Manager, error) {
	if session == nil || session.State == nil || session.State.User == nil {
		return nil, fmt.Errorf("discord session user is not available yet")
	}

	userID, err := snowflake.Parse(session.State.User.ID)
	if err != nil {
		return nil, fmt.Errorf("parse bot user id: %w", err)
	}

	manager := disgovoice.NewManager(
		func(ctx context.Context, guildID snowflake.ID, channelID *snowflake.ID, selfMute, selfDeaf bool) error {
			channel := ""
			if channelID != nil {
				channel = channelID.String()
			}
			return session.VoiceStateUpdate(guildID.String(), channel, selfMute, selfDeaf)
		},
		userID,
		disgovoice.WithLogger(slog.Default()),
		disgovoice.WithDaveSessionLogger(slog.Default()),
		disgovoice.WithDaveSessionCreateFunc(golibdave.NewSession),
	)

	return &Manager{manager: manager}, nil
}

// Join connects to a Discord voice channel using the DAVE-capable voice stack.
func (m *Manager) Join(ctx context.Context, guildID, channelID string, selfMute, selfDeaf bool) (voiceconn.Connection, error) {
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
	if err := conn.Open(ctx, channelSnowflake, selfMute, selfDeaf); err != nil {
		return nil, err
	}

	return &Connection{conn: conn}, nil
}

// HandleVoiceStateUpdate forwards a discordgo voice state event to the disgo voice manager.
func (m *Manager) HandleVoiceStateUpdate(update *discordgo.VoiceStateUpdate) {
	if m == nil || update == nil || update.VoiceState == nil {
		return
	}

	event, err := toVoiceStateUpdate(update)
	if err != nil {
		logger.Warn("Skipping invalid voice state update", "err", err)
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
		logger.Warn("Skipping invalid voice server update", "err", err)
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

func (c *Connection) SendOpusFrame(frame []byte) error {
	_, err := c.conn.UDP().Write(frame)
	return err
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
