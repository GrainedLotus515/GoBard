package player

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/bwmarrin/discordgo"
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
	VoiceConnection *discordgo.VoiceConnection

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
	stopChan chan bool
	doneChan chan bool
	encoder  EncoderInterface

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
		GuildID:  guildID,
		Queue:    NewQueue(),
		Volume:   100,
		stopChan: make(chan bool, 1),
		doneChan: make(chan bool, 1),
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

	// Drain any stale completion signal
	select {
	case <-p.doneChan:
	default:
	}

	// Start playback in goroutine
	go p.playTrack(track)

	return nil
}

// playTrack handles the actual playback of a track
func (p *GuildPlayer) playTrack(track *Track) {
	logger.PlaybackStart(track.Title)

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
		encoder, err = NewCustomEncoder(track.LocalPath, 48000, 2)
	} else {
		// Stream directly from URL
		logger.Info("Streaming from URL", "url", track.URL)
		logger.PlaybackEncodingStart(track.URL)
		encoder, err = NewStreamingEncoder(track.URL, 48000, 2)
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
	time.Sleep(500 * time.Millisecond) // Give voice connection time to stabilize

	// Set speaking state BEFORE streaming
	logger.PlaybackSpeakingStart()
	if err := vc.Speaking(true); err != nil {
		logger.PlaybackSpeakingError(err)
	}

	// Manual frame sending
	logger.PlaybackFrameStart()

	frameCount := 0
	for {
		// Check for pause
		p.mu.RLock()
		paused := p.Paused
		p.mu.RUnlock()

		if paused {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Check for stop signal
		select {
		case <-p.stopChan:
			logger.PlaybackStopped(frameCount)
			vc.Speaking(false)
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

		// Send frame to voice connection
		select {
		case vc.OpusSend <- frame:
			frameCount++
			if frameCount%1000 == 0 {
				logger.PlaybackFramesMilestone(frameCount)
			}
		case <-p.stopChan:
			logger.PlaybackStopped(frameCount)
			vc.Speaking(false)
			return
		}
	}

	// Clear speaking state
	logger.PlaybackSpeakingStop()
	vc.Speaking(false)

	// Cleanup
	p.mu.Lock()
	if p.encoder != nil {
		p.encoder.Cleanup()
		p.encoder = nil
	}
	p.Playing = false

	// Signal completion
	select {
	case p.doneChan <- true:
	default:
	}
	p.mu.Unlock()
}

// WaitForCompletion waits for the current track to finish
func (p *GuildPlayer) WaitForCompletion() {
	select {
	case <-p.doneChan:
	case <-time.After(3 * time.Hour): // Max track length safety
		logger.Info("Track completion timeout reached, continuing")
	}
}

// Pause pauses playback
func (p *GuildPlayer) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()

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
	}
}

// Stop stops playback completely
func (p *GuildPlayer) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.Playing = false
	p.Paused = false
	p.CurrentPosition = 0

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

// Skip skips to the next track
func (p *GuildPlayer) Skip() *Track {
	p.Stop()

	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return what will play next (peek without advancing)
	if p.Queue.CurrentIndex+1 < len(p.Queue.Tracks) {
		return p.Queue.Tracks[p.Queue.CurrentIndex+1]
	}
	return nil
}

// Seek seeks to a position in the current track
func (p *GuildPlayer) Seek(position time.Duration) error {
	// Stop current playback first to prevent duplicate streams
	p.Stop()

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

	// Restart playback from new position
	p.Playing = true
	p.Paused = false
	go p.playTrack(track)

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

// streamToVoice streams audio data to Discord voice connection
func (p *GuildPlayer) streamToVoice(reader io.Reader) error {
	// This will handle streaming PCM audio to Discord
	// TODO: Implement
	return nil
}
