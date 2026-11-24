package discordvoice

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	disgogateway "github.com/disgoorg/disgo/gateway"
	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave"
	"github.com/disgoorg/snowflake/v2"
	"github.com/gorilla/websocket"
)

// Discord acknowledges every voice leave (Gateway opcode 4 without a channel)
// with the bot's own voice state and an empty channel, which is
// indistinguishable on the wire from a removal by a moderator. The adapter
// marks each leave it writes while the bot is still in a channel; the runtime
// consumes the matching acknowledgement so that only removals by someone else
// are published as an empty channel. A voice socket lost while the bot was not
// removed is reported separately, when it happens, as a transport loss.

// ownLeaveWindow bounds how long a written leave may claim the next empty
// self voice state. Discord sends none if it already considered the bot
// absent; the bound keeps such a stale mark from hiding a later removal.
const ownLeaveWindow = 5 * time.Second

// maxOwnLeaves bounds outstanding marks per guild. A Connect writes at most
// two leaves (teardown, failure cleanup); older marks are dropped first.
const maxOwnLeaves = 4

// leaveLedger records the deadlines of written leaves per guild in write
// order. Discord acknowledges opcode 4 writes in order on the one Gateway
// connection, so the oldest unexpired mark belongs to the next empty self
// voice state.
type leaveLedger struct {
	mu      sync.Mutex
	pending map[string][]time.Time
}

func (l *leaveLedger) mark(guild string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending == nil {
		l.pending = make(map[string][]time.Time)
	}
	marks := append(l.pending[guild], now.Add(ownLeaveWindow))
	if len(marks) > maxOwnLeaves {
		marks = append(marks[:0], marks[len(marks)-maxOwnLeaves:]...)
	}
	l.pending[guild] = marks
}

// unmark withdraws the newest mark after its leave failed to send.
func (l *leaveLedger) unmark(guild string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	marks := l.pending[guild]
	if len(marks) <= 1 {
		delete(l.pending, guild)
		return
	}
	l.pending[guild] = marks[:len(marks)-1]
}

func (l *leaveLedger) consume(guild string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	marks := l.pending[guild]
	// Deadlines are appended in increasing order; drop the expired prefix.
	for len(marks) > 0 && !now.Before(marks[0]) {
		marks = marks[1:]
	}
	if len(marks) == 0 {
		delete(l.pending, guild)
		return false
	}
	if len(marks) == 1 {
		delete(l.pending, guild)
	} else {
		l.pending[guild] = marks[1:]
	}
	return true
}

// Voice socket close causes, recorded once per connection.
const (
	closeNone uint32 = iota
	// closeLost: network failure or a close Discord expects clients to
	// survive by reconnecting.
	closeLost
	// closeEnded: Discord ended this client's voice session (4014
	// disconnected, 4021 rate limited, 4022 call terminated). Recovery is
	// not attempted; Discord documents 4014 for kicks and dropped sessions
	// but also for a voice server change, which therefore is not followed.
	closeEnded
)

// classifyClose maps a voice socket close to its cause. Only codes where
// Discord itself ended the session count as ended; every other close,
// including read errors without a close frame, is a transport loss.
func classifyClose(err error) uint32 {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		switch closeErr.Code {
		case disgovoice.GatewayCloseEventCodeDisconnected.Code,
			disgovoice.GatewayCloseEventCodeRateLimited.Code,
			disgovoice.GatewayCloseEventCodeCallTerminated.Code:
			return closeEnded
		}
	}
	return closeLost
}

// connState is one connection's view of why it may leave.
type connState struct {
	guild  string
	user   snowflake.ID
	cause  atomic.Uint32
	absent atomic.Bool // Discord last reported the bot in no channel
}

// trackedConn records the bot's own channel as Discord reports it to this
// connection, so a leave written after Discord removed the bot is not marked.
type trackedConn struct {
	disgovoice.Conn
	state *connState
}

