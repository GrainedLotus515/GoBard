package bot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/media"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

func TestPlayShowsTrackAndUsableControlsBeforeFirstAudio(t *testing.T) {
	snapshot := session.Snapshot{Current: 0, Connected: true, Active: true, Volume: 70, Tracks: []media.Track{
		{Title: "Selected song", Artist: "Artist", Duration: 3 * time.Minute, RequestedBy: "123", Thumbnail: "https://i.ytimg.com/vi/fixture/default.jpg"},
		{Title: "Next song"},
	}}
	card, rows := runtimeCard(MappedInteraction{Name: "play"}, snapshot, 2)
	if card.Title != "Selected song" || card.Author.Name != "Starting playback" || card.Thumbnail == nil || !strings.Contains(card.Description, "Added **2 tracks**") {
		t.Fatalf("missing useful play response: %+v", card)
	}
	if card.Fields[2].Name != "Up next" || !strings.Contains(card.Fields[2].Value, "Next song") || !strings.Contains(card.Fields[0].Value, "0:00 / 3:00") {
		t.Fatal("missing progress/next track")
	}
	checkPlayerControls(t, rows, "Pause", "Repeat off")
	snapshot.Paused, snapshot.Loop, snapshot.Frames = true, true, 10
	card, rows = runtimeCard(MappedInteraction{Name: "pause"}, snapshot, 0)
	if card.Author.Name != "Paused" {
		t.Fatal("paused state is not visible")
	}
	checkPlayerControls(t, rows, "Resume", "Repeat on")
}

func checkPlayerControls(t *testing.T, rows []discordgo.MessageComponent, pause, repeat string) {
	t.Helper()
	labels := make(map[string]bool)
	for _, component := range rows {
		row, ok := component.(discordgo.ActionsRow)
		if !ok {
			t.Fatal("expected button row")
		}
		for _, component := range row.Components {
			button, ok := component.(discordgo.Button)
			if !ok {
				t.Fatal("expected button")
			}
			labels[button.Label] = true
			if _, _, err := botui.ParseCustomID(button.CustomID); err != nil {
				t.Fatalf("unusable control %q", button.Label)
			}
		}
	}
	for _, label := range []string{pause, repeat, "Skip", "Stop", "Queue", "Refresh player"} {
		if !labels[label] {
			t.Fatalf("missing %s", label)
		}
	}
}

func TestPlayerStateAndProgressDoNotInventCompletion(t *testing.T) {
	for _, tc := range []struct {
		snapshot session.Snapshot
		want     string
	}{
		{session.Snapshot{Connected: true, Active: true, Frames: 1}, "Now playing"},
		{session.Snapshot{Connected: true, Transition: playback.Skipped}, "Changing track"},
		{session.Snapshot{Connected: true, Transition: playback.Seeked}, "Seeking"},
		{session.Snapshot{Connecting: true}, "Connecting to voice"},
		{session.Snapshot{Connected: true, Recovery: "source_resume"}, "Resuming after an interruption"},
	} {
		if got := playbackLabel(tc.snapshot); got != tc.want {
			t.Fatalf("state %q, want %q", got, tc.want)
		}
	}
	if got := runtimeProgress(2*time.Minute, time.Minute); strings.Contains(got, "▱") || !strings.Contains(got, "1:00") {
		t.Fatalf("progress exceeded duration: %q", got)
	}
	if got := trackLength(media.Track{Live: true}); got != "Live" {
		t.Fatal(got)
	}
	card, controls := runtimeCard(MappedInteraction{Name: "stop"}, session.Snapshot{Active: true}, 0)
	if card.Title != "Stopping playback" || len(controls) != 0 {
		t.Fatal("stop exposed stale playback controls")
	}
}

func TestDeniedButtonPreservesSharedPlayerAndRepliesPrivately(t *testing.T) {
	r := runtimeFixture(t)
	var methods, paths []string
	var payloads []map[string]any
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		methods, paths, payloads = append(methods, req.Method), append(paths, req.URL.Path), append(payloads, payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	r.executeInteraction(interactionRef{id: "123", app: "456", token: "token"}, MappedInteraction{Guild: "123", User: "456", Name: "pause", Component: true, Operation: session.Operation{Action: session.TogglePause}})
	if len(payloads) != 2 || methods[1] != http.MethodPost || strings.Contains(paths[1], "@original") {
		t.Fatalf("shared message edited on rejection: %v %v", methods, paths)
	}
	if payloads[1]["flags"] != float64(discordgo.MessageFlagsEphemeral) || payloads[1]["allowed_mentions"] == nil {
		t.Fatalf("rejection was not private and mention-safe: %+v", payloads[1])
	}
}

func TestPlayerNavigationIsReadOnlyWithoutVoiceMembership(t *testing.T) {
	r := runtimeFixture(t)
	i := interactionFixture("queue")
	i.Type = discordgo.InteractionMessageComponent
	i.Data = discordgo.MessageComponentInteractionData{CustomID: string(botui.ActionPlaybackShow)}
	mapped, err := MapInteraction(i)
	if err != nil || mapped.Operation.Action != session.Inspect || mapped.Name != "now-playing" {
		t.Fatal(mapped, err)
	}
	if _, _, err := r.execute(context.Background(), mapped); err != nil {
		t.Fatal("read-only player required voice membership", err)
	}
}
