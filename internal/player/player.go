package player

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/voiceconn"
)

const opusFrameInterval = 20 * time.Millisecond

// encodedFrameBufferCapacity intentionally stays small. Volume is applied
// before Opus encoding, so a large queue of already-encoded frames delays
// volume and ducking changes by the amount of audio already buffered. Five
// frames provide a small scheduling cushion while limiting that delay to
// roughly 100ms.
const encodedFrameBufferCapacity = 5

// silenceFrame is the standard Opus silence indicator frame recommended by
// Discord before stopping transmission to prevent decoder interpolation
// artifacts on the next playback start.
var silenceFrame = []byte{0xF8, 0xFF, 0xFE}

var (
	newCustomEncoder = func(inputPath string, sampleRate, channels int, startOffset time.Duration, vol *atomic.Int32) (EncoderInterface, error) {
		return NewCustomEncoder(inputPath, sampleRate, channels, startOffset, vol)
	}
	newStreamingEncoder = func(url, streamURL string, streamHeaders map[string]string, sampleRate, channels int, startOffset time.Duration, vol *atomic.Int32) (EncoderInterface, error) {
		return NewStreamingEncoder(url, streamURL, streamHeaders, sampleRate, channels, startOffset, vol)
	}
	nowOpusFrame    = time.Now
	sleepOpusFrame  = time.Sleep
	sleepVoiceReady = func() {
		time.Sleep(200 * time.Millisecond)
	}
	debugPlaybackOnce    sync.Once
	debugPlaybackEnabled bool
)

// EncoderInterface defines the interface for audio encoders
type EncoderInterface interface {
	OpusFrame() ([]byte, error)
	Cleanup() error
}

type bufferLevelReporter interface {
	BufferLevel() (int, int)
}

// PlaybackEndReason describes why a playback session finished. The bot owns
// queue transitions, but it needs this information to distinguish a natural
// completion from a user action, a source failure, or a lost voice transport.
type PlaybackEndReason string

const (
	PlaybackEndNone             PlaybackEndReason = ""
	PlaybackEndCompleted        PlaybackEndReason = "completed"
	PlaybackEndStopped          PlaybackEndReason = "stopped"
	PlaybackEndSkipped          PlaybackEndReason = "skipped"
	PlaybackEndSeeked           PlaybackEndReason = "seeked"
	PlaybackEndDisconnected     PlaybackEndReason = "disconnected"
	PlaybackEndTransportFailure PlaybackEndReason = "transport_failure"
	PlaybackEndSourceFailure    PlaybackEndReason = "source_failure"
)

// PlaybackResult is the terminal outcome of a playback session. Err is set
// only for failures; intentional control actions have a non-failure reason.
// Track is the immutable session target rather than a later queue selection.
type PlaybackResult struct {
	Reason   PlaybackEndReason
	Err      error
	Track    *Track
	Started  bool
	Position time.Duration
}

// Failed reports whether the session ended because its source or voice
// transport failed.
func (r PlaybackResult) Failed() bool {
	return r.Reason == PlaybackEndSourceFailure || r.Reason == PlaybackEndTransportFailure
}

func isDebugPlaybackEnabled() bool {
	debugPlaybackOnce.Do(func() {
		enabled, err := strconv.ParseBool(os.Getenv("DEBUG_PLAYBACK"))
		if err == nil {
			debugPlaybackEnabled = enabled
		}
	})
	return debugPlaybackEnabled
}

type playbackSession struct {
	stop    chan struct{}
	started chan struct{}
	done    chan struct{}

	stopOnce    sync.Once
	startedOnce sync.Once
	doneOnce    sync.Once

	mu              sync.Mutex
	encoder         EncoderInterface
	startedPlayback bool
	result          PlaybackResult
	resultSet       bool
}

