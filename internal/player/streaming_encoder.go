package player

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/hraban/opus"
)

// StreamingEncoder handles streaming audio encoding using yt-dlp + FFmpeg + libopus
// It uses a two-step process: yt-dlp gets the direct URL, then FFmpeg streams from it
type StreamingEncoder struct {
	ffmpegCmd   *exec.Cmd
	opusEncoder *opus.Encoder
	frameSize   int
	channels    int
	sampleRate  int
	mu          sync.Mutex
	done        bool
	frameChan   chan []byte
	stopChan    chan bool
}

// NewStreamingEncoder creates a new streaming audio encoder
// It uses yt-dlp to get the direct stream URL, then FFmpeg streams from that URL
func NewStreamingEncoder(url string, sampleRate, channels int) (*StreamingEncoder, error) {
	frameSize := 960 // 20ms at 48kHz
	if sampleRate != 48000 {
		frameSize = (sampleRate * 20) / 1000
	}

	// Step 1: Get direct stream URL from yt-dlp (blocking call, ~1-2 seconds)
	logger.Info("Getting stream URL from yt-dlp")
	ytdlpCmd := exec.Command(
		"yt-dlp",
		"-f", "bestaudio",
		"-g", // Get URL only
		"--no-warnings",
		url,
	)

	var ytdlpStderr bytes.Buffer
	ytdlpCmd.Stderr = &ytdlpStderr

	urlOutput, err := ytdlpCmd.Output()
	if err != nil {
		logger.Error("yt-dlp command failed", "stderr", ytdlpStderr.String())
		return nil, fmt.Errorf("failed to get stream URL: %w", err)
	}

	streamURL := strings.TrimSpace(string(urlOutput))
	if streamURL == "" {
		return nil, fmt.Errorf("yt-dlp returned empty stream URL")
	}

	logger.Info("Got stream URL, starting FFmpeg", "url_length", len(streamURL))

	// Step 2: FFmpeg streams directly from the URL (FFmpeg handles HTTP natively)
	ffmpegCmd := exec.Command(
		"ffmpeg",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
		"-i", streamURL, // Direct URL instead of pipe:0
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", fmt.Sprintf("%d", channels),
		"-loglevel", "error", // Only show errors
		"pipe:1", // Output to stdout
	)

	var ffmpegStderr bytes.Buffer
	ffmpegCmd.Stderr = &ffmpegStderr

	// Get stdout from FFmpeg
	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create ffmpeg stdout pipe: %w", err)
	}

	// Start FFmpeg
	if err := ffmpegCmd.Start(); err != nil {
		logger.Error("FFmpeg command failed", "stderr", ffmpegStderr.String())
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Create Opus encoder
	opusEnc, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
	if err != nil {
		ffmpegCmd.Process.Kill()
		return nil, fmt.Errorf("failed to create opus encoder: %w", err)
	}

	// Set bitrate to 128kbps
	opusEnc.SetBitrate(128000)

	encoder := &StreamingEncoder{
		ffmpegCmd:   ffmpegCmd,
		opusEncoder: opusEnc,
		frameSize:   frameSize,
		channels:    channels,
		sampleRate:  sampleRate,
		done:        false,
		frameChan:   make(chan []byte, 100),
		stopChan:    make(chan bool, 1),
	}

	// Start the encoding goroutine
	go encoder.encodeLoop(ffmpegStdout)

	return encoder, nil
}

// encodeLoop reads PCM data from FFmpeg and encodes to Opus frames
func (e *StreamingEncoder) encodeLoop(reader io.Reader) {
	defer close(e.frameChan)

	// PCM buffer: frameSize samples * channels * 2 bytes per sample
	pcmBufferSize := e.frameSize * e.channels * 2
	pcmBuffer := make([]byte, pcmBufferSize)
	pcmSamples := make([]int16, e.frameSize*e.channels)

	for {
		select {
		case <-e.stopChan:
			e.ffmpegCmd.Process.Kill()
			return
		default:
		}

		// Read PCM data from FFmpeg
		n, err := reader.Read(pcmBuffer)
		if err != nil {
			// Handle both EOF and unexpected EOF as end of stream
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				logger.Info("Stream ended")
			} else {
				logger.Error("FFmpeg read error", "err", err)
			}
			return
		}

		if n == 0 {
			continue
		}

		// Convert bytes to int16 samples
		for i := 0; i < n/2; i++ {
			pcmSamples[i] = int16(pcmBuffer[i*2]) | (int16(pcmBuffer[i*2+1]) << 8)
		}

		// Encode full frames
		samplesPerFrame := e.frameSize * e.channels
		for i := 0; i+samplesPerFrame <= n/2; i += samplesPerFrame {
			frameData := pcmSamples[i : i+samplesPerFrame]
			opusFrameBuffer := make([]byte, 4000)
			opusBytes, err := e.opusEncoder.Encode(frameData, opusFrameBuffer)
			if err != nil {
				logger.Error("Opus encoding error", "err", err)
				return
			}

			// Send only the encoded bytes
			opusFrame := opusFrameBuffer[:opusBytes]
			select {
			case e.frameChan <- opusFrame:
			case <-e.stopChan:
				e.ffmpegCmd.Process.Kill()
				return
			}
		}
	}
}

// OpusFrame returns the next Opus frame from the encoding stream
func (e *StreamingEncoder) OpusFrame() ([]byte, error) {
	frame, ok := <-e.frameChan
	if !ok {
		return nil, io.EOF
	}
	return frame, nil
}

// Cleanup stops the encoder and releases resources
func (e *StreamingEncoder) Cleanup() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.done {
		return nil
	}

	e.done = true

	// Signal the encoding loop to stop
	select {
	case e.stopChan <- true:
	default:
	}

	// Kill FFmpeg process
	if e.ffmpegCmd.Process != nil {
		e.ffmpegCmd.Process.Kill()
	}

	// Wait for process to exit
	return e.ffmpegCmd.Wait()
}
