package bot

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/player"
)

func TestPlayLoopSeekReplaysCurrentTrackBeforeAdvancing(t *testing.T) {
	cacheStore, err := cache.NewCache(t.TempDir(), 64*1024*1024)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	trackA := &player.Track{
		Title:    "track-a",
		URL:      "https://example.com/a",
		Duration: 2 * time.Minute,
	}
	trackB := &player.Track{
		Title:    "track-b",
		URL:      "https://example.com/b",
		Duration: 2 * time.Minute,
	}

	seedCacheEntry(t, cacheStore, trackA.URL)
	seedCacheEntry(t, cacheStore, trackB.URL)

	manager := player.NewManager()
	guildID := "guild-seek"
	p := manager.GetPlayer(guildID)
	p.SetVoiceConnection(stubVoiceConn{})
	p.Queue.Add(trackA)
	p.Queue.Add(trackB)

	var playOrder []string
	waitCalls := 0

	b := &Bot{
		PlayerManager: manager,
		Cache:         cacheStore,
	}

	b.playTrackFn = func(gp *player.GuildPlayer) error {
		current := gp.Queue.Current()
		if current == nil {
			return fmt.Errorf("expected current track to be selected before playback")
		}
		playOrder = append(playOrder, current.Title)
		return nil
	}

	b.waitForCompletionFn = func(gp *player.GuildPlayer) {
		waitCalls++
		switch waitCalls {
		case 1:
			if err := gp.Seek(15 * time.Second); err != nil {
				t.Errorf("Seek() error = %v", err)
			}
		case 3:
			// End loop after validating second track playback.
			gp.ClearVoiceConnection()
		}
	}

	done := make(chan struct{})
	go func() {
		b.playLoop(guildID, "channel")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("playLoop did not finish in time")
	}

	want := []string{"track-a", "track-a", "track-b"}
	if !reflect.DeepEqual(playOrder, want) {
		t.Fatalf("play order = %v, want %v", playOrder, want)
	}
}

func TestPlayLoopDefersCacheUntilPlaybackStarts(t *testing.T) {
	cacheStore, manager, p := newPlayLoopTestEnv(t)
	track := &player.Track{
		Title: "track-miss",
		URL:   "https://example.com/miss",
	}
	p.Queue.Add(track)

	playbackMayStart := make(chan struct{})
	waitEntered := make(chan struct{})
	finishPlayback := make(chan struct{})
	cacheCalled := make(chan struct{}, 1)

	b := &Bot{
		PlayerManager: manager,
		Cache:         cacheStore,
	}
	b.playTrackFn = func(*player.GuildPlayer) error { return nil }
	b.waitForPlaybackStartFn = func(*player.GuildPlayer) bool {
		<-playbackMayStart
		return true
	}
	b.cacheTrackFn = func(_ string, path string) error {
		select {
		case cacheCalled <- struct{}{}:
		default:
		}
		return os.WriteFile(path, []byte("cached-audio"), 0o644)
	}
	b.waitForCompletionFn = func(gp *player.GuildPlayer) {
		close(waitEntered)
		<-finishPlayback
		gp.ClearVoiceConnection()
	}

	done := runPlayLoopAsync(b, p.GuildID)

	select {
	case <-waitEntered:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("waitForCompletionFn was not reached")
	}

	select {
	case <-cacheCalled:
		t.Fatal("cache download started before playback start signal")
	case <-time.After(75 * time.Millisecond):
	}

	close(playbackMayStart)

	select {
	case <-cacheCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cache download did not start after playback start signal")
	}

	close(finishPlayback)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("playLoop did not finish in time")
	}
}

func TestPlayLoopSkipsDeferredCacheIfPlaybackEndsBeforeStart(t *testing.T) {
	cacheStore, manager, p := newPlayLoopTestEnv(t)
	p.Queue.Add(&player.Track{
		Title: "track-no-start",
		URL:   "https://example.com/no-start",
	})

	var cacheCalls int32

	b := &Bot{
		PlayerManager: manager,
		Cache:         cacheStore,
	}
	b.playTrackFn = func(*player.GuildPlayer) error { return nil }
	b.waitForPlaybackStartFn = func(*player.GuildPlayer) bool { return false }
	b.cacheTrackFn = func(_ string, path string) error {
		atomic.AddInt32(&cacheCalls, 1)
		return os.WriteFile(path, []byte("cached-audio"), 0o644)
	}
	b.waitForCompletionFn = func(gp *player.GuildPlayer) {
		gp.ClearVoiceConnection()
	}

	done := runPlayLoopAsync(b, p.GuildID)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("playLoop did not finish in time")
	}

	if got := atomic.LoadInt32(&cacheCalls); got != 0 {
		t.Fatalf("cache download calls = %d, want 0", got)
	}
}

