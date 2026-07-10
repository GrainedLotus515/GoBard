package player

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hraban/opus"

	"github.com/GrainedLotus515/gobard/internal/logger"
)

// CustomEncoder handles audio encoding using FFmpeg + libopus
type CustomEncoder struct {
	cmd         *exec.Cmd
	stdout      io.Reader
	opusEncoder *opus.Encoder
	frameSize   int
	channels    int
	sampleRate  int
	mu          sync.Mutex
	done        bool
	frameChan   chan []byte
	stopChan    chan struct{}
	stopOnce    sync.Once
	waitOnce    sync.Once
	waitErr     error
	terminalErr error
	volume      *atomic.Int32
}

// NewCustomEncoder creates a new audio encoder using FFmpeg + libopus.
//
//nolint:gosec // source is a cache path controlled by the cache subsystem; no shell is involved.
func NewCustomEncoder(source string, sampleRate, channels int, startOffset time.Duration, vol *atomic.Int32) (*CustomEncoder, error) {
	frameSize := 960 // 20ms at 48kHz
	if sampleRate != 48000 {
		frameSize = (sampleRate * 20) / 1000
	}

	// FFmpeg command to convert audio to PCM s16le
	cmd := exec.Command("ffmpeg", buildFileFFmpegArgs(source, sampleRate, channels, startOffset)...)

	// Capture stderr to suppress FFmpeg output
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		logger.Error("FFmpeg command failed", "stderr", stderr.String())
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Create Opus encoder
	opusEnc, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
	if err != nil {
		stopProcess(cmd)
		waitProcess(cmd)
		return nil, fmt.Errorf("failed to create opus encoder: %w", err)
	}

	// Set bitrate to 128kbps
	if err := opusEnc.SetBitrate(128000); err != nil {
		stopProcess(cmd)
		waitProcess(cmd)
		return nil, fmt.Errorf("set opus bitrate: %w", err)
	}

	encoder := &CustomEncoder{
		cmd:         cmd,
		stdout:      stdout,
		opusEncoder: opusEnc,
		frameSize:   frameSize,
		channels:    channels,
		sampleRate:  sampleRate,
		done:        false,
		frameChan:   make(chan []byte, encodedFrameBufferCapacity),
		stopChan:    make(chan struct{}),
		volume:      vol,
	}

	// Start the encoding goroutine
	go encoder.encodeLoop()

	return encoder, nil
}

// encodeLoop reads PCM data and encodes to Opus frames.
//
//nolint:gocyclo // The loop intentionally keeps read, conversion, and final-frame cleanup together.
func (e *CustomEncoder) encodeLoop() {
	defer func() {
		close(e.frameChan)
		e.stopProcess()
		if err := e.waitProcess(); err != nil {
			logger.Debug("FFmpeg process exited during encoder cleanup", "err", err)
		}
	}()
	debugPlayback := isDebugPlaybackEnabled()

	// PCM buffer: frameSize samples * channels * 2 bytes per sample
	pcmBufferSize := e.frameSize * e.channels * 2
	pcmBuffer := make([]byte, pcmBufferSize)
	pcmSamples := make([]int16, e.frameSize*e.channels)
	opusFrameBuffer := make([]byte, 4000) // Reusable Opus encode buffer (libopus recommended max)
	samplesPerFrame := e.frameSize * e.channels
	frameCount := 0

	for {
		select {
		case <-e.stopChan:
			return
		default:
		}

		// Read PCM data from FFmpeg
		n, readErr := io.ReadFull(e.stdout, pcmBuffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			e.setTerminalError(fmt.Errorf("read ffmpeg PCM: %w", readErr))
			logger.Error("FFmpeg read error", "err", readErr)
			return
		}

		if n == 0 {
			return
		}

		// Convert bytes to int16 samples
		for i := 0; i < n/2; i++ {
			pcmSamples[i] = int16(pcmBuffer[i*2]) | (int16(pcmBuffer[i*2+1]) << 8)
		}

		e.applyVolume(pcmSamples[:n/2])

		// Encode full frames
		for i := 0; i+samplesPerFrame <= n/2; i += samplesPerFrame {
			frameData := pcmSamples[i : i+samplesPerFrame]
			encoded, err := e.opusEncoder.Encode(frameData, opusFrameBuffer)
			if err != nil {
				e.setTerminalError(fmt.Errorf("encode opus frame: %w", err))
				logger.Error("Opus encoding error", "err", err)
				return
			}

			opusFrame := make([]byte, encoded)
			copy(opusFrame, opusFrameBuffer[:encoded])
			select {
			case e.frameChan <- opusFrame:
				if debugPlayback {
					frameCount++
					if frameCount%100 == 0 {
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
				}
			case <-e.stopChan:
				return
			}
		}

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			// Zero-pad and encode any remaining partial frame
			if remaining := (n / 2) % samplesPerFrame; remaining > 0 {
				for i := n / 2; i < samplesPerFrame; i++ {
					pcmSamples[i] = 0
				}
				encoded, err := e.opusEncoder.Encode(pcmSamples[:samplesPerFrame], opusFrameBuffer)
				if err != nil {
					e.setTerminalError(fmt.Errorf("encode final opus frame: %w", err))
					logger.Error("Opus encoding error on final partial frame", "err", err)
					return
				}
				opusFrame := make([]byte, encoded)
				copy(opusFrame, opusFrameBuffer[:encoded])
				select {
				case e.frameChan <- opusFrame:
				case <-e.stopChan:
					return
				}
			}
			return
		}
	}
}

