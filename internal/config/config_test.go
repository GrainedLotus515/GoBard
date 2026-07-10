package config

import (
	"os"
	"path/filepath"
	"testing"
)

var configEnvKeys = []string{
	"DISCORD_TOKEN",
	"DISCORD_TOKEN_FILE",
	"CACHE_DIR",
	"CACHE_LIMIT",
	"BOT_STATUS",
	"BOT_ACTIVITY_TYPE",
	"BOT_ACTIVITY",
	"BOT_ACTIVITY_URL",
	"REGISTER_COMMANDS_ON_BOT",
	"DEFAULT_VOLUME",
	"REDUCE_VOL_WHEN_VOICE",
	"REDUCE_VOL_WHEN_VOICE_TARGET",
	"MAX_PLAYLIST_TRACKS",
	"YTDLP_MAX_CONCURRENCY",
	"HEALTH_LISTEN_ADDR",
	"DEBUG",
	"DEBUG_PLAYBACK",
}

func cleanConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range configEnvKeys {
		value, present := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv(%q): %v", key, err)
		}
		t.Cleanup(func() {
			if present {
				_ = os.Setenv(key, value)
				return
			}
			_ = os.Unsetenv(key)
		})
	}
}

func TestLoadRequiresExactlyOneTokenSource(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		cleanConfigEnvironment(t)
		if _, err := Load(); err == nil {
			t.Fatal("Load() error = nil, want missing token error")
		}
	})

	t.Run("both", func(t *testing.T) {
		cleanConfigEnvironment(t)
		t.Setenv("DISCORD_TOKEN", "env-token")
		t.Setenv("DISCORD_TOKEN_FILE", filepath.Join(t.TempDir(), "token"))
		if _, err := Load(); err == nil {
			t.Fatal("Load() error = nil, want ambiguous token source error")
		}
	})
}

func TestLoadReadsTokenFileAndAppliesDefaults(t *testing.T) {
	cleanConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "discord-token")
	if err := os.WriteFile(path, []byte("  file-token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("DISCORD_TOKEN_FILE", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DiscordToken != "file-token" || cfg.DiscordTokenFile != path {
		t.Fatalf("token config = (%q, %q), want file token and path", cfg.DiscordToken, cfg.DiscordTokenFile)
	}
	if cfg.DefaultVolume != 100 || cfg.ReduceVolumeOnVoiceTarget != 70 {
		t.Fatalf("playback defaults = volume %d target %d", cfg.DefaultVolume, cfg.ReduceVolumeOnVoiceTarget)
	}
	if cfg.MaxPlaylistTracks != 500 || cfg.YTDLPMaxConcurrency != 4 {
		t.Fatalf("YouTube defaults = playlist %d concurrency %d", cfg.MaxPlaylistTracks, cfg.YTDLPMaxConcurrency)
	}
	if cfg.HealthListenAddr != "127.0.0.1:8080" {
		t.Fatalf("HealthListenAddr = %q, want loopback default", cfg.HealthListenAddr)
	}
}

func TestLoadRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"bad boolean", "REDUCE_VOL_WHEN_VOICE", "sometimes"},
		{"bad playback debug boolean", "DEBUG_PLAYBACK", "sometimes"},
		{"volume over range", "DEFAULT_VOLUME", "101"},
		{"negative cache limit", "CACHE_LIMIT", "-1GB"},
		{"overflowing cache limit", "CACHE_LIMIT", "999999999999999999999GB"},
		{"bad status", "BOT_STATUS", "away"},
		{"bad activity", "BOT_ACTIVITY_TYPE", "DANCING"},
		{"zero playlist limit", "MAX_PLAYLIST_TRACKS", "0"},
		{"playlist limit above safety ceiling", "MAX_PLAYLIST_TRACKS", "501"},
		{"too many yt-dlp processes", "YTDLP_MAX_CONCURRENCY", "17"},
		{"public health listener", "HEALTH_LISTEN_ADDR", "0.0.0.0:8080"},
		{"invalid health port", "HEALTH_LISTEN_ADDR", "127.0.0.1:0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanConfigEnvironment(t)
			t.Setenv("DISCORD_TOKEN", "token")
			t.Setenv(tt.key, tt.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() error = nil for %s=%q", tt.key, tt.value)
			}
		})
	}
}

func TestLoadAcceptsLoopbackIPv6HealthListener(t *testing.T) {
	cleanConfigEnvironment(t)
	t.Setenv("DISCORD_TOKEN", "token")
	t.Setenv("HEALTH_LISTEN_ADDR", "[::1]:9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HealthListenAddr != "[::1]:9090" {
		t.Fatalf("HealthListenAddr = %q", cfg.HealthListenAddr)
	}
}
