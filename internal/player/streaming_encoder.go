package player

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/hraban/opus"
)

// StreamingEncoder handles streaming audio encoding using yt-dlp + FFmpeg + libopus
// It pipes yt-dlp stdout directly to FFmpeg stdin for immediate playback
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
// It streams audio directly from yt-dlp to FFmpeg to Opus without downloading
func NewStreamingEncoder(url string, sampleRate, channels int) (*StreamingEncoder, error) {
	frameSize := 960 // 20ms at 48kHz
	if sampleRate != 48000 {
		frameSize = (sampleRate * 20) / 1000
	}

	// yt-dlp command to stream audio to stdout
	ytdlpCmd := exec.Command(
		"yt-dlp",
		"-f", "bestaudio",
		"-o", "-", // Output to stdout
		"--no-part",      // Don't use .part files
		"--no-cache-dir", // Don't cache
		"--quiet",        // Suppress output
		"--no-warnings",  // Suppress warnings
		url,
	)

	// FFmpeg command to convert audio to PCM s16le
	ffmpegCmd := exec.Command(
		"ffmpeg",
		"-i", "pipe:0", // Read from stdin
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", fmt.Sprintf("%d", channels),
		"-loglevel", "quiet", // Suppress FFmpeg output
		"pipe:1", // Output to stdout
	)

	// Capture stderr to suppress output
	var ytdlpStderr, ffmpegStderr bytes.Buffer
	ytdlpCmd.Stderr = &ytdlpStderr
	ffmpegCmd.Stderr = &ffmpegStderr

	// Get stdout from yt-dlp
	ytdlpStdout, err := ytdlpCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create yt-dlp stdout pipe: %w", err)
	}

	// Get stdout from FFmpeg
	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create ffmpeg stdout pipe: %w", err)
	}

	// Connect yt-dlp stdout to FFmpeg stdin
	ffmpegCmd.Stdin = ytdlpStdout

	// Start yt-dlp
	if err := ytdlpCmd.Start(); err != nil {
		logger.Error("yt-dlp command failed", "stderr", ytdlpStderr.String())
		return nil, fmt.Errorf("failed to start yt-dlp: %w", err)
	}

	// Start FFmpeg
	if err := ffmpegCmd.Start(); err != nil {
		ytdlpCmd.Process.Kill()
		logger.Error("FFmpeg command failed", "stderr", ffmpegStderr.String())
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Create Opus encoder
	opusEnc, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
	if err != nil {
		ytdlpCmd.Process.Kill()
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
			e.ytdlpCmd.Process.Kill()
			e.ffmpegCmd.Process.Kill()
			return
		default:
		}

		// Read PCM data from FFmpeg
		n, err := reader.Read(pcmBuffer)
		if err != nil {
			if err != io.EOF {
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
			n, err := e.opusEncoder.Encode(frameData, opusFrameBuffer)
			if err != nil {
				logger.Error("Opus encoding error", "err", err)
				return
			}

			// Send only the encoded bytes
			opusFrame := opusFrameBuffer[:n]
			select {
			case e.frameChan <- opusFrame:
			case <-e.stopChan:
				e.ytdlpCmd.Process.Kill()
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

	// Kill both processes
	if e.ytdlpCmd.Process != nil {
		e.ytdlpCmd.Process.Kill()
	}
	if e.ffmpegCmd.Process != nil {
		e.ffmpegCmd.Process.Kill()
	}

	// Wait for processes to exit
	e.ytdlpCmd.Wait()
	return e.ffmpegCmd.Wait()
}