func (c trackedConn) HandleVoiceStateUpdate(update disgogateway.EventVoiceStateUpdate) {
	if update.UserID == c.state.user {
		c.state.absent.Store(update.ChannelID == nil)
	}
	c.Conn.HandleVoiceStateUpdate(update)
}

// connCreate wraps the SDK's connection constructor. The SDK writes exactly
// one leave per connection (its Close runs once): from an owner
// Disconnect/Shutdown or from its voice socket close handler, which records
// the close cause first. Leaves are marked unless Discord already removed the
// bot; a lost socket is reported to the loss handler before its leave is
// written.
func (m *Manager) connCreate(guildID, userID snowflake.ID, update disgovoice.StateUpdateFunc, remove func(), opts ...disgovoice.ConnConfigOpt) disgovoice.Conn {
	state := &connState{guild: guildID.String(), user: userID}
	gateway := func(dave godave.Session, events disgovoice.EventHandlerFunc, closed disgovoice.CloseHandlerFunc, gatewayOpts ...disgovoice.GatewayConfigOpt) disgovoice.Gateway {
		return disgovoice.NewGateway(dave, events, m.recordClose(state, closed), gatewayOpts...)
	}
	conn := disgovoice.NewConn(guildID, userID, m.markLeaves(state, update), remove, append(opts, disgovoice.WithConnGatewayCreateFunc(gateway))...)
	return trackedConn{Conn: conn, state: state}
}

// recordClose records a voice socket close cause before the SDK reacts, and
// reports a transport loss unless Discord removed the bot. The SDK runs close
// handlers on gateway workers that its Shutdown joins, so a loss is reported
// before the owner's Disconnect of this connection returns.
func (m *Manager) recordClose(state *connState, closed disgovoice.CloseHandlerFunc) disgovoice.CloseHandlerFunc {
	return func(g disgovoice.Gateway, err error, reconnect bool) {
		cause := classifyClose(err)
		if state.cause.CompareAndSwap(closeNone, cause) && cause == closeLost && !state.absent.Load() {
			m.lossMu.RLock()
			handler := m.lossHandler
			m.lossMu.RUnlock()
			if handler != nil {
				handler(state.guild)
			}
		}
		closed(g, err, reconnect)
	}
}

// markLeaves wraps one connection's voice state writes and marks each leave
// the bot writes while still in a channel. A leave after Discord ended the
// session or reported the bot absent is not marked: Discord's own empty voice
// state was the removal, and this leave is not acknowledged.
func (m *Manager) markLeaves(state *connState, update disgovoice.StateUpdateFunc) disgovoice.StateUpdateFunc {
	return func(ctx context.Context, guildID snowflake.ID, channelID *snowflake.ID, selfMute, selfDeaf bool) error {
		if channelID != nil || state.cause.Load() == closeEnded || state.absent.Load() {
			return update(ctx, guildID, channelID, selfMute, selfDeaf)
		}
		// Mark before writing: the acknowledgement may be handled before
		// the write returns.
		m.leaves.mark(state.guild, time.Now())
		err := update(ctx, guildID, channelID, selfMute, selfDeaf)
		if err != nil {
			m.leaves.unmark(state.guild)
		}
		return err
	}
}

// SetTransportLossHandler registers the consumer for a guild's voice socket
// lost while the bot was not removed from its channel. The handler runs on an
// SDK worker and must not block.
func (m *Manager) SetTransportLossHandler(handler func(guildID string)) {
	if m == nil {
		return
	}
	m.lossMu.Lock()
	m.lossHandler = handler
	m.lossMu.Unlock()
}

// ConsumeOwnLeave reports whether an empty self voice state for guildID
// acknowledges a leave this adapter wrote, and consumes the mark. False means
// someone else removed the bot.
func (m *Manager) ConsumeOwnLeave(guildID string) bool {
	if m == nil {
		return false
	}
	return m.leaves.consume(guildID, time.Now())
}