func newPlaybackSession() *playbackSession {
	return &playbackSession{
		stop:    make(chan struct{}),
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (s *playbackSession) stopPlayback(reason PlaybackEndReason) {
	s.setResultIfUnset(PlaybackResult{Reason: reason})
	s.stopOnce.Do(func() {
		close(s.stop)
	})
}

func (s *playbackSession) signalStarted() {
	s.mu.Lock()
	s.startedPlayback = true
	s.mu.Unlock()
	s.startedOnce.Do(func() {
		close(s.started)
	})
}

func (s *playbackSession) signalDone() {
	s.doneOnce.Do(func() {
		close(s.done)
	})
}

func (s *playbackSession) isStopped() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func (s *playbackSession) setResultIfUnset(result PlaybackResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resultSet {
		return
	}
	result.Started = result.Started || s.startedPlayback
	s.result = result
	s.resultSet = true
}

func (s *playbackSession) resultSnapshot() PlaybackResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.result
	result.Started = result.Started || s.startedPlayback
	return result
}

// completeResult fills terminal metadata without replacing a reason that was
// already selected by an intentional control action.
func (s *playbackSession) completeResult(result PlaybackResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.resultSet {
		result.Started = result.Started || s.startedPlayback
		s.result = result
		s.resultSet = true
		return
	}
	if s.result.Track == nil {
		s.result.Track = result.Track
	}
	s.result.Position = result.Position
	s.result.Started = s.result.Started || result.Started || s.startedPlayback
}

func (s *playbackSession) bindEncoder(encoder EncoderInterface) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isStopped() {
		return false
	}

	s.encoder = encoder
	return true
}

func (s *playbackSession) cleanupEncoder() {
	s.mu.Lock()
	encoder := s.encoder
	s.encoder = nil
	s.mu.Unlock()

	if encoder != nil {
		if err := encoder.Cleanup(); err != nil {
			logger.Debug("Encoder cleanup failed", "err", err)
		}
	}
}

func (s *playbackSession) stopAndCleanup(reason PlaybackEndReason) {
	s.stopPlayback(reason)
	s.cleanupEncoder()
}

// GuildPlayer manages playback for a single guild
type GuildPlayer struct {
	GuildID         string
	Queue           *Queue
	VoiceConnection voiceconn.Connection
	voiceReadyWait  bool

	// Playback state
	Playing         bool
	Paused          bool
	LoopRunning     bool // Track if playLoop goroutine is running
	CurrentPosition time.Duration
	Volume          int

	// Voice reduction
	ReduceOnVoice       bool
	ReduceOnVoiceTarget int
	OriginalVolume      int

	volumeAtomic   atomic.Int32
	activeSpeakers map[string]struct{}

	seekRequested        bool
	skipRequested        bool
	requestedStartOffset time.Duration
	playbackStartedAt    time.Time
	activePlayback       *playbackSession
	lastPlayback         *playbackSession

	mu sync.RWMutex
}

// Manager manages all guild players
type Manager struct {
	players  map[string]*GuildPlayer
	defaults PlaybackDefaults
	mu       sync.RWMutex
}

// PlaybackDefaults are applied to players created after the defaults are set.
// Existing players intentionally retain their runtime guild settings.
type PlaybackDefaults struct {
	Volume              int
	ReduceOnVoice       bool
	ReduceOnVoiceTarget int
}

// NewManager creates a new player manager
func NewManager() *Manager {
	return NewManagerWithDefaults(PlaybackDefaults{
		Volume:              100,
		ReduceOnVoiceTarget: 70,
	})
}

// NewManagerWithDefaults creates a manager whose newly-created players use
// the supplied validated playback defaults.
func NewManagerWithDefaults(defaults PlaybackDefaults) *Manager {
	return &Manager{
		players:  make(map[string]*GuildPlayer),
		defaults: normalizePlaybackDefaults(defaults),
	}
}

// SetPlaybackDefaults updates defaults for future guild players. It does not
// overwrite live per-guild runtime configuration.
func (m *Manager) SetPlaybackDefaults(defaults PlaybackDefaults) {
	m.mu.Lock()
	m.defaults = normalizePlaybackDefaults(defaults)
	m.mu.Unlock()
}

// PlaybackDefaults returns a copy of the defaults used for future players.
func (m *Manager) PlaybackDefaults() PlaybackDefaults {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaults
}

func normalizePlaybackDefaults(defaults PlaybackDefaults) PlaybackDefaults {
	if defaults.Volume < 0 || defaults.Volume > 100 {
		defaults.Volume = 100
	}
	if defaults.ReduceOnVoiceTarget < 0 || defaults.ReduceOnVoiceTarget > 100 {
		defaults.ReduceOnVoiceTarget = 70
	}
	return defaults
}