func TestPlayLoopCacheHitDoesNotStartBackgroundCache(t *testing.T) {
	cacheStore, manager, p := newPlayLoopTestEnv(t)
	track := &player.Track{
		Title: "track-hit",
		URL:   "https://example.com/hit",
	}
	seedCacheEntry(t, cacheStore, track.URL)
	p.Queue.Add(track)

	var cacheCalls int32

	b := &Bot{
		PlayerManager: manager,
		Cache:         cacheStore,
	}
	b.playTrackFn = func(*player.GuildPlayer) error { return nil }
	b.cacheTrackFn = func(_ string, path string) error {
		atomic.AddInt32(&cacheCalls, 1)
		return os.WriteFile(path, []byte("cached-audio"), 0o644)
	}
	b.waitForCompletionFn = func(gp *player.GuildPlayer) {
		gp.ClearVoiceConnection()
	}

	done := runPlayLoopAsync(b, p.GuildID)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("playLoop did not finish in time")
	}

	if got := atomic.LoadInt32(&cacheCalls); got != 0 {
		t.Fatalf("cache download calls = %d, want 0", got)
	}
}

func TestPlayLoopRetryStartsDeferredCacheOnceAfterSuccessfulAttempt(t *testing.T) {
	cacheStore, manager, p := newPlayLoopTestEnv(t)
	p.Queue.Add(&player.Track{
		Title: "track-retry",
		URL:   "https://example.com/retry",
	})

	var playCalls int32
	var cacheCalls int32
	cacheCalled := make(chan struct{}, 1)

	b := &Bot{
		PlayerManager: manager,
		Cache:         cacheStore,
	}
	b.playTrackFn = func(*player.GuildPlayer) error {
		call := atomic.AddInt32(&playCalls, 1)
		if call == 1 {
			return fmt.Errorf("first attempt failed")
		}
		return nil
	}
	b.waitForPlaybackStartFn = func(*player.GuildPlayer) bool { return true }
	b.cacheTrackFn = func(_ string, path string) error {
		atomic.AddInt32(&cacheCalls, 1)
		select {
		case cacheCalled <- struct{}{}:
		default:
		}
		return os.WriteFile(path, []byte("cached-audio"), 0o644)
	}
	b.waitForCompletionFn = func(gp *player.GuildPlayer) {
		gp.ClearVoiceConnection()
	}

	done := runPlayLoopAsync(b, p.GuildID)

	select {
	case <-cacheCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cache download did not start after successful retry")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("playLoop did not finish in time")
	}

	if got := atomic.LoadInt32(&playCalls); got != 2 {
		t.Fatalf("play attempts = %d, want 2", got)
	}
	if got := atomic.LoadInt32(&cacheCalls); got != 1 {
		t.Fatalf("cache download calls = %d, want 1", got)
	}
}

type stubVoiceConn struct{}

func (stubVoiceConn) SetSpeaking(context.Context, bool) error { return nil }
func (stubVoiceConn) SendOpusFrame([]byte) error              { return nil }
func (stubVoiceConn) Disconnect(context.Context) error        { return nil }

func seedCacheEntry(t *testing.T, c *cache.Cache, url string) {
	t.Helper()
	key := cache.GenerateKey(url)
	_, err := c.GetOrCreate(key, func(path string) error {
		return os.WriteFile(path, []byte("cached-audio"), 0o644)
	})
	if err != nil {
		t.Fatalf("failed to seed cache for %q: %v", url, err)
	}
}

func newPlayLoopTestEnv(t *testing.T) (*cache.Cache, *player.Manager, *player.GuildPlayer) {
	t.Helper()

	cacheStore, err := cache.NewCache(t.TempDir(), 64*1024*1024)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	manager := player.NewManager()
	p := manager.GetPlayer("guild-test")
	p.SetVoiceConnection(stubVoiceConn{})

	return cacheStore, manager, p
}

func runPlayLoopAsync(b *Bot, guildID string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		b.playLoop(guildID, "channel")
		close(done)
	}()
	return done
}
