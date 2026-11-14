package bot

import (
	"context"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/session"
)

func TestGuildSnapshotReconcilesBotChannel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		states  []*discordgo.VoiceState
		channel string
	}{
		{"moved during outage", []*discordgo.VoiceState{{UserID: "member", ChannelID: "a"}, {UserID: "bot", ChannelID: "b"}}, "b"},
		{"disconnected during outage", []*discordgo.VoiceState{{UserID: "member", ChannelID: "a"}}, "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runtimeFixture(t)
			target, err := r.sessions.Create("guild", mappingOutput{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := target.Apply(context.Background(), session.Operation{Action: session.Connect, Channel: "a", Output: mappingOutput{}}); err != nil {
				t.Fatal(err)
			}
			r.discord.State.Lock()
			r.discord.State.User = &discordgo.User{ID: "bot"}
			r.discord.State.Unlock()
			r.reconcileSnapshot(&discordgo.Guild{ID: "guild", VoiceStates: tc.states})
			deadline := time.Now().Add(2 * time.Second)
			for {
				snap, err := target.Apply(context.Background(), session.Operation{Action: session.Inspect})
				if err != nil {
					t.Fatal(err)
				}
				if !snap.Connected && snap.Channel == tc.channel {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("stale session voice state after snapshot: connected=%v channel=%q", snap.Connected, snap.Channel)
				}
				time.Sleep(time.Millisecond)
			}
			// A user left behind in the old channel can no longer control playback.
			if tc.channel == "b" {
				if _, err := target.ApplyAs(context.Background(), session.Access{Channel: "a"}, session.Operation{Action: session.Pause}); err == nil {
					t.Fatal("stale-channel user authorized after snapshot")
				}
			}
		})
	}
}