// GetPlayer gets or creates a player for a guild
func (m *Manager) GetPlayer(guildID string) *GuildPlayer {
	m.mu.Lock()
	defer m.mu.Unlock()

	if player, exists := m.players[guildID]; exists {
		return player
	}

	defaults := m.defaults
	player := &GuildPlayer{
		GuildID:             guildID,
		Queue:               NewQueue(),
		Volume:              defaults.Volume,
		ReduceOnVoice:       defaults.ReduceOnVoice,
		ReduceOnVoiceTarget: defaults.ReduceOnVoiceTarget,
		activeSpeakers:      make(map[string]struct{}),
	}
	player.volumeAtomic.Store(volumeToInt32(defaults.Volume))

	m.players[guildID] = player
	return player
}

// RemovePlayer removes a player for a guild
func (m *Manager) RemovePlayer(guildID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if player, exists := m.players[guildID]; exists {
		player.Stop()
		delete(m.players, guildID)
	}
}

// StopAll terminates active playback sessions without depending on network
// disconnects. It is used during process shutdown so encoders and any
// playback-bound background work observe their completion signals promptly.
// Voice transports are subsequently closed by the Discord voice manager.
func (m *Manager) StopAll() {
	if m == nil {
		return
	}

	m.mu.RLock()
	players := make([]*GuildPlayer, 0, len(m.players))
	for _, guildPlayer := range m.players {
		players = append(players, guildPlayer)
	}
	m.mu.RUnlock()

	for _, guildPlayer := range players {
		if guildPlayer == nil {
			continue
		}
		guildPlayer.Stop()
		guildPlayer.ClearVoiceConnection()
		guildPlayer.Queue.ClearAll()
		guildPlayer.SetLoopRunning(false)
	}
}

// Play starts playing the current track
func (p *GuildPlayer) Play() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.VoiceConnection == nil {
		return fmt.Errorf("not connected to voice channel")
	}

	if p.Paused {
		p.Paused = false
		p.Playing = true
		p.playbackStartedAt = time.Now()
		p.volumeAtomic.Store(volumeToInt32(p.effectiveVolumeLocked()))
		return nil
	}

	if p.activePlayback != nil {
		return fmt.Errorf("playback session already active")
	}

	track := p.Queue.Current()
	if track == nil {
		track = p.Queue.Next()
		if track == nil {
			return fmt.Errorf("no tracks in queue")
		}
	}

	p.Playing = true
	p.Paused = false
	startOffset := p.requestedStartOffset
	p.requestedStartOffset = 0
	p.CurrentPosition = startOffset
	p.playbackStartedAt = time.Now()
	p.volumeAtomic.Store(volumeToInt32(p.effectiveVolumeLocked()))

	session := newPlaybackSession()
	p.activePlayback = session
	p.lastPlayback = session

	vol := &p.volumeAtomic
	go p.playTrack(session, track, startOffset, vol)

	return nil
}

