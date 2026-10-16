package source

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

const testAudio = "ffmpeg -nostdin -hide_banner -loglevel error -f lavfi -i sine=frequency=440:duration=0.1 -f wav pipe:1\n"
const testMediaURL = "https://www.youtube.com/watch?v=fixture"

type acquisitionOutput func(context.Context, []byte) error

func (f acquisitionOutput) Send(ctx context.Context, p []byte) error { return f(ctx, p) }

func TestFailureAfterAcceptedAudioCannotBecomeCompletion(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "delivered")
	quoted := "'" + strings.ReplaceAll(marker, "'", "'\"'\"'") + "'"
	// Enough input for FFmpeg's probing while stdout remains open. A tiny WAV
	// can legitimately wait for EOF before producing its first encoded packet.
	o := fakeExtractor(t, strings.Replace(testAudio, "duration=0.1", "duration=10", 1)+"while [ ! -f "+quoted+" ]; do sleep 0.01; done\nprintf 'connection reset' >&2\nexit 7\n")
	e, err := playback.New(o, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := e.Run(ctx, playback.Request{MediaID: testMediaURL}, acquisitionOutput(func(context.Context, []byte) error {
		return os.WriteFile(marker, nil, 0600)
	}))
	if result.Outcome != playback.SourceFailure || result.Frames == 0 || result.Failure == nil || result.Failure.Reason != failure.ConnectionReset || result.Failure.ExitCode != 7 {
		t.Fatalf("%+v failure=%+v", result, result.Failure)
	}
	if lease, ok := o.store.Acquire(cache.GenerateKey(testMediaURL)); ok {
		lease.Release()
		t.Fatal("failed acquisition published")
	}
}

func TestPreparedAudioCanRemainPausedBeyondStartupBudget(t *testing.T) {
	o := fakeExtractor(t, testAudio)
	o.startupBudget = time.Second
	stream, err := o.Open(context.Background(), playback.Request{MediaID: testMediaURL})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	// Waiting beyond the original startup budget is the externally visible
	// pause contract; no decoder read is requested during this interval.
	time.Sleep(o.startupBudget + 10*time.Millisecond)
	var packet [playback.MaxPacketBytes]byte
	if n, err := stream.Next(context.Background(), packet[:]); err != nil || n == 0 {
		t.Fatal("prepared pause expired", n, err)
	}
}

func TestCancelSaturatedAcquisitionJoinsAndAbortsScratch(t *testing.T) {
	o := fakeExtractor(t, "ffmpeg -nostdin -hide_banner -loglevel error -f lavfi -i sine=frequency=440:duration=300 -f wav pipe:1\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := o.Open(ctx, playback.Request{MediaID: testMediaURL})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	s, ok := stream.(*extractorStream)
	if !ok {
		t.Fatal("unexpected stream implementation")
	}
	for {
		s.buffer.mu.Lock()
		full := s.buffer.size == len(s.buffer.data)
		s.buffer.mu.Unlock()
		if full {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture did not saturate buffer")
		case <-time.After(10 * time.Millisecond):
		}
	}
	start := time.Now()
	cancel()
	_ = stream.Close()
	_ = stream.Close()
	if time.Since(start) > 3*time.Second {
		t.Fatal("shutdown exceeded process join budget")
	}
	w, err := o.store.TryBegin("after.webm", 8)
	if err != nil {
		t.Fatal("scratch writer retained after close", err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	check, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	release, err := o.limiter.Acquire(check)
	if err != nil {
		t.Fatal("extractor capacity retained", err)
	}
	release()
}

func runSource(t *testing.T, o *YouTubeOpener, request playback.Request) playback.Result {
	t.Helper()
	e, err := playback.New(o, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return e.Run(ctx, request, discardOutput{})
}

func TestAcquisitionPublishesReusableLeasedAudio(t *testing.T) {
	o := fakeExtractor(t, testAudio)
	request := playback.Request{MediaID: testMediaURL}
	result := runSource(t, o, request)
	if result.Outcome != playback.Completed || result.Frames != 5 {
		t.Fatalf("%+v", result)
	}
	if err := os.Remove(o.executable); err != nil {
		t.Fatal(err)
	}
	stream, err := o.Open(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.store.Clear(); !errors.Is(err, cache.ErrEntryLeased) {
		t.Fatal("cache playback lacked a lease", err)
	}
	_ = stream.Close() // Intentional decoder cancellation may return its exit status.
	request.Offset = playback.FrameDuration
	result = runSource(t, o, request)
	if result.Outcome != playback.Completed || result.Frames != 4 {
		t.Fatalf("cached seek: %+v", result)
	}
}

func TestAcquisitionDoesNotPublishFailedOrLiveAudio(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		live         bool
		outcome      playback.Outcome
	}{
		{"source failure", testAudio + "exit 7\n", false, playback.SourceFailure},
		{"codec failure", "printf invalid\n", false, playback.EncodeFailure},
		{"live", testAudio, true, playback.Completed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := fakeExtractor(t, tc.script)
			result := runSource(t, o, playback.Request{MediaID: testMediaURL, Live: tc.live})
			if result.Outcome != tc.outcome {
				t.Fatalf("%+v", result)
			}
			if lease, ok := o.store.Acquire(cache.GenerateKey(testMediaURL)); ok {
				lease.Release()
				t.Fatal("invalid cache hit")
			}
			w, err := o.store.TryBegin("after.webm", 8)
			if err != nil {
				t.Fatal("cache writer leaked", err)
			}
			if err := w.Abort(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCacheSaturationAndByteLimitDoNotFailPlayback(t *testing.T) {
	for _, saturated := range []bool{false, true} {
		o := fakeExtractor(t, testAudio)
		store, err := cache.NewCache(t.TempDir(), 8)
		if err != nil {
			t.Fatal(err)
		}
		o.store = store
		if saturated {
			w, err := store.TryBegin("busy.webm", 8)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := w.Abort(); err != nil {
					t.Error(err)
				}
			}()
		}
		result := runSource(t, o, playback.Request{MediaID: testMediaURL})
		if result.Outcome != playback.Completed || result.Frames != 5 {
			t.Fatalf("%+v", result)
		}
	}
}

func TestStartupDeadlineIncludesAdmissionAndEmptyExtractor(t *testing.T) {
	for _, admission := range []bool{false, true} {
		o := fakeExtractor(t, "sleep 60 &\nwait\n")
		o.startupBudget = 50 * time.Millisecond
		if admission {
			release, err := o.limiter.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer release()
		}
		result := runSource(t, o, playback.Request{MediaID: testMediaURL})
		if result.Outcome != playback.SourceFailure || result.Failure == nil || result.Failure.Reason != failure.StartupTimeout || result.Frames != 0 {
			t.Fatalf("%+v failure=%+v", result, result.Failure)
		}
	}
}

func TestExtractorSlotReleasedBeforeBufferedPlaybackEnds(t *testing.T) {
	o := fakeExtractor(t, testAudio)
	stream, err := o.Open(context.Background(), playback.Request{MediaID: testMediaURL})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := o.limiter.Acquire(ctx)
	if err != nil {
		t.Fatal("completed extractor still held capacity", err)
	}
	release()
}
