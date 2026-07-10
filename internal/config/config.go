// Package config loads and validates GoBard's process configuration.
package config

import (
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultCacheLimit        = int64(2 * 1024 * 1024 * 1024)
	defaultHealthListenAddr  = "127.0.0.1:8080"
	defaultMaxPlaylistTracks = 500
	defaultYTDLPConcurrency  = 4
	maxTokenFileSize         = 4 * 1024
)

// Config holds all application configuration. Values in this struct have
// already been validated by Load and are safe for consumers to use directly.
type Config struct {
	// Discord configuration. DiscordTokenFile records the source when a file
	// was used; DiscordToken always contains the resolved token.
	DiscordToken     string
	DiscordTokenFile string

	// Cache settings.
	CacheDir   string
	CacheLimit int64 // bytes

	// Bot behavior.
	BotStatus        string
	BotActivityType  string
	BotActivity      string
	BotActivityURL   string
	RegisterGlobally bool

	// Playback settings.
	DefaultVolume             int
	ReduceVolumeOnVoice       bool
	ReduceVolumeOnVoiceTarget int
	MaxPlaylistTracks         int
	YTDLPMaxConcurrency       int

	// Health server configuration.
	HealthListenAddr string

	// Debug settings.
	Debug bool
}

// Load loads configuration from environment variables and rejects invalid
// values. Silent fallbacks make deployment mistakes hard to diagnose, so an
// explicitly supplied value is never ignored.
func Load() (*Config, error) {
	token, tokenFile, err := loadDiscordToken()
	if err != nil {
		return nil, err
	}

	cacheLimit, err := parseByteSize(envOrDefault("CACHE_LIMIT", "2GB"))
	if err != nil {
		return nil, fmt.Errorf("invalid CACHE_LIMIT: %w", err)
	}

	defaultVolume, err := envInt("DEFAULT_VOLUME", 100, 0, 100)
	if err != nil {
		return nil, err
	}
	reduceOnVoice, err := envBool("REDUCE_VOL_WHEN_VOICE")
	if err != nil {
		return nil, err
	}
	reduceTarget, err := envInt("REDUCE_VOL_WHEN_VOICE_TARGET", 70, 0, 100)
	if err != nil {
		return nil, err
	}
	maxPlaylistTracks, err := envInt("MAX_PLAYLIST_TRACKS", defaultMaxPlaylistTracks, 1, defaultMaxPlaylistTracks)
	if err != nil {
		return nil, err
	}
	ytdlpConcurrency, err := envInt("YTDLP_MAX_CONCURRENCY", defaultYTDLPConcurrency, 1, 16)
	if err != nil {
		return nil, err
	}
	registerGlobally, err := envBool("REGISTER_COMMANDS_ON_BOT")
	if err != nil {
		return nil, err
	}
	debug, err := envBool("DEBUG")
	if err != nil {
		return nil, err
	}
	// The player currently reads DEBUG_PLAYBACK directly during its one-time
	// initialization. Validate it here so a malformed deployment value cannot
	// silently change runtime behavior even though it is not stored separately.
	if _, err := envBool("DEBUG_PLAYBACK"); err != nil {
		return nil, err
	}
	status, err := parseStatus(envOrDefault("BOT_STATUS", "online"))
	if err != nil {
		return nil, err
	}
	activityType, err := parseActivityType(envOrDefault("BOT_ACTIVITY_TYPE", "LISTENING"))
	if err != nil {
		return nil, err
	}

	cacheDir := strings.TrimSpace(envOrDefault("CACHE_DIR", "./cache"))
	if cacheDir == "" {
		return nil, fmt.Errorf("CACHE_DIR must not be empty")
	}

	activity := strings.TrimSpace(envOrDefault("BOT_ACTIVITY", "music"))
	if activity == "" {
		return nil, fmt.Errorf("BOT_ACTIVITY must not be empty")
	}

	healthListenAddr, err := parseListenAddr(envOrDefault("HEALTH_LISTEN_ADDR", defaultHealthListenAddr))
	if err != nil {
		return nil, fmt.Errorf("invalid HEALTH_LISTEN_ADDR: %w", err)
	}

	return &Config{
		DiscordToken:              token,
		DiscordTokenFile:          tokenFile,
		CacheDir:                  cacheDir,
		CacheLimit:                cacheLimit,
		BotStatus:                 status,
		BotActivityType:           activityType,
		BotActivity:               activity,
		BotActivityURL:            strings.TrimSpace(os.Getenv("BOT_ACTIVITY_URL")),
		RegisterGlobally:          registerGlobally,
		DefaultVolume:             defaultVolume,
		ReduceVolumeOnVoice:       reduceOnVoice,
		ReduceVolumeOnVoiceTarget: reduceTarget,
		MaxPlaylistTracks:         maxPlaylistTracks,
		YTDLPMaxConcurrency:       ytdlpConcurrency,
		HealthListenAddr:          healthListenAddr,
		Debug:                     debug,
	}, nil
}

