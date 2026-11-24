package discordvoice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/charmbracelet/log"
	disgodiscord "github.com/disgoorg/disgo/discord"
	disgogateway "github.com/disgoorg/disgo/gateway"
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
	if m.ConsumeOwnLeave("1") {
		t.Fatal("unmarked empty voice state treated as an own leave")
	}
	// A recover's teardown, then its failed connect's cleanup.
	m.leaves.mark("1", now)
	m.leaves.mark("1", now)
	if m.ConsumeOwnLeave("2") {
		t.Fatal("leave mark leaked across guilds")
	}
	for range 2 {
		if !m.ConsumeOwnLeave("1") {
			t.Fatal("own leave acknowledgement not recognized")
		}
	}
	if m.ConsumeOwnLeave("1") {
		t.Fatal("two leaves claimed a third empty voice state (a later removal)")
	}

	m.leaves.mark("1", now.Add(-ownLeaveWindow))
	if m.ConsumeOwnLeave("1") {
		t.Fatal("stale leave mark hid a removal")
	}
	m.leaves.mark("1", now.Add(-ownLeaveWindow))
	m.leaves.mark("1", now)
	if !m.ConsumeOwnLeave("1") || m.ConsumeOwnLeave("1") {
		t.Fatal("expired prefix not skipped exactly once")
	}

	m.leaves.mark("1", now)
	m.leaves.mark("1", now)
	m.leaves.unmark("1")
	if !m.ConsumeOwnLeave("1") || m.ConsumeOwnLeave("1") {
		t.Fatal("unmark did not withdraw exactly the unsent leave")
	}

	for range maxOwnLeaves + 2 {
		m.leaves.mark("1", now)
	}
	claimed := 0
	for m.ConsumeOwnLeave("1") {
		claimed++
	}
	if claimed != maxOwnLeaves {
		t.Fatalf("claimed %d acknowledgements, want bound %d", claimed, maxOwnLeaves)
	}
	var none *Manager
	if none.ConsumeOwnLeave("1") {
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

func TestConnectionLeavesAreMarkedUnlessDiscordRemovedTheBot(t *testing.T) {
	ctx := context.Background()
	const bot = snowflake.ID(3)
	var writeErr error
	leaves := 0
	update := func(_ context.Context, _ snowflake.ID, channel *snowflake.ID, _, _ bool) error {
		if channel == nil {
			leaves++
		}
		return writeErr
	}
	channel := snowflake.ID(5)
	selfState := func(channel *snowflake.ID, user snowflake.ID) disgogateway.EventVoiceStateUpdate {
		return disgogateway.EventVoiceStateUpdate{VoiceState: disgodiscord.VoiceState{GuildID: 1, UserID: user, ChannelID: channel}}
	}

	// Real SDK connections: Close writes the leave through the wrapper.
	for _, tc := range []struct {
		name    string
		updates []disgogateway.EventVoiceStateUpdate
		marked  bool
	}{
		{name: "owner disconnect while in a channel", updates: []disgogateway.EventVoiceStateUpdate{selfState(&channel, bot)}, marked: true},
		{name: "never acknowledged a channel (failed join)", marked: true},
		{name: "another member left", updates: []disgogateway.EventVoiceStateUpdate{selfState(&channel, bot), selfState(nil, 9)}, marked: true},
		{name: "after Discord removed the bot", updates: []disgogateway.EventVoiceStateUpdate{selfState(&channel, bot), selfState(nil, bot)}},
	} {
		m := &Manager{}
		conn := m.connCreate(1, bot, update, func() {})
		for _, u := range tc.updates {
			conn.HandleVoiceStateUpdate(u)
		}
		before := leaves
		conn.Shutdown(ctx)
		if leaves != before+1 {
			t.Fatalf("%s: SDK wrote %d leaves, want 1", tc.name, leaves-before)
		}
		if own := m.ConsumeOwnLeave("1"); own != tc.marked {
			t.Fatalf("%s: marked = %v, want %v", tc.name, own, tc.marked)
		}
	}

	// Close causes recorded by the gateway close handler.
	for _, tc := range []struct {
		name     string
		closeErr error
		absent   bool
		marked   bool
		lost     bool
	}{
		{name: "network loss", closeErr: io.ErrUnexpectedEOF, marked: true, lost: true},
		{name: "kicked", closeErr: &websocket.CloseError{Code: 4014}},
		{name: "network loss after removal", closeErr: io.ErrUnexpectedEOF, absent: true},
	} {
		m := &Manager{}
		var lost []string
		m.SetTransportLossHandler(func(guild string) { lost = append(lost, guild) })
		state := &connState{guild: "1", user: bot}
		state.absent.Store(tc.absent)
		closes := 0
		closed := m.recordClose(state, func(disgovoice.Gateway, error, bool) { closes++ })
		closed(nil, tc.closeErr, false)
		// A later close cannot rewrite the first cause or report twice.
		closed(nil, io.EOF, false)
		if err := m.markLeaves(state, update)(ctx, 1, nil, false, false); err != nil {
			t.Fatal(err)
		}
		if own := m.ConsumeOwnLeave("1"); own != tc.marked {
			t.Fatalf("%s: marked = %v, want %v", tc.name, own, tc.marked)
		}
		if got := len(lost) == 1 && lost[0] == "1"; got != tc.lost || len(lost) > 1 || closes != 2 {
			t.Fatalf("%s: loss reports %q (want %v), SDK closes %d", tc.name, lost, tc.lost, closes)
		}
	}

	m := &Manager{}
	writeErr = errors.New("gateway closed")
	if err := m.markLeaves(&connState{guild: "1"}, update)(ctx, 1, nil, false, false); err == nil {
		t.Fatal("write error swallowed")
	}
	if m.ConsumeOwnLeave("1") {
		t.Fatal("unsent leave left a mark that could hide a removal")
	}
}
