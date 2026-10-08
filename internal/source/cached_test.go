package source

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

func TestDownloadByteLimitAndAtomicCachePublication(t *testing.T) {
	o := fakeExtractor(t, "printf '123456789'\n")
	store, err := cache.NewCache(t.TempDir(), 8)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://www.youtube.com/watch?v=fixture"
	key := cache.GenerateKey(url)
	_, err = store.GetOrCreate(key, func(path string) error { return o.Download(context.Background(), url, path, 8) })
	if !errors.Is(err, cache.ErrEntryTooLarge) {
		t.Fatal(err)
	}
	if _, ok := store.Get(key); ok {
		t.Fatal("oversized partial published")
	}
	o = fakeExtractor(t, "printf '12345678'\n")
	path, err := store.GetOrCreate(key, func(path string) error { return o.Download(context.Background(), url, path, 8) })
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "12345678" {
		t.Fatal(string(data), err)
	}
	reopened, err := cache.NewCache(filepath.Dir(path), 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Get(key); !ok {
		t.Fatal("cache did not survive restart")
	}
}

type onePacketSource struct{}
type onePacket struct{ sent bool }

func (onePacketSource) Open(context.Context, playback.Request) (playback.Stream, error) {
	return &onePacket{}, nil
}
func (s *onePacket) Next(_ context.Context, b []byte) (int, error) {
	if s.sent {
		return 0, io.EOF
	}
	s.sent = true
	b[0] = 1
	return 1, nil
}
func (*onePacket) Close() error { return nil }

type blockingDownload struct {
	started chan struct{}
	joined  chan struct{}
}

func (d blockingDownload) Download(ctx context.Context, _, _ string, _ int64) error {
	close(d.started)
	<-ctx.Done()
	close(d.joined)
	return ctx.Err()
}

func TestCacheWorkerCancellationAndHintBudget(t *testing.T) {
	store, err := cache.NewCache(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	d := blockingDownload{started: make(chan struct{}), joined: make(chan struct{})}
	c, err := NewCached(context.Background(), store, onePacketSource{}, d, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := playback.New(c, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	request := playback.Request{MediaID: "https://www.youtube.com/watch?v=fixture"}
	if result := engine.Run(context.Background(), request, discardOutput{}); result.Outcome != playback.Completed {
		t.Fatal(result)
	}
	select {
	case <-d.started:
	case <-time.After(time.Second):
		t.Fatal("cache worker did not start")
	}
	for range 100 {
		if result := engine.Run(context.Background(), request, discardOutput{}); result.Outcome != playback.Completed {
			t.Fatal(result)
		}
	}
	if len(c.warm) != 8 {
		t.Fatal("cache hints are not bounded", len(c.warm))
	}
	c.Close()
	c.Close()
	select {
	case <-d.joined:
	default:
		t.Fatal("worker not joined")
	}
	if _, err := c.Open(context.Background(), request); err == nil {
		t.Fatal("closed source accepted new work")
	}
}

func TestCachedAudioHoldsLeaseThroughDecoderCleanup(t *testing.T) {
	o := fakeExtractor(t, "ffmpeg -nostdin -hide_banner -loglevel error -f lavfi -i sine=frequency=440:duration=0.04 -f wav pipe:1\n")
	store, err := cache.NewCache(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://www.youtube.com/watch?v=fixture"
	if _, err := store.GetOrCreate(cache.GenerateKey(url), func(path string) error { return o.Download(context.Background(), url, path, 1<<20) }); err != nil {
		t.Fatal(err)
	}
	c, err := NewCached(context.Background(), store, o, o, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	stream, err := c.Open(context.Background(), playback.Request{MediaID: url})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); !errors.Is(err, cache.ErrEntryLeased) {
		t.Fatal("active cache entry was not leased", err)
	}
	packet := make([]byte, playback.MaxPacketBytes)
	frames := 0
	for {
		_, err := stream.Next(context.Background(), packet)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		frames++
	}
	if frames != 2 {
		t.Fatalf("cached fixture frames: got %d, want 2", frames)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err := playback.New(c, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	result := engine.Run(context.Background(), playback.Request{MediaID: url, Offset: playback.FrameDuration}, discardOutput{})
	if result.Outcome != playback.Completed || result.Frames != 1 {
		t.Fatalf("cached resume did not honor delivered position: %+v", result)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
}
