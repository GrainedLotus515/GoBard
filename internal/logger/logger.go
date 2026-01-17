package logger

import (
	"os"

	"github.com/charmbracelet/log"
)

var Logger *log.Logger

// debugMode controls whether timing/debug logs are shown
var debugMode bool

func init() {
	Logger = log.New(os.Stderr)
	Logger.SetReportCaller(false)
	Logger.SetReportTimestamp(true)

	// Default to Info level, DEBUG env var will override via SetDebugMode
	Logger.SetLevel(log.InfoLevel)
}

// SetDebugMode enables or disables debug logging
func SetDebugMode(enabled bool) {
	debugMode = enabled
	if enabled {
		Logger.SetLevel(log.DebugLevel)
		Logger.Info("Debug mode enabled")
	} else {
		Logger.SetLevel(log.InfoLevel)
	}
}

// IsDebugMode returns whether debug mode is enabled
func IsDebugMode() bool {
	return debugMode
}

// Timing logs timing information (only shown when DEBUG=true)
func Timing(msg string, keyvals ...any) {
	if debugMode {
		Logger.Debug("⏱️ "+msg, keyvals...)
	}
}

// Playback logging functions
func PlaybackStart(title string) {
	Logger.Info("▶️  Starting playback", "title", title)
}

func PlaybackDownloading(title string) {
	Logger.Info("⬇️  Downloading/caching track", "title", title)
}

func PlaybackCached(path string) {
	Logger.Info("💾 Track cached", "path", path)
}

func PlaybackEncodingStart(source string) {
	Logger.Info("🔄 Starting encoding", "source", source)
}

func PlaybackEncodingSuccess() {
	Logger.Info("✅ Encoder created successfully")
}

func PlaybackEncodingError(err error) {
	Logger.Error("❌ Encoder error", "err", err)
}

func PlaybackVoiceWaiting() {
	Logger.Debug("⏳ Waiting for voice connection to stabilize")
}

func PlaybackSpeakingStart() {
	Logger.Debug("🎤 Setting speaking state")
}

func PlaybackSpeakingError(err error) {
	Logger.Warn("⚠️  Failed to set speaking state", "err", err)
}

func PlaybackFrameStart() {
	Logger.Debug("📡 Starting frame streaming")
}

func PlaybackFrameError(err error) {
	Logger.Error("❌ Frame read error", "err", err)
}

func PlaybackFramesMilestone(count int) {
	Logger.Info("📊 Frames sent", "count", count)
}

func PlaybackFramesComplete(count int) {
	Logger.Info("✨ Playback complete", "frames", count)
}

func PlaybackSpeakingStop() {
	Logger.Debug("🔇 Clearing speaking state")
}

func PlaybackQueueEmpty() {
	Logger.Debug("⏸️  Queue empty, waiting before disconnect")
}

func PlaybackStopped(count int) {
	Logger.Warn("⏹️  Playback stopped", "frames_sent", count)
}

// Voice connection logging
func VoiceConnecting(channel string) {
	Logger.Info("🔗 Connecting to voice channel", "channel", channel)
}

func VoiceConnected(channel string) {
	Logger.Info("✅ Connected to voice channel", "channel", channel)
}

func VoiceConnectionError(err error) {
	Logger.Error("❌ Voice connection failed", "err", err)
}

func VoiceDisconnecting() {
	Logger.Info("🔌 Disconnecting from voice")
}

func VoiceDisconnected() {
	Logger.Info("✅ Disconnected from voice")
}

// Verbose voice connection logging (for debugging voice issues)
func VoiceJoinAttempt(guildID, channelID string) {
	Logger.Debug("🔊 Attempting to join voice channel", "guild", guildID, "channel", channelID)
}

func VoiceJoinSuccess(guildID, channelID string) {
	Logger.Info("🔊 Successfully joined voice channel", "guild", guildID, "channel", channelID)
}

func VoiceJoinRetry(attempt int, maxAttempts int, err error) {
	Logger.Warn("🔊 Voice join failed, retrying", "attempt", attempt, "max_attempts", maxAttempts, "err", err)
}

