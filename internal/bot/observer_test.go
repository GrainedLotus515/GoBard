package bot

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

func TestStalledPanelCannotDelayFailureHealthAndIsJoined(t *testing.T) {
	r, _ := panelFixture(t)
	target, ok := r.sessions.Get("guild")
	if !ok {
		t.Fatal("missing session")
	}
	entered, httpDone, observed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		close(entered)
		defer close(httpDone)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	go func() { defer close(observed); r.observe("guild", target) }()
	t.Cleanup(func() { r.cancel(); <-observed })
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("panel edit did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ticket, err := target.Enqueue(ctx, playback.Request{MediaID: "failing-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := ticket.Wait(ctx); err != nil || result.Outcome != playback.SourceFailure {
		t.Fatal(result, err)
	}
	for r.Dimensions().Source != "last_failure" {
		select {
		case <-ctx.Done():
			t.Fatal("HTTP blocked failure health")
		case <-time.After(10 * time.Millisecond):
		}
	}
	target.Close() // Session removal must cancel HTTP without canceling the runtime.
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("observer did not join editor")
	}
	select {
	case <-httpDone:
	default:
		t.Fatal("observer returned before HTTP completed")
	}
}