// playTrack handles the actual playback of a track.
//
//nolint:gocyclo,nestif // Playback owns the explicit state transitions needed for source and transport recovery.
func (p *GuildPlayer) playTrack(session *playbackSession, track *Track, startOffset time.Duration, vol *atomic.Int32) {
	logger.PlaybackStart(track.Title)
	debugPlayback := isDebugPlaybackEnabled()
	startupTrace := logger.ResumeTrace(track.RequestTraceID, "track_startup", track.RequestedAt)
	firstFrameSentLogged := false

	var (
		vc                   voiceconn.Connection
		encoder              EncoderInterface
		bufferReporter       bufferLevelReporter
		encoderBound         bool
		speaking             bool
		frameCount           int
		playbackPath         string
		prefetchedStreamUsed bool
		endReason            = PlaybackEndCompleted
		endErr               error
	)

	logPlaybackPath := func(path string) {
		logger.Info("Playback path selected", "path", path, "title", track.Title, "trace_id", track.RequestTraceID)
		if startupTrace != nil {
			startupTrace.Step("Playback path selected", "path", path, "title", track.Title)
		}
	}

	defer func() {
		// Do not try to write trailing silence after a transport loss. Besides
		// being pointless, it can delay recovery by another 100ms.
		result := session.resultSnapshot()
		if speaking && vc != nil && result.Reason != PlaybackEndTransportFailure && p.IsVoiceConnected() {
			for i := 0; i < 5; i++ {
				if err := vc.SendOpusFrame(silenceFrame); err != nil {
					logger.Debug("Unable to send trailing silence frame", "err", err)
					break
				}
				sleepOpusFrame(opusFrameInterval)
			}
			logger.PlaybackSpeakingStop()
			if err := vc.SetSpeaking(context.Background(), false); err != nil {
				logger.Debug("Unable to clear voice speaking state", "err", err)
			}
		}

		if encoderBound {
			session.cleanupEncoder()
		} else if encoder != nil {
			if err := encoder.Cleanup(); err != nil {
				logger.Debug("Encoder cleanup failed", "err", err)
			}
		}

		var finalPosition time.Duration
		p.mu.Lock()
		if p.activePlayback == session {
			finalPosition = p.currentPositionLocked()
			p.CurrentPosition = finalPosition
			p.Playing = false
			p.Paused = false
			p.playbackStartedAt = time.Time{}
			p.activePlayback = nil
		} else {
			finalPosition = p.currentPositionLocked()
		}
		p.mu.Unlock()

		session.completeResult(PlaybackResult{
			Reason:   endReason,
			Err:      endErr,
			Track:    track,
			Position: finalPosition,
		})
		session.signalDone()
	}()

	if startupTrace != nil {
		startupTrace.Step(
			"Player playback goroutine started",
			"title", track.Title,
			"start_offset_ms", startOffset.Milliseconds(),
			"has_local_path", track.LocalPath != "",
		)
	}

	if session.isStopped() {
		logger.PlaybackStopped(frameCount)
		return
	}

	p.mu.Lock()
	if p.VoiceConnection == nil {
		endReason = PlaybackEndTransportFailure
		endErr = fmt.Errorf("not connected to voice channel")
		logger.Error("No voice connection available", "err", endErr)
		p.mu.Unlock()
		return
	}
	vc = p.VoiceConnection
	p.mu.Unlock()

	if session.isStopped() {
		logger.PlaybackStopped(frameCount)
		return
	}

	var err error
	if track.LocalPath != "" {
		playbackPath = "cached_file"
		logger.Info("Using cached file", "path", track.LocalPath)
		logger.PlaybackEncodingStart(track.LocalPath)
		if startupTrace != nil {
			startupTrace.Step("Creating file encoder", "source", track.LocalPath)
		}
		encoder, err = newCustomEncoder(track.LocalPath, 48000, 2, startOffset, vol)
	} else {
		playbackPath = "ytdlp_fallback"
		logger.Info("Streaming from YouTube source", "video_id", track.ID)
		logger.PlaybackEncodingStart("youtube:" + track.ID)
		streamURL := ""
		var streamHeaders map[string]string
		if track.CanUsePrefetchedStream(time.Now(), startOffset) {
			playbackPath = "prefetched_direct"
			streamURL = track.StreamURL
			streamHeaders = track.StreamHeaders
			prefetchedStreamUsed = true
			logger.Info("Using prefetched stream URL", "title", track.Title, "expires_at", track.StreamExpiresAt)
			if startupTrace != nil {
				startupTrace.Step("Creating direct stream encoder", "expires_at", track.StreamExpiresAt)
			}
		} else if track.StreamURL != "" {
			logger.Info("Prefetched stream URL unavailable, resolving live at playback", "title", track.Title, "expires_at", track.StreamExpiresAt)
		}
		if startupTrace != nil && !prefetchedStreamUsed {
			startupTrace.Step("Creating yt-dlp fallback stream encoder", "video_id", track.ID)
		}
		encoder, err = newStreamingEncoder(track.URL, streamURL, streamHeaders, 48000, 2, startOffset, vol)
	}

	if err != nil {
		endReason = PlaybackEndSourceFailure
		endErr = err
		logger.PlaybackEncodingError(err)
		if startupTrace != nil {
			startupTrace.Finish("Encoder creation failed", "err", err)
		}
		return
	}

	if session.isStopped() {
		logger.PlaybackStopped(frameCount)
		return
	}

	if !session.bindEncoder(encoder) {
		logger.PlaybackStopped(frameCount)
		return
	}
	encoderBound = true
	if debugPlayback {
		if reporter, ok := encoder.(bufferLevelReporter); ok {
			bufferReporter = reporter
		}
	}
	logger.PlaybackEncodingSuccess()
	if startupTrace != nil {
		startupTrace.Step("Encoder created", "prefetched_stream_used", prefetchedStreamUsed)
	}

	firstFrame, err := encoder.OpusFrame()
	if err != nil && prefetchedStreamUsed && !session.isStopped() {
		logger.Warn("Prefetched stream failed before first frame, retrying with yt-dlp", "title", track.Title, "err", err)
		if startupTrace != nil {
			startupTrace.Step("Prefetched stream failed before first frame; retrying with yt-dlp", "err", err)
		}
		session.cleanupEncoder()
		encoderBound = false
		track.ClearPrefetchedStream()
		playbackPath = "ytdlp_fallback"

		encoder, err = newStreamingEncoder(track.URL, "", nil, 48000, 2, startOffset, vol)
		if err != nil {
			endReason = PlaybackEndSourceFailure
			endErr = err
			logger.PlaybackEncodingError(err)
			if startupTrace != nil {
				startupTrace.Finish("Fallback encoder creation failed", "err", err)
			}
			return
		}

		if session.isStopped() {
			if cleanupErr := encoder.Cleanup(); cleanupErr != nil {
				logger.Debug("Encoder cleanup failed", "err", cleanupErr)
			}
			logger.PlaybackStopped(frameCount)
			return
		}

		if !session.bindEncoder(encoder) {
			if cleanupErr := encoder.Cleanup(); cleanupErr != nil {
				logger.Debug("Encoder cleanup failed", "err", cleanupErr)
			}
			logger.PlaybackStopped(frameCount)
			return
		}
		encoderBound = true
		if debugPlayback {
			if reporter, ok := encoder.(bufferLevelReporter); ok {
				bufferReporter = reporter
			} else {
				bufferReporter = nil
			}
		}
		logger.PlaybackEncodingSuccess()
		if startupTrace != nil {
			startupTrace.Step("Fallback encoder created")
		}
		firstFrame, err = encoder.OpusFrame()
	}
	if err != nil {
		endReason = PlaybackEndSourceFailure
		endErr = fmt.Errorf("failed to obtain first opus frame: %w", err)
		if err != io.EOF {
			logger.PlaybackFrameError(err)
		} else {
			logger.PlaybackFramesComplete(frameCount)
		}
		if startupTrace != nil {
			startupTrace.Finish("Failed to obtain first opus frame", "err", err)
		}
		return
	}
	if startupTrace != nil {
		startupTrace.Step("First opus frame ready")
	}
	logPlaybackPath(playbackPath)

	if p.consumeVoiceReadyWait() {
		logger.PlaybackVoiceWaiting()
		sleepVoiceReady()
		if startupTrace != nil {
			startupTrace.Step("Voice ready wait completed")
		}
	} else if startupTrace != nil {
		startupTrace.Step("Voice ready wait skipped", "reason", "existing_voice_connection")
	}

	if session.isStopped() {
		logger.PlaybackStopped(frameCount)
		return
	}

	logger.PlaybackSpeakingStart()
	if err := vc.SetSpeaking(context.Background(), true); err != nil {
		logger.PlaybackSpeakingError(err)
		endReason = PlaybackEndTransportFailure
		endErr = fmt.Errorf("failed to enable voice speaking: %w", err)
		p.markVoiceConnectionDead()
		return
	}
	speaking = true
	if startupTrace != nil {
		startupTrace.Step("Speaking state enabled")
	}

	logger.PlaybackFrameStart()
	// Start pacing just before the first network send. Encoder startup and the
	// one-time voice-ready wait must not make the first frame deadline stale.
	nextFrameDeadline := nowOpusFrame()

	for {
		p.mu.RLock()
		paused := p.Paused
		p.mu.RUnlock()

		if paused {
			select {
			case <-session.stop:
				logger.PlaybackStopped(frameCount)
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}

		if frameCount > 0 && frameCount%100 == 0 {
			p.mu.RLock()
			vcValid := p.VoiceConnection != nil
			p.mu.RUnlock()
			if !vcValid {
				logger.Error("Voice connection lost during playback")
				endReason = PlaybackEndTransportFailure
				endErr = fmt.Errorf("voice connection lost during playback")
				return
			}
		}

		if session.isStopped() {
			logger.PlaybackStopped(frameCount)
			return
		}

		frame := firstFrame
		if frameCount > 0 {
			frame, err = encoder.OpusFrame()
			if err != nil {
				if err != io.EOF {
					logger.PlaybackFrameError(err)
					endReason = PlaybackEndSourceFailure
					endErr = err
				} else {
					logger.PlaybackFramesComplete(frameCount)
				}
				break
			}
		}

		if session.isStopped() {
			logger.PlaybackStopped(frameCount)
			return
		}

		if debugPlayback {
			sendStartedAt := time.Now()
			if err := vc.SendOpusFrame(frame); err != nil {
				logger.Error("Failed sending opus frame", "err", err)
				endReason = PlaybackEndTransportFailure
				endErr = fmt.Errorf("failed sending opus frame: %w", err)
				p.markVoiceConnectionDead()
				return
			}
			if sendDuration := time.Since(sendStartedAt); sendDuration > 5*time.Millisecond {
				logger.Warn(
					"Discord voice send stalled",
					"frame_count", frameCount+1,
					"send_duration_ms", sendDuration.Milliseconds(),
				)
			}
		} else {
			if err := vc.SendOpusFrame(frame); err != nil {
				logger.Error("Failed sending opus frame", "err", err)
				endReason = PlaybackEndTransportFailure
				endErr = fmt.Errorf("failed sending opus frame: %w", err)
				p.markVoiceConnectionDead()
				return
			}
		}
		if startupTrace != nil && !firstFrameSentLogged {
			startupTrace.Finish("First frame sent")
			firstFrameSentLogged = true
		}

		session.signalStarted()
		frameCount++
		if debugPlayback && bufferReporter != nil && frameCount%500 == 0 {
			buffered, capacity := bufferReporter.BufferLevel()
			bufferFillPct := 0
			if capacity > 0 {
				bufferFillPct = buffered * 100 / capacity
			}
			logger.Info(
				"Playback buffer level",
				"frame_count", frameCount,
				"buffered_frames", buffered,
				"buffer_capacity", capacity,
				"buffer_fill_pct", bufferFillPct,
			)
		}
		if frameCount%1000 == 0 {
			logger.PlaybackFramesMilestone(frameCount)
		}

		nextFrameDeadline = nextFrameDeadline.Add(opusFrameInterval)
		if delay := nextFrameDeadline.Sub(nowOpusFrame()); delay > 0 {
			sleepOpusFrame(delay)
		} else if delay < -(3 * opusFrameInterval) {
			if debugPlayback {
				logger.Warn(
					"Playback deadline reset; this is the strongest signal for the ~600ms audio drop",
					"frame_count", frameCount,
					"drift_ms", (-delay).Milliseconds(),
				)
			}
			nextFrameDeadline = nowOpusFrame()
		}
	}
}

// WaitForCompletion waits for the current track to finish. It deliberately has
// no arbitrary timeout: advancing while an active encoder is still running
// corrupts playback state and can leave two sessions transmitting at once.
func (p *GuildPlayer) WaitForCompletion() {
	_ = p.WaitForCompletionResult()
}

// WaitForCompletionResult waits for the selected playback session and returns
// its terminal result. It is safe to call after WaitForCompletion; results are
// immutable once the done channel closes.
func (p *GuildPlayer) WaitForCompletionResult() PlaybackResult {
	p.mu.RLock()
	session := p.activePlayback
	if session == nil {
		session = p.lastPlayback
	}
	p.mu.RUnlock()

	if session == nil {
		return PlaybackResult{Reason: PlaybackEndNone}
	}

	<-session.done
	return session.resultSnapshot()
}

// LastPlaybackResult returns the active session's partial result or the most
// recent terminal result. Call WaitForCompletionResult when a terminal result
// is required.
func (p *GuildPlayer) LastPlaybackResult() PlaybackResult {
	p.mu.RLock()
	session := p.activePlayback
	if session == nil {
		session = p.lastPlayback
	}
	p.mu.RUnlock()
	if session == nil {
		return PlaybackResult{Reason: PlaybackEndNone}
	}
	return session.resultSnapshot()
}

// PlaybackSignals returns channels for playback start and completion notifications.
func (p *GuildPlayer) PlaybackSignals() (<-chan struct{}, <-chan struct{}) {
	p.mu.RLock()
	session := p.activePlayback
	if session == nil {
		session = p.lastPlayback
	}
	p.mu.RUnlock()

	if session != nil {
		return session.started, session.done
	}

	started := make(chan struct{})
	done := make(chan struct{})
	close(done)
	return started, done
}

// Pause pauses playback
func (p *GuildPlayer) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.CurrentPosition = p.currentPositionLocked()
	p.playbackStartedAt = time.Time{}
	p.Paused = true
	p.Playing = false
}

