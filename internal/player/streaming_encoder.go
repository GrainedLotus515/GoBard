package player

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

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
// If streamURL is provided, it uses that directly; otherwise fetches via yt-dlp
func NewStreamingEncoder(url string, streamURL string, sampleRate, channels int) (*StreamingEncoder, error) {
	start := time.Now()

	frameSize := 960 // 20ms at 48kHz
	if sampleRate != 48000 {
		frameSize = (sampleRate * 20) / 1000
	}

	var finalStreamURL string

	if streamURL != "" {
		// Use pre-fetched URL (fast path)
		logger.Info("Using pre-fetched stream URL", "url_length", len(streamURL))
		logger.Timing("Stream URL extraction", "source", "pre-fetched", "duration_ms", 0)
		finalStreamURL = streamURL
	} else {
		// Fallback: fetch URL from yt-dlp (slow path, ~7 seconds)
		logger.Info("Getting stream URL from yt-dlp (no pre-fetched URL)")
		ytdlpStart := time.Now()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		ytdlpCmd := exec.CommandContext(ctx,
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
			if ctx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("yt-dlp timed out after 30 seconds")
			}
			logger.Error("yt-dlp command failed", "stderr", ytdlpStderr.String())
			return nil, fmt.Errorf("failed to get stream URL: %w", err)
		}

		finalStreamURL = strings.TrimSpace(string(urlOutput))
		logger.Timing("Stream URL extraction", "source", "yt-dlp fallback", "duration_ms", time.Since(ytdlpStart).Milliseconds())
	}

	if finalStreamURL == "" {
		return nil, fmt.Errorf("no stream URL available")
	}

	logger.Info("Got stream URL, starting FFmpeg", "url_length", len(finalStreamURL))

	// FFmpeg streams directly from the URL (FFmpeg handles HTTP natively)
	ffmpegCmd := exec.Command(
		"ffmpeg",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
		"-i", finalStreamURL, // Direct URL instead of pipe:0
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", fmt.Sprintf("%d", channels),
		"-loglevel", "error", // Only show errors
		"pipe:1", // Output to stdout
	)

	// Get stdout and stderr from FFmpeg
	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create ffmpeg stdout pipe: %w", err)
	}

	ffmpegStderr, err := ffmpegCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create ffmpeg stderr pipe: %w", err)
	}

	// Start FFmpeg
	if err := ffmpegCmd.Start(); err != nil {
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
		frameChan:   make(chan []byte, 300), // Increased from 100 to 300 (~6 seconds buffer)
		stopChan:    make(chan bool, 1),
	}

	// Start stderr monitoring goroutine
	go encoder.monitorFFmpegErrors(ffmpegStderr)

	// Start the encoding goroutine
	go encoder.encodeLoop(ffmpegStdout)

	logger.Timing("Encoder creation completed", "duration_ms", time.Since(start).Milliseconds())
	return encoder, nil
}

// monitorFFmpegErrors reads and logs FFmpeg stderr output
func (e *StreamingEncoder) monitorFFmpegErrors(stderr io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		if n > 0 {
			logger.Error("FFmpeg error", "output", string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

// encodeLoop reads PCM data from FFmpeg and encodes to Opus frames
func (e *StreamingEncoder) encodeLoop(reader io.Reader) {
	defer close(e.frameChan)

	logger.Info("Starting encode loop")

	// PCM buffer: frameSize samples * channels * 2 bytes per sample
	pcmBufferSize := e.frameSize * e.channels * 2
	pcmBuffer := make([]byte, pcmBufferSize)
	pcmSamples := make([]int16, e.frameSize*e.channels)

	frameCount := 0
	var firstFrameTime time.Time

	for {
		select {
		case <-e.stopChan:
			logger.Info("Encode loop stopped by signal", "frames_encoded", frameCount)
			e.ffmpegCmd.Process.Kill()
			return
		default:
		}

		// Read PCM data from FFmpeg
		n, err := reader.Read(pcmBuffer)
		if err != nil {
			// Handle both EOF and unexpected EOF as end of stream
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				logger.Info("Stream ended normally", "frames_encoded", frameCount)
			} else {
				logger.Error("FFmpeg read error", "err", err, "frames_encoded", frameCount)
			}
			return
		}

		if n == 0 {
			continue
		}

		if frameCount == 0 {
			firstFrameTime = time.Now()
			logger.Info("First PCM data received", "bytes", n)
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
				logger.Error("Opus encoding error", "err", err, "frames_encoded", frameCount)
				return
			}

			// Send only the encoded bytes
			opusFrame := opusFrameBuffer[:opusBytes]
			select {
			case e.frameChan <- opusFrame:
				frameCount++
				if frameCount == 1 {
					logger.Timing("First opus frame ready", "duration_ms", time.Since(firstFrameTime).Milliseconds())
				}
				if frameCount%500 == 0 {
					logger.Info("Streaming progress", "frames_encoded", frameCount)
				}
			case <-e.stopChan:
				logger.Info("Encode loop stopped while sending frame", "frames_encoded", frameCount)
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
