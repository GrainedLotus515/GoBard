package bot

import (
	"context"
	"fmt"
	"os"
	"reflect"
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
