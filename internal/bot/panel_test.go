package bot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/media"
	"github.com/GrainedLotus515/gobard/internal/session"
)

func panelFixture(t *testing.T) (*Runtime, *playerPanel) {
	t.Helper()
	r := runtimeFixture(t)
	if _, err := r.sessions.Create("guild", mappingOutput{}); err != nil {
		t.Fatal(err)
	}
	p := r.panel("guild")
	p.channel, p.message, p.view = "channel", "message", MappedInteraction{Name: "now-playing"}
	return r, p
}

func TestPanelCoalescesProgressAndUpdatesState(t *testing.T) {
	r, _ := panelFixture(t)
	var cards []discordgo.MessageEmbed
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		var edit struct {
			Embeds   []discordgo.MessageEmbed          `json:"embeds"`
			Mentions *discordgo.MessageAllowedMentions `json:"allowed_mentions"`
		}
		if err := json.NewDecoder(req.Body).Decode(&edit); err != nil {
			return nil, err
		}
		if edit.Mentions == nil || len(edit.Mentions.Parse) != 0 {
			t.Error("unsafe automatic edit")
		}
		cards = append(cards, edit.Embeds[0])
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	now := time.Now()
	s := session.Snapshot{Current: 0, Active: true, Connected: true, Frames: 100, Tracks: []media.Track{{Title: "First", Duration: time.Minute}}}
	r.updatePanel(r.ctx, "guild", s, now)
	s.Position = 2 * time.Second
	r.updatePanel(r.ctx, "guild", s, now.Add(4*time.Second))
	if len(cards) != 1 {
		t.Fatal("unchanged progress generated edit")
	}
	s.Position = 16 * time.Second
	r.updatePanel(r.ctx, "guild", s, now.Add(16*time.Second))
	s.Paused = true
	r.updatePanel(r.ctx, "guild", s, now.Add(17*time.Second))
	if len(cards) != 2 {
		t.Fatal("minimum update interval ignored")
	}
	r.updatePanel(r.ctx, "guild", s, now.Add(19*time.Second))
	if cards[2].Author.Name != "Paused" {
		t.Fatal("pause not reflected")
	}
	s.Tracks[0].Title, s.Paused, s.Position = "Second", false, 0
	r.updatePanel(r.ctx, "guild", s, now.Add(22*time.Second))
	if cards[3].Title != "Second" {
		t.Fatal("track change not reflected")
	}
	s.Current, s.Tracks, s.Active = -1, nil, false
	r.updatePanel(r.ctx, "guild", s, now.Add(25*time.Second))
	if cards[4].Title != "Nothing playing" {
		t.Fatal("drain not reflected")
	}
}

func TestPanelFailuresAreFiniteAndRespectRateLimit(t *testing.T) {
	for _, code := range []int{404, 403, 429, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			r, p := panelFixture(t)
			calls := 0
			r.rest.client.Transport = httpRoundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: code, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader("private response"))}, nil
			})
			now := time.Now()
			for i := 0; i < 600; i++ {
				r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, now.Add(time.Duration(i)*time.Second))
			}
			want := 3
			if code == 403 || code == 404 {
				want = 1
			}
			if calls != want || !p.suspended || p.message != "message" {
				t.Fatalf("retry budget: calls=%d message=%q", calls, p.message)
			}
		})
	}
}

func TestPanel429DoesNotRetryBeforeHeaderDelay(t *testing.T) {
	r, _ := panelFixture(t)
	calls := 0
	r.rest.client.Transport = httpRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	now := time.Now()
	r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, now)
	r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, now.Add(119*time.Second))
	if calls != 1 {
		t.Fatal("rate limit ignored")
	}
	r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, now.Add(120*time.Second))
	if calls != 2 {
		t.Fatal("bounded retry not performed")
	}
}

func TestPanelRejectsAllStaleControls(t *testing.T) {
	r, _ := panelFixture(t)
	m := MappedInteraction{Guild: "guild", Component: true, Message: "older", Operation: session.Operation{Action: session.TogglePause}}
	if err := r.checkPlayer(context.Background(), m); !errors.Is(err, errStalePlayer) {
		t.Fatal(err)
	}
	m.Operation.Action = session.Inspect
	if err := r.checkPlayer(context.Background(), m); !errors.Is(err, errStalePlayer) {
		t.Fatal(err)
	}
}

