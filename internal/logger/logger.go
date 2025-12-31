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
