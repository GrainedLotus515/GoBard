package source

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
)

type discardOutput struct{}

func (discardOutput) Send(context.Context, []byte) error { return nil }

func fakeExtractor(t *testing.T, script string) *YouTubeOpener {
	t.Helper()
	path := filepath.Join(t.TempDir(), "extractor")
	// #nosec G306 -- executable test fixture, owner-only, in a private temporary directory.
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewCache(t.TempDir(), 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	o, err := NewYouTubeOpener(processlimit.New(1), store, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	o.executable = path
	o.preparationDelay = 0
	return o
}

func TestExtractorAuthenticationIsNotSuccessfulEOF(t *testing.T) {
	o := fakeExtractor(t, "printf '%s\\n' \"ERROR: Sign in to confirm you're not a bot https://signed.invalid/?cookie=secret\" >&2\nexit 1\n")
	var logs bytes.Buffer
	e, err := playback.New(o, log.New(&logs))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := e.Run(ctx, playback.Request{ID: "request", AttemptID: "attempt", Provider: "youtube", MediaID: "https://www.youtube.com/watch?v=fixture"}, discardOutput{})
	if r.Outcome != playback.SourceFailure || r.Frames != 0 || r.Failure == nil || r.Failure.Category != failure.Authentication {
		t.Fatalf("%+v failure=%v", r, r.Failure)
	}
	if strings.Contains(logs.String(), "secret") || strings.Count(logs.String(), "playback.terminal") != 1 {
		t.Fatal(logs.String())
	}
	// Capacity must have been released even though both decoder and source failed.
	release, err := o.limiter.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestExtractorRejectsUnsafeInputBeforeLaunch(t *testing.T) {
	o := fakeExtractor(t, "exit 99\n")
	for _, url := range []string{"file:///etc/passwd", "http://127.0.0.1/", "https://www.youtube.com/watch?v=x&redirect=http://localhost", "https://youtube.com@localhost/watch?v=x"} {
		_, err := o.Open(context.Background(), playback.Request{MediaID: url})
		var classified *failure.Error
		if !errors.As(err, &classified) || classified.Stage != "validate" {
			t.Fatalf("%s: %v", url, err)
		}
	}
}

func TestExtractorCancellationJoinsProcessAndReturnsBudget(t *testing.T) {
	// A child also inherits the pipe; cancellation must kill the group.
	o := fakeExtractor(t, "sleep 60 &\nwait\n")
	e, err := playback.New(o, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := e.Run(ctx, playback.Request{MediaID: "https://www.youtube.com/watch?v=fixture"}, discardOutput{})
	if r.Outcome != playback.Shutdown || time.Since(start) > 3*time.Second {
		t.Fatal(r, time.Since(start))
	}
	check, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	release, err := o.limiter.Acquire(check)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestExtractorExitAfterAudioRemainsAuthoritative(t *testing.T) {
	for _, status := range []int{0, 7} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			o := fakeExtractor(t, "ffmpeg -nostdin -hide_banner -loglevel error -f lavfi -i sine=frequency=440:duration=0.1 -f wav pipe:1\nexit "+strconv.Itoa(status)+"\n")
			engine, err := playback.New(o, log.New(io.Discard))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := engine.Run(ctx, playback.Request{Provider: "youtube", MediaID: "https://www.youtube.com/watch?v=fixture"}, discardOutput{})
			want := playback.Completed
			if status != 0 {
				want = playback.SourceFailure
			}
			if result.Outcome != want || (status == 0 && result.Frames == 0) {
				t.Fatalf("%+v failure=%v", result, result.Failure)
			}
			if status != 0 && (result.Failure == nil || result.Failure.ExitCode != status) {
				t.Fatal(result.Failure)
			}
		})
	}
}