// Resume resumes playback
func (p *GuildPlayer) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.Paused {
		p.Paused = false
		p.Playing = true
		p.playbackStartedAt = time.Now()
		p.volumeAtomic.Store(volumeToInt32(p.effectiveVolumeLocked()))
	}
}

// Stop stops playback completely
func (p *GuildPlayer) Stop() {
	p.mu.Lock()
	p.seekRequested = false
	p.skipRequested = false
	session := p.stopPlaybackLocked(true)
	p.mu.Unlock()

	if session != nil {
		session.stopAndCleanup(PlaybackEndStopped)
	}
}

// Skip skips to the next track
func (p *GuildPlayer) Skip() *Track {
	p.mu.Lock()
	p.seekRequested = false
	p.skipRequested = true
	session := p.stopPlaybackLocked(true)
	p.mu.Unlock()

	if session != nil {
		session.stopAndCleanup(PlaybackEndSkipped)
	}
	return p.Queue.Peek()
}

// Seek seeks to a position in the current track
func (p *GuildPlayer) Seek(position time.Duration) error {
	p.mu.Lock()

	track := p.Queue.Current()
	if track == nil {
		p.mu.Unlock()
		return fmt.Errorf("no track currently playing")
	}

	if position < 0 || (!track.IsLive && position > track.Duration) {
		p.mu.Unlock()
		return fmt.Errorf("invalid seek position")
	}

	p.CurrentPosition = position
	p.seekRequested = true
	p.skipRequested = false
	p.requestedStartOffset = position
	session := p.stopPlaybackLocked(false)
	p.CurrentPosition = position
	p.mu.Unlock()

	if session != nil {
		session.stopAndCleanup(PlaybackEndSeeked)
	}

	return nil
}