// OpusFrame returns the next Opus frame from the encoding stream
func (e *CustomEncoder) OpusFrame() ([]byte, error) {
	frame, ok := <-e.frameChan
	if !ok {
		if err := e.getTerminalError(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return frame, nil
}

func (e *CustomEncoder) setTerminalError(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	if e.terminalErr == nil {
		e.terminalErr = err
	}
	e.mu.Unlock()
}

func (e *CustomEncoder) getTerminalError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.terminalErr
}

// BufferLevel reports the number of buffered frames and channel capacity.
func (e *CustomEncoder) BufferLevel() (int, int) {
	return len(e.frameChan), cap(e.frameChan)
}

func (e *CustomEncoder) applyVolume(samples []int16) {
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
func (e *CustomEncoder) Cleanup() error {
	e.mu.Lock()
	if !e.done {
		e.done = true
		e.mu.Unlock()
		e.stopOnce.Do(func() { close(e.stopChan) })
		e.stopProcess()
		return e.waitProcess()
	}
	e.mu.Unlock()
	return e.waitProcess()
}

func (e *CustomEncoder) stopProcess() {
	stopProcess(e.cmd)
}

func (e *CustomEncoder) waitProcess() error {
	e.waitOnce.Do(func() {
		if e.cmd != nil {
			e.waitErr = e.cmd.Wait()
		}
	})
	return e.waitErr
}

func buildFileFFmpegArgs(source string, sampleRate, channels int, startOffset time.Duration) []string {
	args := make([]string, 0, 16)
	if startOffset > 0 {
		args = append(args, "-ss", formatFFmpegTimestamp(startOffset))
	}
	args = append(args,
		"-i", source,
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", fmt.Sprintf("%d", channels),
		"-",
	)
	return args
}

func buildStreamingFFmpegArgs(sampleRate, channels int, startOffset time.Duration) []string {
	args := []string{
		"-i", "pipe:0", // Read from stdin
	}
	if startOffset > 0 {
		// Output seeking works even on non-seekable input pipes.
		args = append(args, "-ss", formatFFmpegTimestamp(startOffset))
	}
	args = append(args,
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", fmt.Sprintf("%d", channels),
		"-loglevel", "error",
		"pipe:1",
	)
	return args
}

func buildDirectStreamingFFmpegArgs(source string, headers map[string]string, sampleRate, channels int, startOffset time.Duration) []string {
	args := make([]string, 0, 24)
	if headerArg := formatFFmpegHeaders(headers); headerArg != "" {
		args = append(args, "-headers", headerArg)
	}

	args = append(args,
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
		// The stream URL was checked before starting FFmpeg. Following an HTTP
		// redirect would bypass that check and turn the decoder into an SSRF hop.
		"-max_redirects", "0",
		"-i", source,
	)
	if startOffset > 0 {
		args = append(args, "-ss", formatFFmpegTimestamp(startOffset))
	}
	args = append(args,
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", fmt.Sprintf("%d", channels),
		"-loglevel", "error",
		"pipe:1",
	)
	return args
}

func formatFFmpegHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}

	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(headers[key])
		builder.WriteString("\r\n")
	}

	return builder.String()
}

func formatFFmpegTimestamp(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalMillis := d.Milliseconds()
	hours := totalMillis / (1000 * 60 * 60)
	minutes := (totalMillis / (1000 * 60)) % 60
	seconds := (totalMillis / 1000) % 60
	millis := totalMillis % 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, millis)
}
