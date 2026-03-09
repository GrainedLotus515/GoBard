package player

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/voiceconn"
)

const opusFrameInterval = 20 * time.Millisecond

var (
	newCustomEncoder = func(inputPath string, sampleRate, channels int, startOffset time.Duration) (EncoderInterface, error) {
		return NewCustomEncoder(inputPath, sampleRate, channels, startOffset)
	}
	newStreamingEncoder = func(url, streamURL string, sampleRate, channels int, startOffset time.Duration) (EncoderInterface, error) {
		return NewStreamingEncoder(url, streamURL, sampleRate, channels, startOffset)
	}
	nowOpusFrame    = time.Now
	sleepOpusFrame  = time.Sleep
	sleepVoiceReady = func() {
		time.Sleep(200 * time.Millisecond)
	}
)

// EncoderInterface defines the interface for audio encoders
type EncoderInterface interface {
	OpusFrame() ([]byte, error)
	Cleanup() error
}

// GuildPlayer manages playback for a single guild
type GuildPlayer struct {
	GuildID         string
	Queue           *Queue
	VoiceConnection voiceconn.Connection

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

	// Encoder
	stopChan        chan bool
	doneChan        chan struct{}
	startedChan     chan struct{}
	doneSignaled    bool
	startedSignaled bool
	encoder         EncoderInterface

	seekRequested        bool
	requestedStartOffset time.Duration
	playbackStartedAt    time.Time

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
		GuildID:     guildID,
		Queue:       NewQueue(),
		Volume:      100,
		stopChan:    make(chan bool, 1),
		doneChan:    make(chan struct{}),
		startedChan: make(chan struct{}),
	}

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
	p.startedChan = make(chan struct{})
	p.doneChan = make(chan struct{})
	p.startedSignaled = false
	p.doneSignaled = false

	// Drain any stale stop signal from previous playback
	select {
	case <-p.stopChan:
	default:
	}

	startedChan := p.startedChan
	doneChan := p.doneChan

	// Start playback in goroutine
	go p.playTrack(track, startOffset, startedChan, doneChan)

	return nil
}

// playTrack handles the actual playback of a track
func (p *GuildPlayer) playTrack(track *Track, startOffset time.Duration, startedChan chan struct{}, doneChan chan struct{}) {
	logger.PlaybackStart(track.Title)

	// Ensure completion is always signaled, regardless of exit path
	defer func() {
		p.signalPlaybackDone(startedChan, doneChan)
	}()

	p.mu.Lock()
	if p.VoiceConnection == nil {
		logger.Error("No voice connection available")
		p.mu.Unlock()
		return
	}
	vc := p.VoiceConnection
	p.mu.Unlock()

	// Create appropriate encoder based on whether we have a cached file
	var encoder EncoderInterface
	var err error

	if track.LocalPath != "" {
		// Use cached file
		logger.Info("Using cached file", "path", track.LocalPath)
		logger.PlaybackEncodingStart(track.LocalPath)
		encoder, err = newCustomEncoder(track.LocalPath, 48000, 2, startOffset)
	} else {
		// Stream directly from URL
		logger.Info("Streaming from URL", "url", track.URL)
		logger.PlaybackEncodingStart(track.URL)
		encoder, err = newStreamingEncoder(track.URL, track.StreamURL, 48000, 2, startOffset)
	}

	if err != nil {
		logger.PlaybackEncodingError(err)
		p.mu.Lock()
		p.Playing = false
		p.mu.Unlock()
		return
	}
	logger.PlaybackEncodingSuccess()

	p.mu.Lock()
	p.encoder = encoder
	p.mu.Unlock()

	// Wait for voice connection to be ready
	logger.PlaybackVoiceWaiting()
	sleepVoiceReady() // Give voice connection time to stabilize before streaming begins.

	// Set speaking state BEFORE streaming
	logger.PlaybackSpeakingStart()
	if err := vc.SetSpeaking(context.Background(), true); err != nil {
		logger.PlaybackSpeakingError(err)
	}

	// Manual frame sending
	logger.PlaybackFrameStart()

	frameCount := 0
	playbackStarted := false
	nextFrameDeadline := nowOpusFrame()
	for {
		// Check for pause
		p.mu.RLock()
		paused := p.Paused
		p.mu.RUnlock()

		if paused {
			time.Sleep(100 * time.Millisecond)
			// Check for stop during pause
			select {
			case <-p.stopChan:
				logger.PlaybackStopped(frameCount)
				_ = vc.SetSpeaking(context.Background(), false)
				return
			default:
			}
			continue
		}

		// Check voice connection periodically (every 100 frames ≈ 2 seconds)
		if frameCount > 0 && frameCount%100 == 0 {
			p.mu.RLock()
			vcValid := p.VoiceConnection != nil
			p.mu.RUnlock()
			if !vcValid {
				logger.Error("Voice connection lost during playback")
				return
			}
		}

		// Check for stop signal
		select {
		case <-p.stopChan:
			logger.PlaybackStopped(frameCount)
			_ = vc.SetSpeaking(context.Background(), false)
			return
		default:
		}

		// Read opus frame
		frame, err := encoder.OpusFrame()
		if err != nil {
			if err != io.EOF {
				logger.PlaybackFrameError(err)
			} else {
				logger.PlaybackFramesComplete(frameCount)
			}
			break
		}

		if err := vc.SendOpusFrame(frame); err != nil {
			logger.Error("Failed sending opus frame", "err", err)
			return
		}
		if !playbackStarted {
			p.signalPlaybackStarted(startedChan)
			playbackStarted = true
		}
		frameCount++
		if frameCount%1000 == 0 {
			logger.PlaybackFramesMilestone(frameCount)
		}
		nextFrameDeadline = nextFrameDeadline.Add(opusFrameInterval)
		if delay := nextFrameDeadline.Sub(nowOpusFrame()); delay > 0 {
			sleepOpusFrame(delay)
		} else if delay < -(3 * opusFrameInterval) {
			nextFrameDeadline = nowOpusFrame()
		}
	}

	// Clear speaking state
	logger.PlaybackSpeakingStop()
	_ = vc.SetSpeaking(context.Background(), false)

	// Cleanup
	p.mu.Lock()
	if p.encoder != nil {
		p.encoder.Cleanup()
		p.encoder = nil
	}
	p.CurrentPosition = p.currentPositionLocked()
	p.Playing = false
	p.playbackStartedAt = time.Time{}
	p.mu.Unlock()
}

