package discordvoice

import (
	"context"
	"errors"
	"time"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

// Guild owns one guild's connection and packet output. Its lifecycle methods
// are serialized by the session coordinator after attempts have joined.
type Guild struct {
	manager *Manager
	id      string
	conn    *Connection
	output  *Output
}

func (m *Manager) Guild(id string) *Guild { return &Guild{manager: m, id: id} }

func (g *Guild) Connect(ctx context.Context, channel string) (playback.Output, error) {
	if err := g.Disconnect(ctx); err != nil {
		return nil, err
	}
	conn, err := g.manager.Join(ctx, g.id, channel, false, false)
	if err != nil {
		return nil, err
	}
	g.conn = conn
	output, err := conn.Output()
	if err == nil {
		g.output = output
		err = conn.SetSpeaking(ctx, true)
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		return nil, errors.Join(err, g.Disconnect(cleanup))
	}
	return output, nil
}

func (g *Guild) Disconnect(ctx context.Context) error {
	var err error
	if g.output != nil {
		err = g.output.Close()
		g.output = nil
	}
	if g.conn != nil {
		err = errors.Join(err, g.conn.Disconnect(ctx))
		g.conn = nil
	}
	return err
}