// SetVolume sets the playback volume (0-100)
func (p *GuildPlayer) SetVolume(volume int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if volume < 0 || volume > 100 {
		return fmt.Errorf("volume must be between 0 and 100")
	}

	p.Volume = volume
	p.OriginalVolume = volume
	p.volumeAtomic.Store(volumeToInt32(p.effectiveVolumeLocked()))
	return nil
}

// ReduceVolume reduces volume when someone speaks
func (p *GuildPlayer) ReduceVolume() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.ReduceOnVoice || !p.Playing || len(p.activeSpeakers) > 0 {
		return
	}

	p.OriginalVolume = p.Volume
	p.volumeAtomic.Store(volumeToInt32(p.ReduceOnVoiceTarget))
}

// RestoreVolume restores volume after speaking ends
func (p *GuildPlayer) RestoreVolume() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.ReduceOnVoice || !p.Playing {
		return
	}

	p.volumeAtomic.Store(volumeToInt32(p.effectiveVolumeLocked()))
}

// SpeakerStarted tracks a user starting to speak and ducks volume if first speaker.
func (p *GuildPlayer) SpeakerStarted(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.activeSpeakers[userID] = struct{}{}
	if len(p.activeSpeakers) == 1 && p.ReduceOnVoice && p.Playing {
		p.OriginalVolume = p.Volume
		p.volumeAtomic.Store(volumeToInt32(p.ReduceOnVoiceTarget))
	}
}

