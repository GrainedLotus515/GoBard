package discordvoice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/charmbracelet/log"
	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/gorilla/websocket"
)

func TestClosedManagerRejectsNewVoiceWork(t *testing.T) {
	s, err := discordgo.New("Bot fixture")
	if err != nil {
		t.Fatal(err)
	}
	s.State.User = &discordgo.User{ID: "123"}
	m, err := NewManager(context.Background(), s, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	m.Close(context.Background())
	m.Close(context.Background())
	if _, err = m.Join(context.Background(), "456", "789", false, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

//nolint:gocyclo // Each assertion documents a distinct field preserved by the adapter.
func TestToVoiceStateUpdate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	got, err := toVoiceStateUpdate(&discordgo.VoiceStateUpdate{
		VoiceState: &discordgo.VoiceState{
			GuildID:                 "1",
			ChannelID:               "2",
			UserID:                  "3",
			SessionID:               "session-id",
			Deaf:                    true,
			Mute:                    true,
			SelfDeaf:                true,
			SelfMute:                true,
			SelfStream:              true,
			SelfVideo:               true,
			Suppress:                true,
			RequestToSpeakTimestamp: &now,
		},
	})
	if err != nil {
		t.Fatalf("toVoiceStateUpdate() error = %v", err)
	}

	if got.GuildID.String() != "1" {
		t.Fatalf("GuildID = %s, want 1", got.GuildID)
	}
	if got.ChannelID == nil || got.ChannelID.String() != "2" {
		t.Fatalf("ChannelID = %v, want 2", got.ChannelID)
	}
	if got.UserID.String() != "3" {
		t.Fatalf("UserID = %s, want 3", got.UserID)
	}
	if got.SessionID != "session-id" {
		t.Fatalf("SessionID = %q, want session-id", got.SessionID)
	}
	if !got.GuildDeaf || !got.GuildMute || !got.SelfDeaf || !got.SelfMute || !got.SelfStream || !got.SelfVideo || !got.Suppress {
		t.Fatal("expected all voice state flags to be preserved")
	}
	if got.RequestToSpeakTimestamp == nil || !got.RequestToSpeakTimestamp.Equal(now) {
		t.Fatalf("RequestToSpeakTimestamp = %v, want %v", got.RequestToSpeakTimestamp, now)
	}
}

func TestToVoiceServerUpdate(t *testing.T) {
	endpoint := "voice.example.com"

	got, err := toVoiceServerUpdate(&discordgo.VoiceServerUpdate{
		Token:    "token",
		GuildID:  "42",
		Endpoint: &endpoint,
	})
	if err != nil {
		t.Fatalf("toVoiceServerUpdate() error = %v", err)
	}

	if got.Token != "token" {
		t.Fatalf("Token = %q, want token", got.Token)
	}
	if got.GuildID.String() != "42" {
		t.Fatalf("GuildID = %s, want 42", got.GuildID)
	}
	if got.Endpoint == nil || *got.Endpoint != endpoint {
		t.Fatalf("Endpoint = %v, want %q", got.Endpoint, endpoint)
	}
}

func TestToVoiceStateUpdateRejectsInvalidIDs(t *testing.T) {
	_, err := toVoiceStateUpdate(&discordgo.VoiceStateUpdate{
		VoiceState: &discordgo.VoiceState{
			GuildID: "invalid",
			UserID:  "3",
		},
	})
	if err == nil {
		t.Fatal("toVoiceStateUpdate() error = nil, want parse failure")
	}
}

func TestOwnLeavesClaimAcknowledgementsInOrderWithinWindow(t *testing.T) {
	m := &Manager{}
	now := time.Now()
	if _, own := m.ConsumeOwnLeave("1"); own {
		t.Fatal("unmarked empty voice state treated as an own leave")
	}
	// A recover's teardown, then its failed connect's cleanup.
	m.leaves.mark("1", LeaveRequested, now)
	m.leaves.mark("1", LeaveTransportLost, now)
	if _, own := m.ConsumeOwnLeave("2"); own {
		t.Fatal("leave mark leaked across guilds")
	}
	if kind, own := m.ConsumeOwnLeave("1"); !own || kind != LeaveRequested {
		t.Fatalf("first acknowledgement = %v, %v; want requested", kind, own)
	}
	if kind, own := m.ConsumeOwnLeave("1"); !own || kind != LeaveTransportLost {
		t.Fatalf("second acknowledgement = %v, %v; want transport lost", kind, own)
	}
	if _, own := m.ConsumeOwnLeave("1"); own {
		t.Fatal("two leaves claimed a third empty voice state (a later removal)")
	}

	m.leaves.mark("1", LeaveRequested, now.Add(-ownLeaveWindow))
	m.leaves.mark("1", LeaveTransportLost, now)
	if kind, own := m.ConsumeOwnLeave("1"); !own || kind != LeaveTransportLost {
		t.Fatalf("expired mark not skipped: %v, %v", kind, own)
	}
	m.leaves.mark("1", LeaveRequested, now.Add(-ownLeaveWindow))
	if _, own := m.ConsumeOwnLeave("1"); own {
		t.Fatal("stale leave mark hid a removal")
	}

	m.leaves.mark("1", LeaveRequested, now)
	m.leaves.mark("1", LeaveTransportLost, now)
	m.leaves.unmark("1")
	if kind, _ := m.ConsumeOwnLeave("1"); kind != LeaveRequested {
		t.Fatal("unmark did not withdraw the newest (unsent) leave")
	}
	if _, own := m.ConsumeOwnLeave("1"); own {
		t.Fatal("withdrawn leave still claimed an acknowledgement")
	}

	for range maxOwnLeaves + 2 {
		m.leaves.mark("1", LeaveRequested, now)
	}
	claimed := 0
	for {
		if _, own := m.ConsumeOwnLeave("1"); !own {
			break
		}
		claimed++
	}
	if claimed != maxOwnLeaves {
		t.Fatalf("claimed %d acknowledgements, want bound %d", claimed, maxOwnLeaves)
	}
	var none *Manager
	if _, own := none.ConsumeOwnLeave("1"); own {
		t.Fatal("nil manager claimed a leave")
	}
}

func TestClassifyVoiceSocketClose(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want uint32
	}{
		{"kicked", &websocket.CloseError{Code: 4014}, closeEnded},
		{"rate limited", &websocket.CloseError{Code: 4021}, closeEnded},
		{"call terminated", fmt.Errorf("read: %w", &websocket.CloseError{Code: 4022}), closeEnded},
		{"server crash", &websocket.CloseError{Code: 4015}, closeLost},
		{"session timeout", &websocket.CloseError{Code: 4009}, closeLost},
		{"abnormal closure", &websocket.CloseError{Code: websocket.CloseAbnormalClosure}, closeLost},
		{"network", io.ErrUnexpectedEOF, closeLost},
	} {
		if got := classifyClose(tc.err); got != tc.want {
			t.Errorf("%s: classifyClose = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestConnectionLeavesAreMarkedWithTheirCloseCause(t *testing.T) {
	ctx := context.Background()
	channel := snowflake.ID(5)
	var writeErr error
	writes := 0
	update := func(context.Context, snowflake.ID, *snowflake.ID, bool, bool) error {
		writes++
		return writeErr
	}
	closes := 0
	closed := func(disgovoice.Gateway, error, bool) { closes++ }

	for _, tc := range []struct {
		name     string
		closeErr error // nil: the owner disconnects; no socket close first
		want     LeaveKind
		marked   bool
	}{
		{name: "owner disconnect", want: LeaveRequested, marked: true},
		{name: "network loss", closeErr: io.ErrUnexpectedEOF, want: LeaveTransportLost, marked: true},
		{name: "kicked", closeErr: &websocket.CloseError{Code: 4014}},
	} {
		m := &Manager{}
		cause := new(atomic.Uint32)
		leave := m.markLeaves("1", cause, update)
		if err := leave(ctx, 1, &channel, false, false); err != nil {
			t.Fatal(err)
		}
		if _, own := m.ConsumeOwnLeave("1"); own {
			t.Fatalf("%s: join marked as a leave", tc.name)
		}
		if tc.closeErr != nil {
			recordClose(cause, closed)(nil, tc.closeErr, false)
			// A later close cannot rewrite the first cause.
			recordClose(cause, closed)(nil, io.EOF, false)
		}
		if err := leave(ctx, 1, nil, false, false); err != nil {
			t.Fatal(err)
		}
		kind, own := m.ConsumeOwnLeave("1")
		if own != tc.marked || kind != tc.want {
			t.Fatalf("%s: leave = %v, %v; want %v, %v", tc.name, kind, own, tc.want, tc.marked)
		}
	}
	if closes != 4 {
		t.Fatalf("SDK close handler called %d times, want 4", closes)
	}

	m := &Manager{}
	writeErr = errors.New("gateway closed")
	if err := m.markLeaves("1", new(atomic.Uint32), update)(ctx, 1, nil, false, false); err == nil {
		t.Fatal("write error swallowed")
	}
	if _, own := m.ConsumeOwnLeave("1"); own {
		t.Fatal("unsent leave left a mark that could hide a removal")
	}
	if writes != 7 {
		t.Fatalf("writes = %d, want 7", writes)
	}
}
