package bot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

func TestCommandFailuresGiveSafeSpecificGuidance(t *testing.T) {
	private := errors.New("secret-token https://media.invalid/?signature=private @everyone")
	for _, tc := range []struct {
		err  error
		want string
	}{
		{session.ErrBusy, "Wait a moment"},
		{session.ErrFull, "Remove some upcoming"},
		{session.ErrVoiceAccess, "voice channel"},
		{session.ErrManageGuild, "Manage Server"},
		{session.ErrSuperseded, "stopped or disconnected"},
		{context.DeadlineExceeded, "already have taken effect"},
		{failure.Wrap("acquire", failure.Authentication, private), "provider refused"},
		{failure.Wrap("voice_join", failure.Authentication, private), "Discord refused"},
		{failure.Wrap("read", failure.ZeroFrames, private), "no playable audio"},
		{failure.Wrap("read", failure.Codec, private), "couldn't be processed"},
		{failure.Wrap("send", failure.Transport, private), "voice connection"},
		{&failure.Error{Category: failure.Source, Reason: failure.HTTPForbidden, Cause: private}, "refused the audio download"},
		{&failure.Error{Category: failure.Source, Reason: failure.FormatUnavailable, Cause: private}, "no compatible audio format"},
		{private, "command values"},
	} {
		card := commandFailure(tc.err)
		data, err := json.Marshal(card)
		if err != nil || !strings.Contains(card.Description, tc.want) {
			t.Fatalf("guidance: %+v %v", card, err)
		}
		for _, forbidden := range []string{"secret-token", "signature=", "@everyone", "media.invalid"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatal("unsafe error content")
			}
		}
	}
}

func TestPlaybackFeedbackDistinguishesRecoveryFromNextTrack(t *testing.T) {
	s := session.Snapshot{RequestID: "request", Active: true, Connected: true, Recovery: "source_resume", Last: playback.Result{
		Request: playback.Request{ID: "request", AttemptID: "failed"}, Outcome: playback.SourceFailure,
		Failure: &failure.Error{Category: failure.Source, Reason: failure.ConnectionReset},
	}}
	if got := playbackFeedback(s); got == nil || got.Name != "Trying to resume" {
		t.Fatal(got)
	}
	s.Frames = 1
	if got := playbackFeedback(s); got != nil {
		t.Fatal("recovered request still warns", got)
	}
	s.RequestID, s.Recovery = "next-request", ""
	if got := playbackFeedback(s); got == nil || got.Name != "Previous track couldn't finish" || !strings.Contains(got.Value, "queue has moved on") {
		t.Fatal(got)
	}
	s.RequestID, s.Active, s.Frames = "", false, 0
	if got := playbackFeedback(s); got == nil || got.Name != "Playback couldn't continue" {
		t.Fatal(got)
	}
	s.Recovery = "voice_reconnect"
	if got := playbackFeedback(s); got == nil || got.Name != "Reconnecting to voice" {
		t.Fatal(got)
	}
	s.Recovery = "voice_exhausted"
	if got := playbackFeedback(s); got == nil || !strings.Contains(got.Value, "attempts have ended") {
		t.Fatal(got)
	}
	s.Recovery = ""
	for _, outcome := range []playback.Outcome{playback.Completed, playback.Skipped, playback.Stopped, playback.Shutdown} {
		s.Last.Outcome = outcome
		if got := playbackFeedback(s); got != nil {
			t.Fatal("intentional outcome rendered as failure", outcome)
		}
	}
}

func TestDeletedPlayerRecreatedOnlyByExplicitRequest(t *testing.T) {
	r, p := panelFixture(t)
	var methods []string
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/channels/") {
			methods = append(methods, req.Method)
			if req.Method == http.MethodPatch {
				return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"replacement"}`))}, nil
	})
	if err := r.renderInteraction(context.Background(), interactionRef{app: "app", token: "token"}, MappedInteraction{Guild: "guild", Channel: "channel", Name: "now-playing"}, session.Snapshot{}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "PATCH,POST" || p.message != "replacement" {
		t.Fatal(methods, p.message)
	}
	methods = nil
	p.digest = [32]byte{} // Force a changed view so the observer discovers deletion.
	r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, time.Now().Add(time.Minute))
	r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, time.Now().Add(2*time.Minute))
	if strings.Join(methods, ",") != "PATCH" {
		t.Fatal("observer recreated deleted card", methods)
	}
}

func TestAppliedCommandSurvivesPlayerRateLimit(t *testing.T) {
	r, p := panelFixture(t)
	var receipt discordgo.MessageEmbed
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/channels/") {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
		var body struct {
			Embeds []discordgo.MessageEmbed `json:"embeds"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		receipt = body.Embeds[0]
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	start := time.Now()
	if err := r.renderInteraction(context.Background(), interactionRef{app: "app", token: "token"}, MappedInteraction{Guild: "guild", Channel: "channel", Name: "volume"}, session.Snapshot{}, 0, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receipt.Description, "command was applied") || p.next.Before(start.Add(120*time.Second)) {
		t.Fatal("lost acceptance or rate limit", receipt.Description, p.next)
	}
}

func TestSlashAcknowledgementIsPrivateBeforeWork(t *testing.T) {
	r := runtimeFixture(t)
	calls := 0
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			var response discordgo.InteractionResponse
			if err := json.NewDecoder(req.Body).Decode(&response); err != nil {
				return nil, err
			}
			if response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
				t.Error("public command acknowledgement")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	r.executeInteraction(interactionRef{id: "id", app: "app", token: "token"}, MappedInteraction{Guild: "guild", Name: "queue", Operation: session.Operation{Action: session.Inspect}})
}

func TestFinishedCardRequiresDeliveredAudio(t *testing.T) {
	s := session.Snapshot{Current: -1, Last: playback.Result{Outcome: playback.Completed}}
	card, _ := runtimeCard(MappedInteraction{Name: "now-playing"}, s, 0)
	if card.Title == "Queue finished" {
		t.Fatal("zero-frame completion shown")
	}
	s.Last.Frames = 1
	card, _ = runtimeCard(MappedInteraction{Name: "now-playing"}, s, 0)
	if card.Title != "Queue finished" {
		t.Fatal(card.Title)
	}
}

func TestConfigDenialNamesPermissionWithoutExistingSession(t *testing.T) {
	r := runtimeFixture(t)
	_, _, err := r.execute(context.Background(), MappedInteraction{Guild: "guild", Name: "config"})
	if !errors.Is(err, session.ErrManageGuild) {
		t.Fatal(err)
	}
}