func TestPanelLongRateLimitSuspendsUpdates(t *testing.T) {
	r, p := panelFixture(t)
	calls := 0
	r.rest.client.Transport = httpRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"7200"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	now := time.Now()
	r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, now)
	r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, now.Add(2*time.Hour))
	if calls != 1 || !p.suspended || p.message != "message" {
		t.Fatal("long rate limit was shortened")
	}
}

func TestPanelJoinsCanceledHTTPAndSkipsBusyUpdates(t *testing.T) {
	r, p := panelFixture(t)
	entered := make(chan struct{})
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		close(entered)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, time.Now()) }()
	<-entered
	r.updatePanel(r.ctx, "guild", session.Snapshot{Current: -1}, time.Now())
	r.cancel()
	wg.Wait()
	if len(p.gate) != 0 {
		t.Fatal("panel retained ownership after cancellation")
	}
}

func TestDiscordMessageResponseIsBoundedAndProjected(t *testing.T) {
	d := newDiscordHTTP("secret")
	for _, body := range []string{`{"id":"123","content":"private"}`, `{"id":"../escape"}`, strings.Repeat("x", 65537)} {
		d.client.Transport = httpRoundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		id, err := d.request(context.Background(), http.MethodPatch, "test", struct{}{}, false, true)
		if strings.Contains(body, `"123"`) {
			if err != nil || id != "123" {
				t.Fatal(id, err)
			}
		} else if err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}

func TestPersistentPanelFollowsOnlyPlayAndRetiresOldControls(t *testing.T) {
	r, p := panelFixture(t)
	type request struct {
		method, path string
		body         struct {
			Embeds     []*discordgo.MessageEmbed `json:"embeds"`
			Components []json.RawMessage         `json:"components"`
			Flags      discordgo.MessageFlags    `json:"flags"`
		}
	}
	var requests []request
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		var body struct {
			Embeds     []*discordgo.MessageEmbed `json:"embeds"`
			Components []json.RawMessage         `json:"components"`
			Flags      discordgo.MessageFlags    `json:"flags"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request{req.Method, req.URL.Path, body})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"new-panel"}`))}, nil
	})
	idle := session.Snapshot{Current: -1}
	show := func(name, channel string, s session.Snapshot) {
		t.Helper()
		if err := r.showPanel(r.ctx, MappedInteraction{Name: name, Channel: channel}, s, p); err != nil {
			t.Fatal(err)
		}
	}
	show("play", "channel", idle)
	show("play", "channel", idle)
	if len(requests) != 1 {
		t.Fatal("identical cards should not shimmer through repeated edits", len(requests))
	}
	show("queue", "elsewhere", idle)
	show("now-playing", "elsewhere", idle)
	if p.channel != "channel" || p.message != "message" {
		t.Fatal("read-only command moved player")
	}
	playing := session.Snapshot{Current: 0, Connected: true, Active: true, Frames: 1, Tracks: []media.Track{{Title: "Song"}}}
	show("play", "channel", playing)
	show("play", "channel", idle)
	show("play", "channel", playing)
	for _, req := range requests {
		if req.method != http.MethodPatch {
			t.Fatal("voice lifecycle created another message")
		}
	}
	show("play", "elsewhere", playing)
	retire, create := requests[len(requests)-2], requests[len(requests)-1]
	if retire.method != http.MethodPatch || retire.body.Embeds[0].Title != "Player moved" || len(retire.body.Components) != 0 {
		t.Fatal("old player was not retired", retire)
	}
	if create.method != http.MethodPost || !strings.Contains(create.path, "/channels/elsewhere/messages") || create.body.Flags&discordgo.MessageFlagsSuppressNotifications == 0 {
		t.Fatal("new player not quietly created", create)
	}
	if p.channel != "elsewhere" || p.message != "new-panel" {
		t.Fatal("new player not tracked")
	}
	p.anchor = "elsewhere"
	before := len(requests)
	show("play", "channel", playing)
	if len(requests) != before || p.channel != "elsewhere" {
		t.Fatal("anchored player moved or redundantly edited")
	}
}