// SpeakerStopped tracks a user stopping speaking and restores volume if no speakers remain.
func (p *GuildPlayer) SpeakerStopped(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.activeSpeakers, userID)
	if len(p.activeSpeakers) == 0 && p.ReduceOnVoice && p.Playing {
		p.volumeAtomic.Store(volumeToInt32(p.effectiveVolumeLocked()))
	}
}

// SetVoiceConnection safely sets the voice connection reference.
func (p *GuildPlayer) SetVoiceConnection(vc voiceconn.Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.VoiceConnection = vc
	p.voiceReadyWait = vc != nil
}

// GetCurrentPosition safely returns the current playback position.
func (p *GuildPlayer) GetCurrentPosition() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.currentPositionLocked()
}

// GetPlaybackState returns a snapshot of the current playback flags and position.
func (p *GuildPlayer) GetPlaybackState() (playing bool, paused bool, position time.Duration) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Playing, p.Paused, p.currentPositionLocked()
}

// SetVoiceReductionEnabled toggles volume ducking on voice activity.
func (p *GuildPlayer) SetVoiceReductionEnabled(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ReduceOnVoice = enabled
	p.volumeAtomic.Store(volumeToInt32(p.effectiveVolumeLocked()))
}

// SetVoiceReductionTarget sets the ducked volume level.
func (p *GuildPlayer) SetVoiceReductionTarget(target int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if target < 0 || target > 100 {
		return fmt.Errorf("volume must be between 0 and 100")
	}

	p.ReduceOnVoiceTarget = target
	p.volumeAtomic.Store(volumeToInt32(p.effectiveVolumeLocked()))
	return nil
}

