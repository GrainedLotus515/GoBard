package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GrainedLotus515/gobard/internal/player"
)

func TestNextTrackPrefetchWaitsForPlaybackAndHydratesImmediateSuccessor(t *testing.T) {
	p := player.NewManager().GetPlayer("guild-prefetch")
	current := &player.Track{Title: "current", URL: "https://www.youtube.com/watch?v=current"}
	next := &player.Track{Title: "next", URL: "https://www.youtube.com/watch?v=next"}
	later := &player.Track{Title: "later", URL: "https://www.youtube.com/watch?v=later"}
	p.Queue.Add(current)
	p.Queue.Add(next)
	p.Queue.Add(later)
	p.Queue.Next()

	called := make(chan string, 1)
	b := &Bot{prefetchVideoInfoFn: func(_ context.Context, url string) (*player.Track, error) {
		called <- url
		return &player.Track{Title: "hydrated next", URL: url}, nil
	}}
	started := make(chan struct{})
	done := make(chan struct{})
	b.deferNextTrackPrefetchUntilPlaybackStarts(p, started, done)

	select {
	case url := <-called:
		t.Fatalf("prefetch started before playback: %s", url)
	case <-time.After(20 * time.Millisecond):
	}
	close(started)

	select {
	case url := <-called:
		if url != next.URL {
			t.Fatalf("prefetched URL = %q, want immediate successor %q", url, next.URL)
		}
	case <-time.After(time.Second):
		t.Fatal("immediate-successor prefetch did not start")
	}
	b.waitForAsyncWork()

	if got := p.Queue.Peek(); got == nil || got.Title != "hydrated next" || got.Resolution != nil {
		t.Fatalf("prefetched successor = %#v, want hydrated replacement", got)
	}
	tracks, _ := p.Queue.Snapshot()
	if len(tracks) != 3 || tracks[2] != later {
		t.Fatalf("prefetch changed tracks beyond the immediate successor: %#v", tracks)
	}
}

func TestNextTrackPrefetchIsCanceledAndJoinedAtShutdown(t *testing.T) {
	p := player.NewManager().GetPlayer("guild-prefetch-shutdown")
	p.Queue.Add(&player.Track{Title: "current", URL: "https://www.youtube.com/watch?v=current"})
	p.Queue.Add(&player.Track{Title: "next", URL: "https://www.youtube.com/watch?v=next"})
	p.Queue.Next()

	entered := make(chan struct{})
	exited := make(chan struct{})
	b := &Bot{prefetchVideoInfoFn: func(ctx context.Context, _ string) (*player.Track, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	}}
	started := make(chan struct{})
	close(started)
	b.deferNextTrackPrefetchUntilPlaybackStarts(p, started, make(chan struct{}))

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("prefetch did not enter cancellable work")
	}
	if err := b.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("Stop() returned without joining canceled prefetch")
	}
	resolution := p.Queue.Peek().Resolution
	if resolution == nil {
		t.Fatal("canceled successor lost its resolution result")
	}
	if _, err := resolution.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prefetch resolution error = %v, want context canceled", err)
	}
}
