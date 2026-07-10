package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hraban/opus"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/sourceurl"
)

const ytdlpProcessAcquireTimeout = 30 * time.Second

// StreamingEncoder handles streaming audio encoding using either a direct media URL
// or a yt-dlp -> FFmpeg pipeline before libopus encoding.
type StreamingEncoder struct {
	ytdlpCmd            *exec.Cmd
	ffmpegCmd           *exec.Cmd
	opusEncoder         *opus.Encoder
	frameSize           int
	channels            int
	sampleRate          int
	mu                  sync.Mutex
	done                bool
	frameChan           chan []byte
	stopChan            chan struct{}
	stopOnce            sync.Once
	waitOnce            sync.Once
	terminalErr         error
	volume              *atomic.Int32
	releaseYTDLPProcess func()
}

// NewStreamingEncoder creates a new streaming audio encoder.
//
//nolint:gosec,nestif // Inputs are validated by the source-resolution boundary; commands use argument arrays, never a shell.
func NewStreamingEncoder(url, streamURL string, streamHeaders map[string]string, sampleRate, channels int, startOffset time.Duration, vol *atomic.Int32) (*StreamingEncoder, error) {
	start := time.Now()

	frameSize := 960 // 20ms at 48kHz
	if sampleRate != 48000 {
		frameSize = (sampleRate * 20) / 1000
	}

	var (
		ytdlpCmd            *exec.Cmd
		ffmpegCmd           *exec.Cmd
		ffmpegStdout        io.ReadCloser
		ffmpegStderr        io.ReadCloser
		ytdlpStderr         io.ReadCloser
		releaseYTDLPProcess func()
		err                 error
	)
	created := false
	defer func() {
		if !created && releaseYTDLPProcess != nil {
			releaseYTDLPProcess()
		}
	}()

	if streamURL != "" {
		if !sourceurl.IsPublicHTTPURL(streamURL) {
			return nil, fmt.Errorf("direct media stream URL is not permitted")
		}
		logger.Info("Starting direct FFmpeg stream")
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
		canonicalURL, validationErr := sourceurl.ValidateCanonicalYouTubeVideoURL(url)
		if validationErr != nil {
			return nil, fmt.Errorf("YouTube stream URL is not permitted: %w", validationErr)
		}
		url = canonicalURL

		acquireCtx, cancel := context.WithTimeout(context.Background(), ytdlpProcessAcquireTimeout)
		defer cancel()
		releaseYTDLPProcess, err = processlimit.AcquireGlobal(acquireCtx)
		if err != nil {
			return nil, fmt.Errorf("wait for yt-dlp capacity: %w", err)
		}

		logger.Info("Starting yt-dlp -> FFmpeg pipeline")

		// Use yt-dlp to stream audio directly to FFmpeg.
		// This avoids 403 errors when a direct media URL is unavailable or stale.
		ytdlpCmd = exec.Command("yt-dlp", buildStreamingYTDLPArgs(url)...)

		ffmpegCmd = exec.Command("ffmpeg", buildStreamingFFmpegArgs(sampleRate, channels, startOffset)...)

		ytdlpStdout, err := ytdlpCmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create yt-dlp stdout pipe: %w", err)
		}
		ytdlpStderr, err = ytdlpCmd.StderrPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create yt-dlp stderr pipe: %w", err)
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
			waitProcess(ytdlpCmd)
			return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
		}
	}

	// Create Opus encoder
	opusEnc, err := opus.NewEncoder(sampleRate, channels, opus.AppAudio)
	if err != nil {
		stopProcess(ffmpegCmd)
		stopProcess(ytdlpCmd)
		waitProcess(ffmpegCmd)
		waitProcess(ytdlpCmd)
		return nil, fmt.Errorf("failed to create opus encoder: %w", err)
	}

	// Set bitrate to 128kbps
	if err := opusEnc.SetBitrate(128000); err != nil {
		stopProcess(ffmpegCmd)
		stopProcess(ytdlpCmd)
		waitProcess(ffmpegCmd)
		waitProcess(ytdlpCmd)
		return nil, fmt.Errorf("set opus bitrate: %w", err)
	}

	encoder := &StreamingEncoder{
		ytdlpCmd:            ytdlpCmd,
		ffmpegCmd:           ffmpegCmd,
		opusEncoder:         opusEnc,
		frameSize:           frameSize,
		channels:            channels,
		sampleRate:          sampleRate,
		done:                false,
		frameChan:           make(chan []byte, encodedFrameBufferCapacity),
		stopChan:            make(chan struct{}),
		volume:              vol,
		releaseYTDLPProcess: releaseYTDLPProcess,
	}
	created = true

	// Start stderr monitoring goroutine
	go encoder.monitorFFmpegErrors(ffmpegStderr)
	if ytdlpStderr != nil {
		go encoder.monitorYTDLPErrors(ytdlpStderr)
	}

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
			// FFmpeg echoes direct signed input URLs in many failures. Do not log
			// raw stderr because those URLs carry bearer-like query credentials.
			logger.Error("FFmpeg reported an error", "bytes", n)
		}
		if err != nil {
			return
		}
	}
}