// GetVoiceReductionConfig returns voice ducking settings.
func (p *GuildPlayer) GetVoiceReductionConfig() (bool, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ReduceOnVoice, p.ReduceOnVoiceTarget
}

// ConsumeSeekRequest reports and clears a pending seek restart request.
func (p *GuildPlayer) ConsumeSeekRequest() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	requested := p.seekRequested
	p.seekRequested = false
	return requested
}

// ConsumeSkipRequest reports whether the active session was explicitly
// skipped. The playback loop should bypass one-track loop mode when true and
// then advance to the immediate successor.
func (p *GuildPlayer) ConsumeSkipRequest() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	requested := p.skipRequested
	p.skipRequested = false
	return requested
}

// Disconnect disconnects from voice channel
func (p *GuildPlayer) Disconnect() error {
	p.mu.Lock()
	p.seekRequested = false
	p.skipRequested = false
	session := p.stopPlaybackLocked(true)
	vc := p.VoiceConnection
	p.VoiceConnection = nil
	p.voiceReadyWait = false
	p.clearActiveSpeakersLocked()
	p.mu.Unlock()

	if session != nil {
		session.stopAndCleanup(PlaybackEndDisconnected)
	}

	if vc != nil {
		return vc.Disconnect(context.Background())
	}

	return nil
}

// StartLoopIfIdle marks the playback loop as running if it was previously idle.
func (p *GuildPlayer) StartLoopIfIdle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.LoopRunning {
		return false
	}

	p.LoopRunning = true
	return true
}

// IsLoopRunning safely checks if the playback loop is running
func (p *GuildPlayer) IsLoopRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.LoopRunning
}

// SetLoopRunning safely sets the playback loop running state
func (p *GuildPlayer) SetLoopRunning(running bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.LoopRunning = running
}

// IsVoiceConnected safely checks if voice connection exists
func (p *GuildPlayer) IsVoiceConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.VoiceConnection != nil
}

// ClearVoiceConnection safely clears the voice connection reference
func (p *GuildPlayer) ClearVoiceConnection() {
	p.mu.Lock()
	session := p.stopPlaybackLocked(false)
	p.VoiceConnection = nil
	p.voiceReadyWait = false
	p.clearActiveSpeakersLocked()
	p.mu.Unlock()

	if session != nil {
		session.stopAndCleanup(PlaybackEndTransportFailure)
	}
}

// markVoiceConnectionDead clears the voice connection after a send failure.
// This lets the playLoop detect the dead connection and attempt a rejoin.
func (p *GuildPlayer) markVoiceConnectionDead() {
	p.mu.Lock()
	p.VoiceConnection = nil
	p.voiceReadyWait = false
	p.clearActiveSpeakersLocked()
	p.mu.Unlock()
}

func (p *GuildPlayer) consumeVoiceReadyWait() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.VoiceConnection == nil || !p.voiceReadyWait {
		return false
	}

	p.voiceReadyWait = false
	return true
}

func (p *GuildPlayer) stopPlaybackLocked(resetPosition bool) *playbackSession {
	p.CurrentPosition = p.currentPositionLocked()
	p.Playing = false
	p.Paused = false
	p.playbackStartedAt = time.Time{}
	if resetPosition {
		p.CurrentPosition = 0
		p.requestedStartOffset = 0
	}

	return p.activePlayback
}

func (p *GuildPlayer) effectiveVolumeLocked() int {
	if p.ReduceOnVoice && p.Playing && len(p.activeSpeakers) > 0 {
		return p.ReduceOnVoiceTarget
	}
	return p.Volume
}

func volumeToInt32(volume int) int32 {
	if volume <= 0 {
		return 0
	}
	if volume >= 100 {
		return 100
	}
	return int32(volume) //nolint:gosec // volume is bounded to the inclusive range 1..99 above.
}

func (p *GuildPlayer) clearActiveSpeakersLocked() {
	clear(p.activeSpeakers)
	p.volumeAtomic.Store(volumeToInt32(p.effectiveVolumeLocked()))
}

func (p *GuildPlayer) currentPositionLocked() time.Duration {
	position := p.CurrentPosition
	if p.Playing && !p.playbackStartedAt.IsZero() {
		position += time.Since(p.playbackStartedAt)
	}
	return position
}