func VoiceReady(ready bool) {
	Logger.Debug("🔊 Voice connection ready state", "ready", ready)
}

func VoiceOpusSend(success bool, frameCount int) {
	if !success {
		Logger.Warn("🔊 Failed to send opus frame", "frame", frameCount)
	}
}

func VoiceStateChange(state string, details ...any) {
	Logger.Debug("🔊 Voice state changed", append([]any{"state", state}, details...)...)
}

func VoiceError(operation string, err error, details ...any) {
	Logger.Error("🔊 Voice error", append([]any{"operation", operation, "err", err}, details...)...)
}

func VoiceCloseCode(code int) {
	// Document known close codes for easier debugging
	var codeDesc string
	switch code {
	case 4001:
		codeDesc = "Unknown opcode"
	case 4002:
		codeDesc = "Failed to decode payload"
	case 4003:
		codeDesc = "Not authenticated"
	case 4004:
		codeDesc = "Authentication failed"
	case 4005:
		codeDesc = "Already authenticated"
	case 4006:
		codeDesc = "Session no longer valid"
	case 4009:
		codeDesc = "Session timeout"
	case 4011:
		codeDesc = "Server not found"
	case 4012:
		codeDesc = "Unknown protocol"
	case 4014:
		codeDesc = "Disconnected"
	case 4015:
		codeDesc = "Voice server crashed"
	case 4016:
		codeDesc = "Unknown encryption mode"
	case 4021:
		codeDesc = "Voice server crashed (new)"
	case 4022:
		codeDesc = "Unknown session"
	default:
		codeDesc = "Unknown"
	}
	Logger.Warn("🔊 Voice connection closed", "close_code", code, "description", codeDesc)
}

func VoiceWaitingForReady(timeout string) {
	Logger.Debug("🔊 Waiting for voice connection to be ready", "timeout", timeout)
}

func VoiceReadySuccess(elapsed string) {
	Logger.Info("🔊 Voice connection is ready", "elapsed", elapsed)
}

func VoiceReadyTimeout() {
	Logger.Warn("🔊 Voice connection ready timeout - proceeding anyway")
}

// Command logging
func CommandExecuting(name string, user string) {
	Logger.Info("⚙️  Executing command", "cmd", name, "user", user)
}

func CommandSuccess(name string) {
	Logger.Info("✅ Command succeeded", "cmd", name)
}

func CommandError(name string, err error) {
	Logger.Error("❌ Command error", "cmd", name, "err", err)
}

// Download logging
func DownloadStart(url string) {
	Logger.Info("⬇️  Starting download", "url", url)
}

func DownloadProgress(url string, size string) {
	Logger.Debug("📥 Downloading", "url", url, "size", size)
}

func DownloadComplete(path string) {
	Logger.Info("✅ Download complete", "path", path)
}

func DownloadError(url string, err error) {
	Logger.Error("❌ Download failed", "url", url, "err", err)
}

// Spotify logging
func SpotifySearching(query string) {
	Logger.Info("🔍 Searching Spotify", "query", query)
}

func SpotifyFound(title string, artists string) {
	Logger.Info("✅ Found on Spotify", "title", title, "artists", artists)
}

func SpotifyError(err error) {
	Logger.Error("❌ Spotify error", "err", err)
}

// YouTube logging
func YouTubeSearching(query string) {
	Logger.Info("🔍 Searching YouTube", "query", query)
}

func YouTubeFound(title string, duration string) {
	Logger.Info("✅ Found on YouTube", "title", title, "duration", duration)
}

func YouTubeError(err error) {
	Logger.Error("❌ YouTube error", "err", err)
}

// General logging
func Info(msg string, keyvals ...any) {
	Logger.Info(msg, keyvals...)
}

func Debug(msg string, keyvals ...any) {
	Logger.Debug(msg, keyvals...)
}

func Warn(msg string, keyvals ...any) {
	Logger.Warn(msg, keyvals...)
}

func Error(msg string, keyvals ...any) {
	Logger.Error(msg, keyvals...)
}

func Fatal(msg string, keyvals ...any) {
	Logger.Fatal(msg, keyvals...)
}
