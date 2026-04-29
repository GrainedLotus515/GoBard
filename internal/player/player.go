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

	mu      sync.Mutex
	encoder EncoderInterface
}

func newPlaybackSession() *playbackSession {
	return &playbackSession{
		stop:    make(chan struct{}),
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (s *playbackSession) stopPlayback() {
	s.stopOnce.Do(func() {
		close(s.stop)
	})
}

func (s *playbackSession) signalStarted() {
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
		_ = encoder.Cleanup()
	}
}

func (s *playbackSession) stopAndCleanup() {
	s.stopPlayback()
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

	volumeAtomic  atomic.Int32
	activeSpeakers map[string]struct{}

	seekRequested        bool
	requestedStartOffset time.Duration
	playbackStartedAt    time.Time
	activePlayback       *playbackSession
	lastPlayback         *playbackSession

	mu sync.RWMutex
}

// Manager manages all guild players
type Manager struct {
	players map[string]*GuildPlayer
	mu      sync.RWMutex
}

// NewManager creates a new player manager
func NewManager() *Manager {
	return &Manager{
		players: make(map[string]*GuildPlayer),
	}
}

// GetPlayer gets or creates a player for a guild
func (m *Manager) GetPlayer(guildID string) *GuildPlayer {
	m.mu.Lock()
	defer m.mu.Unlock()

	if player, exists := m.players[guildID]; exists {
		return player
	}

	player := &GuildPlayer{
		GuildID:       guildID,
		Queue:         NewQueue(),
		Volume:        100,
		activeSpeakers: make(map[string]struct{}),
	}
	player.volumeAtomic.Store(100)

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

	session := newPlaybackSession()
	p.activePlayback = session
	p.lastPlayback = session

	vol := &p.volumeAtomic
	go p.playTrack(session, track, startOffset, vol)

	return nil
}

// playTrack handles the actual playback of a track
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
	)

	logPlaybackPath := func(path string) {
		logger.Info("Playback path selected", "path", path, "title", track.Title, "trace_id", track.RequestTraceID)
		if startupTrace != nil {
			startupTrace.Step("Playback path selected", "path", path, "title", track.Title)
		}
	}

	defer func() {
		if speaking && vc != nil {
			logger.PlaybackSpeakingStop()
			_ = vc.SetSpeaking(context.Background(), false)
		}

		if encoderBound {
			session.cleanupEncoder()
		} else if encoder != nil {
			_ = encoder.Cleanup()
		}

		p.mu.Lock()
		if p.activePlayback == session {
			p.CurrentPosition = p.currentPositionLocked()
			p.Playing = false
			p.Paused = false
			p.playbackStartedAt = time.Time{}
			p.activePlayback = nil
			p.voiceReadyWait = true
		}
		p.mu.Unlock()

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
		logger.Error("No voice connection available")
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
		logger.Info("Streaming from URL", "url", track.URL)
		logger.PlaybackEncodingStart(track.URL)
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
			startupTrace.Step("Creating yt-dlp fallback stream encoder", "url", track.URL)
		}
		encoder, err = newStreamingEncoder(track.URL, streamURL, streamHeaders, 48000, 2, startOffset, vol)
	}

	if err != nil {
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

	nextFrameDeadline := nowOpusFrame()
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
			logger.PlaybackEncodingError(err)
			if startupTrace != nil {
				startupTrace.Finish("Fallback encoder creation failed", "err", err)
			}
			return
		}

		if session.isStopped() {
			_ = encoder.Cleanup()
			logger.PlaybackStopped(frameCount)
			return
		}

		if !session.bindEncoder(encoder) {
			_ = encoder.Cleanup()
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
	}
	speaking = true
	if startupTrace != nil {
		startupTrace.Step("Speaking state enabled")
	}

	logger.PlaybackFrameStart()

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

// WaitForCompletion waits for the current track to finish.
func (p *GuildPlayer) WaitForCompletion() {
	p.mu.RLock()
	session := p.activePlayback
	if session == nil {
		session = p.lastPlayback
	}
	p.mu.RUnlock()

	if session == nil {
		return
	}

	select {
	case <-session.done:
	case <-time.After(3 * time.Hour):
		logger.Info("Track completion timeout reached, continuing")
	}
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
	}
}

// Stop stops playback completely
func (p *GuildPlayer) Stop() {
	p.mu.Lock()
	p.seekRequested = false
	session := p.stopPlaybackLocked(true)
	p.mu.Unlock()

	if session != nil {
		session.stopAndCleanup()
	}
}

// Skip skips to the next track
func (p *GuildPlayer) Skip() *Track {
	p.Stop()
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
	p.requestedStartOffset = position
	session := p.stopPlaybackLocked(false)
	p.CurrentPosition = position
	p.mu.Unlock()

	if session != nil {
		session.stopAndCleanup()
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
	p.volumeAtomic.Store(int32(volume))
	return nil
}

// ReduceVolume reduces volume when someone speaks
func (p *GuildPlayer) ReduceVolume() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.ReduceOnVoice || !p.Playing {
		return
	}

	p.OriginalVolume = p.Volume
	p.Volume = p.ReduceOnVoiceTarget
	p.volumeAtomic.Store(int32(p.ReduceOnVoiceTarget))
}

// RestoreVolume restores volume after speaking ends
func (p *GuildPlayer) RestoreVolume() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.ReduceOnVoice || !p.Playing {
		return
	}

	p.Volume = p.OriginalVolume
	p.volumeAtomic.Store(int32(p.OriginalVolume))
}

// SpeakerStarted tracks a user starting to speak and ducks volume if first speaker.
func (p *GuildPlayer) SpeakerStarted(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.activeSpeakers[userID] = struct{}{}
	if len(p.activeSpeakers) == 1 && p.ReduceOnVoice && p.Playing {
		p.OriginalVolume = p.Volume
		p.Volume = p.ReduceOnVoiceTarget
		p.volumeAtomic.Store(int32(p.ReduceOnVoiceTarget))
	}
}

// SpeakerStopped tracks a user stopping speaking and restores volume if no speakers remain.
func (p *GuildPlayer) SpeakerStopped(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.activeSpeakers, userID)
	if len(p.activeSpeakers) == 0 && p.ReduceOnVoice && p.Playing {
		p.Volume = p.OriginalVolume
		p.volumeAtomic.Store(int32(p.OriginalVolume))
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
}

// SetVoiceReductionTarget sets the ducked volume level.
func (p *GuildPlayer) SetVoiceReductionTarget(target int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if target < 0 || target > 100 {
		return fmt.Errorf("volume must be between 0 and 100")
	}

	p.ReduceOnVoiceTarget = target
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

// Disconnect disconnects from voice channel
func (p *GuildPlayer) Disconnect() error {
	p.mu.Lock()
	p.seekRequested = false
	session := p.stopPlaybackLocked(true)
	vc := p.VoiceConnection
	p.VoiceConnection = nil
	p.voiceReadyWait = false
	p.mu.Unlock()

	if session != nil {
		session.stopAndCleanup()
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
	defer p.mu.Unlock()
	p.VoiceConnection = nil
	p.voiceReadyWait = false
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

func (p *GuildPlayer) currentPositionLocked() time.Duration {
	position := p.CurrentPosition
	if p.Playing && !p.playbackStartedAt.IsZero() {
		position += time.Since(p.playbackStartedAt)
	}
	return position
}

// streamToVoice streams audio data to Discord voice connection
func (p *GuildPlayer) streamToVoice(reader io.Reader) error {
	// This will handle streaming PCM audio to Discord
	// TODO: Implement
	return nil
}
