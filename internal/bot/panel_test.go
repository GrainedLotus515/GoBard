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
	r.updatePanel("guild", s, now)
	s.Position = 2 * time.Second
	r.updatePanel("guild", s, now.Add(4*time.Second))
	if len(cards) != 1 {
		t.Fatal("unchanged progress generated edit")
	}
	s.Position = 16 * time.Second
	r.updatePanel("guild", s, now.Add(16*time.Second))
	s.Paused = true
	r.updatePanel("guild", s, now.Add(17*time.Second))
	if len(cards) != 2 {
		t.Fatal("minimum update interval ignored")
	}
	r.updatePanel("guild", s, now.Add(19*time.Second))
	if cards[2].Author.Name != "Paused" {
		t.Fatal("pause not reflected")
	}
	s.Tracks[0].Title, s.Paused, s.Position = "Second", false, 0
	r.updatePanel("guild", s, now.Add(22*time.Second))
	if cards[3].Title != "Second" {
		t.Fatal("track change not reflected")
	}
	s.Current, s.Tracks, s.Active = -1, nil, false
	r.updatePanel("guild", s, now.Add(25*time.Second))
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
				r.updatePanel("guild", session.Snapshot{Current: -1}, now.Add(time.Duration(i)*time.Second))
			}
			want := 3
			if code == 403 || code == 404 {
				want = 1
			}
			if calls != want || p.message != "" {
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
	r.updatePanel("guild", session.Snapshot{Current: -1}, now)
	r.updatePanel("guild", session.Snapshot{Current: -1}, now.Add(119*time.Second))
	if calls != 1 {
		t.Fatal("rate limit ignored")
	}
	r.updatePanel("guild", session.Snapshot{Current: -1}, now.Add(120*time.Second))
	if calls != 2 {
		t.Fatal("bounded retry not performed")
	}
}

func TestPanelRejectsStaleMutationButAllowsReadOnlyRefresh(t *testing.T) {
	r, _ := panelFixture(t)
	m := MappedInteraction{Guild: "guild", Component: true, Message: "older", Operation: session.Operation{Action: session.TogglePause}}
	if err := r.checkPlayer(context.Background(), m); !errors.Is(err, errStalePlayer) {
		t.Fatal(err)
	}
	m.Operation.Action = session.Inspect
	if err := r.checkPlayer(context.Background(), m); err != nil {
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
	r.updatePanel("guild", session.Snapshot{Current: -1}, now)
	r.updatePanel("guild", session.Snapshot{Current: -1}, now.Add(2*time.Hour))
	if calls != 1 || p.message != "" {
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
	go func() { defer wg.Done(); r.updatePanel("guild", session.Snapshot{Current: -1}, time.Now()) }()
	<-entered
	r.updatePanel("guild", session.Snapshot{Current: -1}, time.Now())
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
