package player

import (
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/hraban/opus"
)

// StreamingEncoder handles streaming audio encoding using yt-dlp + FFmpeg + libopus
// It uses a pipeline: yt-dlp streams audio -> FFmpeg decodes -> libopus encodes
type StreamingEncoder struct {
	ytdlpCmd    *exec.Cmd
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
// streamURL parameter is kept for compatibility but ignored (we use yt-dlp pipeline instead)
func NewStreamingEncoder(url string, streamURL string, sampleRate, channels int) (*StreamingEncoder, error) {
	start := time.Now()

	frameSize := 960 // 20ms at 48kHz
	if sampleRate != 48000 {
		frameSize = (sampleRate * 20) / 1000
	}

	logger.Info("Starting yt-dlp -> FFmpeg pipeline")

	// Use yt-dlp to stream audio directly to FFmpeg
	// This avoids 403 errors since yt-dlp handles YouTube's authentication headers
	ytdlpCmd := exec.Command(
		"yt-dlp",
		"-f", "bestaudio",
		"--no-warnings",
		"-o", "-", // Output to stdout
		url, // Use original URL, not the extracted stream URL
	)

	// FFmpeg reads from stdin (piped from yt-dlp)
	ffmpegCmd := exec.Command(
		"ffmpeg",
		"-i", "pipe:0", // Read from stdin
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", fmt.Sprintf("%d", channels),
		"-loglevel", "error", // Only show errors
		"pipe:1", // Output to stdout
	)

	// Pipe yt-dlp stdout to FFmpeg stdin
	ytdlpStdout, err := ytdlpCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create yt-dlp stdout pipe: %w", err)
	}

	ffmpegCmd.Stdin = ytdlpStdout

	// Get stdout and stderr from FFmpeg
	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create ffmpeg stdout pipe: %w", err)
	}

	ffmpegStderr, err := ffmpegCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create ffmpeg stderr pipe: %w", err)
	}

	// Start both processes
	if err := ytdlpCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start yt-dlp: %w", err)
	}

	if err := ffmpegCmd.Start(); err != nil {
		ytdlpCmd.Process.Kill()
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
		ytdlpCmd:    ytdlpCmd,
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
			if e.ffmpegCmd.Process != nil {
				e.ffmpegCmd.Process.Kill()
			}
			if e.ytdlpCmd != nil && e.ytdlpCmd.Process != nil {
				e.ytdlpCmd.Process.Kill()
			}
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
				if e.ffmpegCmd.Process != nil {
					e.ffmpegCmd.Process.Kill()
				}
				if e.ytdlpCmd != nil && e.ytdlpCmd.Process != nil {
					e.ytdlpCmd.Process.Kill()
				}
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

	// Kill both processes
	if e.ffmpegCmd.Process != nil {
		e.ffmpegCmd.Process.Kill()
	}
	if e.ytdlpCmd != nil && e.ytdlpCmd.Process != nil {
		e.ytdlpCmd.Process.Kill()
	}

	// Wait for processes to exit (ignore errors)
	if e.ffmpegCmd != nil {
		e.ffmpegCmd.Wait()
	}
	if e.ytdlpCmd != nil {
		e.ytdlpCmd.Wait()
	}

	return nil
}