// WaitForCompletion waits for the current track to finish
func (p *GuildPlayer) WaitForCompletion() {
	p.mu.RLock()
	doneChan := p.doneChan
	p.mu.RUnlock()

	select {
	case <-doneChan:
	case <-time.After(3 * time.Hour): // Max track length safety
		logger.Info("Track completion timeout reached, continuing")
	}
}

// PlaybackSignals returns channels for playback start and completion notifications.
func (p *GuildPlayer) PlaybackSignals() (<-chan struct{}, <-chan struct{}) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.startedChan, p.doneChan
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
	defer p.mu.Unlock()

	p.seekRequested = false
	p.stopPlaybackLocked(true)
}

// Skip skips to the next track
func (p *GuildPlayer) Skip() *Track {
	p.Stop()

	// Return what will play next (peek without advancing)
	// Note: The playLoop will handle actually advancing the queue
	return p.Queue.Peek()
}

// Seek seeks to a position in the current track
func (p *GuildPlayer) Seek(position time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	track := p.Queue.Current()
	if track == nil {
		return fmt.Errorf("no track currently playing")
	}

	if position < 0 || (!track.IsLive && position > track.Duration) {
		return fmt.Errorf("invalid seek position")
	}

	p.CurrentPosition = position
	p.seekRequested = true
	p.requestedStartOffset = position
	p.stopPlaybackLocked(false)
	p.CurrentPosition = position

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
}

// RestoreVolume restores volume after speaking ends
func (p *GuildPlayer) RestoreVolume() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.ReduceOnVoice || !p.Playing {
		return
	}

	p.Volume = p.OriginalVolume
}

// SetVoiceConnection safely sets the voice connection reference.
func (p *GuildPlayer) SetVoiceConnection(vc voiceconn.Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.VoiceConnection = vc
}

// GetCurrentPosition safely returns the current playback position.
func (p *GuildPlayer) GetCurrentPosition() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.currentPositionLocked()
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
	p.Stop()

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.VoiceConnection != nil {
		err := p.VoiceConnection.Disconnect(context.Background())
		p.VoiceConnection = nil
		return err
	}

	return nil
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
}

func (p *GuildPlayer) stopPlaybackLocked(resetPosition bool) {
	p.CurrentPosition = p.currentPositionLocked()
	p.Playing = false
	p.Paused = false
	p.playbackStartedAt = time.Time{}
	if resetPosition {
		p.CurrentPosition = 0
		p.requestedStartOffset = 0
	}

	// Stop streaming
	select {
	case p.stopChan <- true:
	default:
	}

	// Cleanup encoder
	if p.encoder != nil {
		p.encoder.Cleanup()
		p.encoder = nil
	}
}

func (p *GuildPlayer) signalPlaybackStarted(startedChan chan struct{}) {
	if startedChan == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.startedChan == startedChan {
		if p.startedSignaled {
			return
		}
		p.startedSignaled = true
	}

	close(startedChan)
}

func (p *GuildPlayer) signalPlaybackDone(startedChan chan struct{}, doneChan chan struct{}) {
	if doneChan == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.doneChan == doneChan {
		if p.doneSignaled {
			return
		}
		p.doneSignaled = true
	}

	close(doneChan)
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