func TestPanelTemporaryFailureKeepsIdentityForManualRefresh(t *testing.T) {
	r, p := panelFixture(t)
	r.panelResult("guild", p, time.Now(), errors.New("temporary"))
	r.panelResult("guild", p, time.Now(), errors.New("temporary"))
	r.panelResult("guild", p, time.Now(), errors.New("temporary"))
	if !p.suspended || p.message != "message" {
		t.Fatal("lost message identity")
	}
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPatch || !strings.HasSuffix(req.URL.Path, "/channels/channel/messages/message") {
			t.Fatal("refresh created a duplicate", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	if err := r.showPanel(r.ctx, MappedInteraction{Name: "now-playing", Channel: "elsewhere"}, session.Snapshot{Current: -1}, p); err != nil {
		t.Fatal(err)
	}
	if p.suspended || p.message != "message" {
		t.Fatal("refresh did not restore existing player")
	}
}

func TestPlayerAnchorRequiresManageGuild(t *testing.T) {
	r, p := panelFixture(t)
	m := MappedInteraction{Guild: "guild", Channel: "music", PlayerChannelSetting: true, AnchorPlayer: true}
	if _, _, err := r.execute(r.ctx, m); !errors.Is(err, session.ErrManageGuild) {
		t.Fatal(err)
	}
	m.ManageGuild = true
	if _, _, err := r.execute(r.ctx, m); err != nil {
		t.Fatal(err)
	}
	if p.anchor != "music" {
		t.Fatal("anchor not set")
	}
	m.AnchorPlayer = false
	if _, _, err := r.execute(r.ctx, m); err != nil {
		t.Fatal(err)
	}
	if p.anchor != "" {
		t.Fatal("anchor not cleared")
	}
}

func TestPanelDoesNotCreateWhenRetirementFails(t *testing.T) {
	r, p := panelFixture(t)
	calls := 0
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPatch {
			t.Fatal("created duplicate after retirement failure")
		}
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	if err := r.showPanel(r.ctx, MappedInteraction{Name: "play", Channel: "elsewhere"}, session.Snapshot{Current: -1}, p); err == nil {
		t.Fatal("lost retirement error")
	}
	if calls != 1 || p.message != "message" || p.channel != "channel" {
		t.Fatal("lost old player identity")
	}
}

func TestSupersededReadOnlyResponseCannotReactivateOldPlayer(t *testing.T) {
	r, p := panelFixture(t)
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || strings.Contains(req.URL.Path, "@original") {
			t.Fatal("stale response edited old player")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	err := r.renderInteraction(r.ctx, interactionRef{app: "app", token: "token"}, MappedInteraction{Guild: "guild", Channel: "old-channel", Message: "old-message", Component: true, Name: "now-playing"}, session.Snapshot{Current: -1}, 0, nil)
	if err != nil || p.message != "message" || p.channel != "channel" {
		t.Fatal("stale response adopted", err)
	}
}

func TestPanelRefreshRecoversFailedMoveInDestination(t *testing.T) {
	r, p := panelFixture(t)
	posts := 0
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			posts++
			if !strings.HasSuffix(req.URL.Path, "/channels/destination/messages") {
				t.Fatal("recreated player in old channel")
			}
			if posts == 1 {
				return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"replacement"}`))}, nil
	})
	idle := session.Snapshot{Current: -1}
	if err := r.showPanel(r.ctx, MappedInteraction{Name: "play", Channel: "destination"}, idle, p); err == nil {
		t.Fatal("missing creation error")
	}
	if err := r.showPanel(r.ctx, MappedInteraction{Name: "now-playing", Channel: "channel"}, idle, p); err != nil {
		t.Fatal(err)
	}
	if posts != 2 || p.channel != "destination" || p.message != "replacement" {
		t.Fatal("destination not retained")
	}
}