// monitorYTDLPErrors reads and logs yt-dlp stderr output.
func (e *StreamingEncoder) monitorYTDLPErrors(stderr io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		if n > 0 {
			// yt-dlp can include signed media URLs in diagnostics as well.
			logger.Error("yt-dlp reported an error", "bytes", n)
		}
		if err != nil {
			return
		}
	}
}

// encodeLoop reads PCM data from FFmpeg and encodes to Opus frames.
//
//nolint:gocyclo // Streaming has explicit handling for stoppage, partial PCM, and paced output.
func (e *StreamingEncoder) encodeLoop(reader io.Reader) {
	defer func() {
		close(e.frameChan)
		e.stopProcesses()
		e.waitProcesses()
	}()
	debugPlayback := isDebugPlaybackEnabled()

	logger.Info("Starting encode loop")

	// PCM buffer: frameSize samples * channels * 2 bytes per sample
	pcmBufferSize := e.frameSize * e.channels * 2
	pcmBuffer := make([]byte, pcmBufferSize)
	pcmSamples := make([]int16, e.frameSize*e.channels)
	opusFrameBuffer := make([]byte, 4000) // Reusable Opus encode buffer (libopus recommended max)
	samplesPerFrame := e.frameSize * e.channels

	frameCount := 0
	var firstFrameTime time.Time

	for {
		select {
		case <-e.stopChan:
			logger.Info("Encode loop stopped by signal", "frames_encoded", frameCount)
			return
		default:
		}

		// Read PCM data from FFmpeg
		n, readErr := io.ReadFull(reader, pcmBuffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			e.setTerminalError(fmt.Errorf("read ffmpeg PCM: %w", readErr))
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
		for i := 0; i+samplesPerFrame <= n/2; i += samplesPerFrame {
			frameData := pcmSamples[i : i+samplesPerFrame]
			encoded, err := e.opusEncoder.Encode(frameData, opusFrameBuffer)
			if err != nil {
				e.setTerminalError(fmt.Errorf("encode opus frame: %w", err))
				logger.Error("Opus encoding error", "err", err, "frames_encoded", frameCount)
				return
			}

			opusFrame := make([]byte, encoded)
			copy(opusFrame, opusFrameBuffer[:encoded])
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
				if debugPlayback && frameCount%500 == 0 {
					logger.Debug("Streaming progress", "frames_encoded", frameCount)
				}
			case <-e.stopChan:
				logger.Info("Encode loop stopped while sending frame", "frames_encoded", frameCount)
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
					logger.Error("Opus encoding error on final partial frame", "err", err, "frames_encoded", frameCount)
					return
				}
				opusFrame := make([]byte, encoded)
				copy(opusFrame, opusFrameBuffer[:encoded])
				select {
				case e.frameChan <- opusFrame:
				case <-e.stopChan:
					logger.Info("Encode loop stopped while sending final frame", "frames_encoded", frameCount)
					return
				}
			}
			logger.Info("Stream ended normally", "frames_encoded", frameCount)
			return
		}
	}
}

// OpusFrame returns the next Opus frame from the encoding stream
func (e *StreamingEncoder) OpusFrame() ([]byte, error) {
	frame, ok := <-e.frameChan
	if !ok {
		if err := e.getTerminalError(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return frame, nil
}

func (e *StreamingEncoder) setTerminalError(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	if e.terminalErr == nil {
		e.terminalErr = err
	}
	e.mu.Unlock()
}

func (e *StreamingEncoder) getTerminalError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.terminalErr
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
	if !e.done {
		e.done = true
		e.mu.Unlock()
		e.stopOnce.Do(func() { close(e.stopChan) })
		e.stopProcesses()
		e.waitProcesses()
		return nil
	}
	e.mu.Unlock()
	e.waitProcesses()
	return nil
}

func (e *StreamingEncoder) stopProcesses() {
	stopProcess(e.ffmpegCmd)
	stopProcess(e.ytdlpCmd)
}

func (e *StreamingEncoder) waitProcesses() {
	e.waitOnce.Do(func() {
		waitProcess(e.ffmpegCmd)
		waitProcess(e.ytdlpCmd)
		if e.releaseYTDLPProcess != nil {
			e.releaseYTDLPProcess()
			e.releaseYTDLPProcess = nil
		}
	})
}

func stopProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		logger.Debug("Unable to stop encoder process", "err", err)
	}
}

func waitProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			logger.Debug("Unable to wait for encoder process", "err", err)
		}
	}
}

func buildStreamingYTDLPArgs(url string) []string {
	return []string{
		"-f", "bestaudio[ext=webm]/bestaudio",
		"--quiet",
		"--no-warnings",
		"--no-progress",
		"-o", "-",
		"--",
		url,
	}
}
