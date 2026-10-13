package bot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/media"
	"github.com/GrainedLotus515/gobard/internal/netguard"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

type httpRoundTrip func(*http.Request) (*http.Response, error)

func (f httpRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDiscordHTTPDoesNotLeakOrRetryRejection(t *testing.T) {
	d := newDiscordHTTP("secret-token")
	calls := 0
	d.client.Transport = httpRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bot secret-token" {
			t.Error("missing bot authorization")
		}
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("secret-token signed-url @everyone"))}, nil
	})
	err := d.send(context.Background(), http.MethodPut, "applications/app/commands", commandDefinitions(), true)
	var f *failure.Error
	if !errors.As(err, &f) || f.Category != failure.Throttled || calls != 1 {
		t.Fatalf("result %v, calls %d", err, calls)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "signed") {
		t.Fatal("raw response escaped boundary")
	}
}

func TestDiscordHTTPCancellation(t *testing.T) {
	d := newDiscordHTTP("secret")
	entered := make(chan struct{})
	d.client.Transport = httpRoundTrip(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.send(ctx, http.MethodPost, "interactions/id/token/callback", struct{}{}, false) }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP cancellation did not join")
	}
}

func TestGatewaySocketCancellationJoinsStalledWrite(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	ctx, cancel := context.WithCancel(context.Background())
	socket := netguard.Wrap(ctx, left)
	done := make(chan error, 1)
	go func() { _, err := socket.Write([]byte("frame")); done <- err }()
	cancel()
	if err := socket.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled write leaked")
	}
	if err := socket.Close(); err != nil {
		t.Fatal(err)
	}
}

type runtimeResolver struct{}

func (runtimeResolver) Resolve(context.Context, string) ([]media.Track, error) {
	return nil, errors.New("not invoked")
}

func runtimeFixture(t *testing.T) *Runtime {
	t.Helper()
	logger := log.New(io.Discard)
	engine, err := playback.New(mappingOpener{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewManager(context.Background(), engine, 2, 20, session.Defaults{Volume: 100, DuckTarget: 70})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(context.Background(), &config.Config{DiscordToken: "test-token", PlayCommandConcurrency: 2, DefaultVolume: 100}, manager, runtimeResolver{}, logger)
	if err != nil {
		manager.Close()
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func TestRuntimeInteractionResponseAndMentionSuppression(t *testing.T) {
	r := runtimeFixture(t)
	var mu sync.Mutex
	var bodies [][]byte
	finished := make(chan struct{})
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		bodies = append(bodies, data)
		n := len(bodies)
		mu.Unlock()
		if n == 2 {
			close(finished)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	i := interactionFixture("queue")
	i.ID = "123"
	i.AppID = "456"
	i.Token = "token"
	r.interaction(i)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("no response")
	}
	r.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("responses %d", len(bodies))
	}
	for _, data := range bodies {
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if nested, ok := body["data"].(map[string]any); ok {
			body = nested
		}
		mentions, ok := body["allowed_mentions"].(map[string]any)
		if !ok {
			t.Fatalf("missing allowed_mentions: %s", data)
		}
		if parse, ok := mentions["parse"].([]any); ok && len(parse) > 0 {
			t.Fatal("mentions permitted")
		}
	}
}

func TestRuntimeSaturationKeepsControlCapacity(t *testing.T) {
	r := runtimeFixture(t)
	for range 2 {
		if !r.admit(true) {
			t.Fatal("play rejected before limit")
		}
	}
	if r.admit(true) {
		t.Fatal("play exceeded limit")
	}
	for range 30 {
		if !r.admit(false) {
			t.Fatal("play consumed control capacity")
		}
	}
	if r.admit(false) {
		t.Fatal("interaction limit exceeded")
	}
	for range 32 {
		<-r.slots
	}
	for range 2 {
		<-r.playSlots
	}
	for range 40 {
		r.reject(interactionRef{})
	}
	if len(r.rejections) != 16 {
		t.Fatal("rejection queue unbounded")
	}
}

func TestRuntimeCloseCancelsAndJoinsAdmittedInteraction(t *testing.T) {
	r := runtimeFixture(t)
	entered, joined := make(chan struct{}), make(chan struct{})
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		close(entered)
		<-req.Context().Done()
		close(joined)
		return nil, req.Context().Err()
	})
	i := interactionFixture("queue")
	i.ID = "123"
	i.AppID = "456"
	i.Token = "token"
	r.interaction(i)
	<-entered
	r.Close()
	select {
	case <-joined:
	default:
		t.Fatal("HTTP work outlived runtime")
	}
	r.callback(func() { t.Error("callback admitted after close") })
	r.interaction(i)
	if len(r.slots) != 0 {
		t.Fatal("interaction admitted after close")
	}
}

func TestRuntimeReadinessDoesNotClaimSourceOrAudioSuccess(t *testing.T) {
	r := runtimeFixture(t)
	if err := r.Ready(context.Background()); err == nil {
		t.Fatal("disconnected runtime ready")
	}
	d := r.Dimensions()
	if d.DiscordConnected || d.CommandsReady || d.Source != "unknown" || d.Pipeline != "unknown" || d.ActiveDelivery != 0 {
		t.Fatalf("invented health evidence: %+v", d)
	}
	r.connected.Store(true)
	if err := r.Ready(context.Background()); err == nil {
		t.Fatal("login alone made runtime ready")
	}
}

func TestRuntimeQueuePaginationPreservesOneBasedPositions(t *testing.T) {
	s := session.Snapshot{Current: 0, Tracks: make([]media.Track, 18)}
	for i := range s.Tracks {
		s.Tracks[i].Title = "track"
	}
	card, components := runtimeQueueCard(s, 2)
	if !strings.Contains(card.Footer.Text, "Page 2/3") || !strings.HasPrefix(card.Fields[1].Name, "10.") {
		t.Fatalf("wrong second page: %+v", card)
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatal("missing navigation")
	}
	next, ok := row.Components[1].(discordgo.Button)
	if !ok || next.CustomID != "music:queue:page:3" {
		t.Fatal("incorrect next page")
	}
}
