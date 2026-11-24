package discordvoice

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave"
	"github.com/disgoorg/snowflake/v2"
	"github.com/gorilla/websocket"
)

// LeaveKind classifies a voice leave (Gateway opcode 4 without a channel)
// that this adapter wrote. Discord acknowledges a leave with the bot's own
// voice state and an empty channel, which is indistinguishable on the wire
// from a removal by a moderator. The runtime consumes the matching mark so
// that only removals by someone else are published as an empty channel.
type LeaveKind uint8

const (
	// LeaveRequested is a leave the session asked for: Disconnect, the
	// teardown that precedes every Connect, or a failed connect's cleanup.
	// Its acknowledgement carries no news for the session.
	LeaveRequested LeaveKind = iota + 1
	// LeaveTransportLost is the SDK leaving after the voice socket closed for
	// a reason other than Discord ending the bot's voice session (a network
	// failure, a voice server crash). The bot was not removed; the session
	// must recover or release the connection.
	LeaveTransportLost
)

// ownLeaveWindow bounds how long a written leave may claim the next empty
// self voice state. Discord sends none if the bot was not in a channel; the
// bound keeps that stale mark from hiding a later moderator removal.
const ownLeaveWindow = 5 * time.Second

// maxOwnLeaves bounds outstanding marks per guild. A Connect writes at most
// two leaves (teardown, failure cleanup); older marks are dropped first.
const maxOwnLeaves = 4

type ownLeave struct {
	kind     LeaveKind
	deadline time.Time
}

// leaveLedger records written leaves per guild in write order. Discord
// acknowledges opcode 4 writes in order on the one Gateway connection, so the
// oldest unexpired mark belongs to the next empty self voice state.
type leaveLedger struct {
	mu      sync.Mutex
	pending map[string][]ownLeave
}

func (l *leaveLedger) mark(guild string, kind LeaveKind, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending == nil {
		l.pending = make(map[string][]ownLeave)
	}
	marks := append(l.pending[guild], ownLeave{kind: kind, deadline: now.Add(ownLeaveWindow)})
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

func (l *leaveLedger) consume(guild string, now time.Time) (LeaveKind, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	marks := l.pending[guild]
	// Deadlines are appended in increasing order; drop the expired prefix.
	for len(marks) > 0 && !now.Before(marks[0].deadline) {
		marks = marks[1:]
	}
	if len(marks) == 0 {
		delete(l.pending, guild)
		return 0, false
	}
	kind := marks[0].kind
	if len(marks) == 1 {
		delete(l.pending, guild)
	} else {
		l.pending[guild] = marks[1:]
	}
	return kind, true
}

// Voice socket close causes recorded per connection before the SDK leaves.
const (
	closeNone uint32 = iota
	// closeLost: network failure or a close Discord expects clients to
	// survive by reconnecting.
	closeLost
	// closeEnded: Discord ended this client's voice session (kicked, call
	// terminated, rate limited). Its own empty voice state is authoritative.
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

// connCreate wraps the SDK's connection constructor so that every leave a
// connection writes is marked in the ledger with its cause. The SDK writes
// exactly one leave per connection (its Close runs once): either from an
// owner Disconnect/Shutdown (requested), or from its voice socket close
// handler. The close cause is recorded before that handler runs.
func (m *Manager) connCreate(guildID, userID snowflake.ID, update disgovoice.StateUpdateFunc, remove func(), opts ...disgovoice.ConnConfigOpt) disgovoice.Conn {
	cause := new(atomic.Uint32)
	gateway := func(dave godave.Session, events disgovoice.EventHandlerFunc, closed disgovoice.CloseHandlerFunc, gatewayOpts ...disgovoice.GatewayConfigOpt) disgovoice.Gateway {
		return disgovoice.NewGateway(dave, events, recordClose(cause, closed), gatewayOpts...)
	}
	return disgovoice.NewConn(guildID, userID, m.markLeaves(guildID.String(), cause, update), remove, append(opts, disgovoice.WithConnGatewayCreateFunc(gateway))...)
}

// recordClose records a voice socket close cause before the SDK reacts.
func recordClose(cause *atomic.Uint32, closed disgovoice.CloseHandlerFunc) disgovoice.CloseHandlerFunc {
	return func(g disgovoice.Gateway, err error, reconnect bool) {
		cause.CompareAndSwap(closeNone, classifyClose(err))
		closed(g, err, reconnect)
	}
}

// markLeaves wraps one connection's voice state writes and marks each leave
// with the connection's close cause. A leave after Discord ended the session
// is not marked: Discord's own empty voice state is the removal.
func (m *Manager) markLeaves(guild string, cause *atomic.Uint32, update disgovoice.StateUpdateFunc) disgovoice.StateUpdateFunc {
	return func(ctx context.Context, guildID snowflake.ID, channelID *snowflake.ID, selfMute, selfDeaf bool) error {
		if channelID != nil {
			return update(ctx, guildID, channelID, selfMute, selfDeaf)
		}
		kind := LeaveRequested
		switch cause.Load() {
		case closeEnded:
			return update(ctx, guildID, channelID, selfMute, selfDeaf)
		case closeLost:
			kind = LeaveTransportLost
		}
		// Mark before writing: the acknowledgement may be handled before
		// the write returns.
		m.leaves.mark(guild, kind, time.Now())
		err := update(ctx, guildID, channelID, selfMute, selfDeaf)
		if err != nil {
			m.leaves.unmark(guild)
		}
		return err
	}
}

// ConsumeOwnLeave reports whether an empty self voice state for guildID
// acknowledges a leave this adapter wrote, and why that leave was written.
// It consumes the mark. A false result means someone else removed the bot.
func (m *Manager) ConsumeOwnLeave(guildID string) (LeaveKind, bool) {
	if m == nil {
		return 0, false
	}
	return m.leaves.consume(guildID, time.Now())
}
