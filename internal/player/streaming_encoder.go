package player

import (
	"fmt"
	"io"
	"math"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/hraban/opus"
)

// StreamingEncoder handles streaming audio encoding using either a direct media URL
// or a yt-dlp -> FFmpeg pipeline before libopus encoding.
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
	volume      *atomic.Int32
}

// NewStreamingEncoder creates a new streaming audio encoder
func NewStreamingEncoder(url, streamURL string, streamHeaders map[string]string, sampleRate, channels int, startOffset time.Duration, vol *atomic.Int32) (*StreamingEncoder, error) {
	start := time.Now()

	frameSize := 960 // 20ms at 48kHz
	if sampleRate != 48000 {
		frameSize = (sampleRate * 20) / 1000
	}

	var (
		ytdlpCmd     *exec.Cmd
		ffmpegCmd    *exec.Cmd
		ffmpegStdout io.ReadCloser
		ffmpegStderr io.ReadCloser
		err          error
	)

	if streamURL != "" {
		logger.Info("Starting direct FFmpeg stream", "url", streamURL)
		ffmpegCmd = exec.Command("ffmpeg", buildDirectStreamingFFmpegArgs(streamURL, streamHeaders, sampleRate, channels, startOffset)...)
		ffmpegStdout, err = ffmpegCmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create direct ffmpeg stdout pipe: %w", err)
		}

		ffmpegStderr, err = ffmpegCmd.StderrPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create direct ffmpeg stderr pipe: %w", err)
		}

		if err := ffmpegCmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start direct ffmpeg stream: %w", err)
		}
	} else {
		logger.Info("Starting yt-dlp -> FFmpeg pipeline")

		// Use yt-dlp to stream audio directly to FFmpeg.
		// This avoids 403 errors when a direct media URL is unavailable or stale.
		ytdlpCmd = exec.Command(
			"yt-dlp",
			"-f", "bestaudio",
			"--no-warnings",
			"-o", "-", // Output to stdout
			"--",
			url,
		)

		ffmpegCmd = exec.Command("ffmpeg", buildStreamingFFmpegArgs(sampleRate, channels, startOffset)...)

		ytdlpStdout, err := ytdlpCmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create yt-dlp stdout pipe: %w", err)
		}

		ffmpegCmd.Stdin = ytdlpStdout

		ffmpegStdout, err = ffmpegCmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create ffmpeg stdout pipe: %w", err)
		}

		ffmpegStderr, err = ffmpegCmd.StderrPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create ffmpeg stderr pipe: %w", err)
		}

		if err := ytdlpCmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start yt-dlp: %w", err)
		}

		if err := ffmpegCmd.Start(); err != nil {
			stopProcess(ytdlpCmd)
			return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
		}
	}

	// Create Opus encoder
	opusEnc, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
	if err != nil {
		stopProcess(ffmpegCmd)
		stopProcess(ytdlpCmd)
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
		frameChan:   make(chan []byte, 1000), // Increased from 100 to 1000 (~20 seconds buffer)
		stopChan:    make(chan bool, 1),
		volume:      vol,
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
	debugPlayback := isDebugPlaybackEnabled()

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
			stopProcess(e.ffmpegCmd)
			stopProcess(e.ytdlpCmd)
			return
		default:
		}

		// Read PCM data from FFmpeg
		n, readErr := io.ReadFull(reader, pcmBuffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			logger.Error("FFmpeg read error", "err", readErr, "frames_encoded", frameCount)
			return
		}

		if n == 0 {
			logger.Info("Stream ended normally", "frames_encoded", frameCount)
			return
		}

		if frameCount == 0 {
			firstFrameTime = time.Now()
			logger.Info("First PCM data received", "bytes", n)
		}

		// Convert bytes to int16 samples
		for i := 0; i < n/2; i++ {
			pcmSamples[i] = int16(pcmBuffer[i*2]) | (int16(pcmBuffer[i*2+1]) << 8)
		}

		e.applyVolume(pcmSamples[:n/2])

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
				if debugPlayback && frameCount%100 == 0 {
					buffered, capacity := len(e.frameChan), cap(e.frameChan)
					if buffered < capacity/10 {
						logger.Warn(
							"Encoder buffer running low",
							"frames_encoded", frameCount,
							"buffered_frames", buffered,
							"buffer_capacity", capacity,
						)
					}
				}
				if frameCount%500 == 0 {
					logger.Info("Streaming progress", "frames_encoded", frameCount)
				}
			case <-e.stopChan:
				logger.Info("Encode loop stopped while sending frame", "frames_encoded", frameCount)
				stopProcess(e.ffmpegCmd)
				stopProcess(e.ytdlpCmd)
				return
			}
		}

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			logger.Info("Stream ended normally", "frames_encoded", frameCount)
			return
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

// BufferLevel reports the number of buffered frames and channel capacity.
func (e *StreamingEncoder) BufferLevel() (int, int) {
	return len(e.frameChan), cap(e.frameChan)
}

func (e *StreamingEncoder) applyVolume(samples []int16) {
	vol := float64(e.volume.Load()) / 100.0
	if vol < 0.999 || vol > 1.001 {
		for i, s := range samples {
			scaled := float64(s) * vol
			if scaled > math.MaxInt16 {
				scaled = math.MaxInt16
			} else if scaled < math.MinInt16 {
				scaled = math.MinInt16
			}
			samples[i] = int16(scaled)
		}
	}
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
	stopProcess(e.ffmpegCmd)
	stopProcess(e.ytdlpCmd)

	// Wait for processes to exit (ignore errors)
	if e.ffmpegCmd != nil {
		waitProcess(e.ffmpegCmd)
	}
	if e.ytdlpCmd != nil {
		waitProcess(e.ytdlpCmd)
	}

	return nil
}

func stopProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	_ = cmd.Process.Kill()
}

func waitProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	_ = cmd.Wait()
}