func loadDiscordToken() (token string, tokenFile string, err error) {
	envToken, hasEnvToken := os.LookupEnv("DISCORD_TOKEN")
	envToken = strings.TrimSpace(envToken)
	hasEnvToken = hasEnvToken && envToken != ""

	file, hasTokenFile := os.LookupEnv("DISCORD_TOKEN_FILE")
	file = strings.TrimSpace(file)
	hasTokenFile = hasTokenFile && file != ""

	switch {
	case hasEnvToken && hasTokenFile:
		return "", "", fmt.Errorf("set exactly one of DISCORD_TOKEN or DISCORD_TOKEN_FILE")
	case hasEnvToken:
		return envToken, "", nil
	case hasTokenFile:
		cleanPath := filepath.Clean(file)
		info, statErr := os.Stat(cleanPath)
		if statErr != nil {
			return "", "", fmt.Errorf("read DISCORD_TOKEN_FILE: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("DISCORD_TOKEN_FILE must point to a regular file")
		}
		if info.Size() > maxTokenFileSize {
			return "", "", fmt.Errorf("DISCORD_TOKEN_FILE is unexpectedly large")
		}
		contents, readErr := os.ReadFile(cleanPath)
		if readErr != nil {
			return "", "", fmt.Errorf("read DISCORD_TOKEN_FILE: %w", readErr)
		}
		resolved := strings.TrimSpace(string(contents))
		if resolved == "" {
			return "", "", fmt.Errorf("DISCORD_TOKEN_FILE is empty")
		}
		if strings.ContainsRune(resolved, '\x00') {
			return "", "", fmt.Errorf("DISCORD_TOKEN_FILE contains an invalid token")
		}
		return resolved, cleanPath, nil
	default:
		return "", "", fmt.Errorf("exactly one of DISCORD_TOKEN or DISCORD_TOKEN_FILE is required")
	}
}

func envOrDefault(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}

func envBool(key string) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return false, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("invalid %s: expected boolean: %w", key, err)
	}
	return parsed, nil
}

func envInt(key string, defaultValue, min, max int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid %s: expected integer: %w", key, err)
	}
	if parsed < min || parsed > max {
		return 0, fmt.Errorf("invalid %s: must be between %d and %d", key, min, max)
	}
	return parsed, nil
}

func parseStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch status {
	case "online", "idle", "dnd", "invisible", "offline":
		return status, nil
	default:
		return "", fmt.Errorf("invalid BOT_STATUS %q: expected online, idle, dnd, invisible, or offline", raw)
	}
}

func parseActivityType(raw string) (string, error) {
	activityType := strings.ToUpper(strings.TrimSpace(raw))
	switch activityType {
	case "PLAYING", "STREAMING", "LISTENING", "WATCHING", "COMPETING":
		return activityType, nil
	default:
		return "", fmt.Errorf("invalid BOT_ACTIVITY_TYPE %q: expected PLAYING, STREAMING, LISTENING, WATCHING, or COMPETING", raw)
	}
}

func parseListenAddr(raw string) (string, error) {
	addr := strings.TrimSpace(raw)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("host is required")
	}
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", fmt.Errorf("host must be a loopback address")
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("port must be between 1 and 65535")
	}
	return addr, nil
}

// parseByteSize accepts positive base-2 KB, MB, and GB values. It deliberately
// rejects unknown units and overflow instead of silently falling back.
func parseByteSize(raw string) (int64, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	if value == "" {
		return 0, fmt.Errorf("value is empty")
	}

	multiplier := int64(1)
	number := value
	for suffix, factor := range map[string]int64{
		"GB": 1024 * 1024 * 1024,
		"MB": 1024 * 1024,
		"KB": 1024,
	} {
		if strings.HasSuffix(value, suffix) {
			multiplier = factor
			number = strings.TrimSpace(strings.TrimSuffix(value, suffix))
			break
		}
	}

	parsed, err := strconv.ParseInt(number, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("expected a positive integer with optional KB, MB, or GB suffix")
	}
	if parsed > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("value overflows bytes")
	}
	return parsed * multiplier, nil
}
