package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/config"
)

func stubRuntimeTools(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"ffmpeg", "yt-dlp", "deno"} {
		// #nosec G306 G703 -- fixed executable names in a private test directory.
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestNewAppliesMemoryDerivedAdmission(t *testing.T) {
	stubRuntimeTools(t)
	var output bytes.Buffer
	logger := log.NewWithOptions(&output, log.Options{Formatter: log.JSONFormatter})
	cfg := &config.Config{
		DiscordToken:              "offline-test-token",
		CacheDir:                  t.TempDir(),
		CacheLimit:                1 << 20,
		BotActivityType:           "LISTENING",
		BotActivity:               "music",
		DefaultVolume:             100,
		ReduceVolumeOnVoiceTarget: 70,
		MaxPlaylistTracks:         1,
		YTDLPMaxConcurrency:       1,
		MaxGuildSessions:          16,
		PlaybackMemoryLimit:       512 << 20,
		QueueLimit:                1,
		PlayCommandConcurrency:    1,
		HealthListenAddr:          "127.0.0.1:8080",
	}
	application, err := New(context.Background(), cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	application.Close()

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode capacity log %q: %v", output.String(), err)
	}
	if event["event"] != "capacity.admission" || event["memory_source"] != "operator" || event["effective_guild_limit"] != float64(5) {
		t.Fatalf("capacity event = %#v", event)
	}
}

func TestNewRejectsUnfundableMemoryBudget(t *testing.T) {
	stubRuntimeTools(t)
	cfg := &config.Config{PlaybackMemoryLimit: 256 << 20, MaxGuildSessions: 16}
	application, err := New(context.Background(), cfg, log.New(&bytes.Buffer{}))
	if err == nil || application != nil {
		t.Fatal("unfundable memory budget was accepted")
	}
}
