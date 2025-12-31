package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all application configuration
type Config struct {
	// Discord configuration
	DiscordToken string
	ClientID     string

	// API Keys
	YouTubeAPIKey   string
	SpotifyClientID string
	SpotifySecret   string

	// Cache settings
	CacheDir   string
	CacheLimit int64 // in bytes

	// Bot behavior
	BotStatus           string
	BotActivityType     string
	BotActivity         string
	BotActivityURL      string
	RegisterGlobally    bool
	WaitAfterQueueEmpty time.Duration

	// Features
	EnableSponsorBlock  bool
	SponsorBlockTimeout int

	// Playback settings
	DefaultVolume             int
	ReduceVolumeOnVoice       bool
	ReduceVolumeOnVoiceTarget int

	// Debug settings
	Debug bool
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		// Required
		DiscordToken: os.Getenv("DISCORD_TOKEN"),

		// Optional APIs
		YouTubeAPIKey:   os.Getenv("YOUTUBE_API_KEY"),
		SpotifyClientID: os.Getenv("SPOTIFY_CLIENT_ID"),
		SpotifySecret:   os.Getenv("SPOTIFY_CLIENT_SECRET"),

		// Cache defaults
		CacheDir:   getEnvOrDefault("CACHE_DIR", "./cache"),
		CacheLimit: parseCacheLimit(getEnvOrDefault("CACHE_LIMIT", "2GB")),

		// Bot settings
		BotStatus:           getEnvOrDefault("BOT_STATUS", "online"),
		BotActivityType:     getEnvOrDefault("BOT_ACTIVITY_TYPE", "LISTENING"),
		BotActivity:         getEnvOrDefault("BOT_ACTIVITY", "music"),
		BotActivityURL:      os.Getenv("BOT_ACTIVITY_URL"),
		RegisterGlobally:    getEnvBool("REGISTER_COMMANDS_ON_BOT", false),
		WaitAfterQueueEmpty: time.Duration(getEnvInt("WAIT_AFTER_QUEUE_EMPTIES", 30)) * time.Second,

		// Features
		EnableSponsorBlock:  getEnvBool("ENABLE_SPONSORBLOCK", false),
		SponsorBlockTimeout: getEnvInt("SPONSORBLOCK_TIMEOUT", 5),

		// Playback
		DefaultVolume:             getEnvInt("DEFAULT_VOLUME", 100),
		ReduceVolumeOnVoice:       getEnvBool("REDUCE_VOL_WHEN_VOICE", false),
		ReduceVolumeOnVoiceTarget: getEnvInt("REDUCE_VOL_WHEN_VOICE_TARGET", 70),

		// Debug
		Debug: getEnvBool("DEBUG", false),
	}

	if cfg.DiscordToken == "" {
		return nil, fmt.Errorf("DISCORD_TOKEN environment variable is required")
	}

	return cfg, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		b, err := strconv.ParseBool(value)
		if err != nil {
			return defaultValue
		}
		return b
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		i, err := strconv.Atoi(value)
		if err != nil {
			return defaultValue
		}
		return i
	}
	return defaultValue
}

func parseCacheLimit(limit string) int64 {
	if limit == "" {
		return 2 * 1024 * 1024 * 1024 // 2GB default
	}

	multiplier := int64(1)
	numStr := limit

	// Parse unit suffix
	if len(limit) >= 2 {
		suffix := limit[len(limit)-2:]
		if suffix == "GB" || suffix == "gb" {
			multiplier = 1024 * 1024 * 1024
			numStr = limit[:len(limit)-2]
		} else if suffix == "MB" || suffix == "mb" {
			multiplier = 1024 * 1024
			numStr = limit[:len(limit)-2]
		} else if suffix == "KB" || suffix == "kb" {
			multiplier = 1024
			numStr = limit[:len(limit)-2]
		}
	}

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 2 * 1024 * 1024 * 1024 // 2GB default on error
	}

	return num * multiplier
}
