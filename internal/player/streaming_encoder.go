package player

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// streamingBeforeYTDLPAcquire is a narrow test seam for proving cancellation
// while a session is queued behind the shared process limiter.
var streamingBeforeYTDLPAcquire = func() {}

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
	frameChan           chan EncodedFrame
	framePool           *encodedFramePool
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
	return NewStreamingEncoderContext(context.Background(), url, streamURL, streamHeaders, sampleRate, channels, startOffset, vol)
}

// NewStreamingEncoderContext creates the media pipeline with a caller-owned
// cancellation boundary.  In particular, a stopped playback session must not
// remain queued behind the global yt-dlp capacity limiter.
//
//nolint:gosec,nestif // Inputs are validated by the source-resolution boundary; commands use argument arrays, never a shell.
func NewStreamingEncoderContext(parent context.Context, url, streamURL string, streamHeaders map[string]string, sampleRate, channels int, startOffset time.Duration, vol *atomic.Int32) (*StreamingEncoder, error) {
	if parent == nil {
		parent = context.Background()
	}
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
		ffmpegCmd = exec.CommandContext(parent, "ffmpeg", buildDirectStreamingFFmpegArgs(streamURL, streamHeaders, sampleRate, channels, startOffset)...)
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

		streamingBeforeYTDLPAcquire()
		acquireCtx, cancel := context.WithTimeout(parent, ytdlpProcessAcquireTimeout)
		defer cancel()
		waitStarted := time.Now()
		releaseYTDLPProcess, err = processlimit.AcquireGlobalClass(acquireCtx, processlimit.Bulk)
		if err != nil {
			return nil, fmt.Errorf("wait for yt-dlp capacity: %w", err)
		}
		logger.Timing("yt-dlp slot acquired", "work_class", processlimit.Bulk, "wait_ms", time.Since(waitStarted).Milliseconds())

		logger.Info("Starting yt-dlp -> FFmpeg pipeline")

		// Use yt-dlp to stream audio directly to FFmpeg.
		// This avoids 403 errors when a direct media URL is unavailable or stale.
		ytdlpCmd = exec.CommandContext(parent, "yt-dlp", buildStreamingYTDLPArgs(url)...)

		ffmpegCmd = exec.CommandContext(parent, "ffmpeg", buildStreamingFFmpegArgs(sampleRate, channels, startOffset)...)

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
		frameChan:           make(chan EncodedFrame, encodedFrameBufferCapacity),
		framePool:           newEncodedFramePool(),
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
		intentionalStop := e.stopRequested()
		if intentionalStop {
			e.stopProcesses()
		}
		if err := e.waitProcesses(); err != nil && !intentionalStop {
			e.setTerminalError(fmt.Errorf("media process exited unsuccessfully: %w", err))
		}
		close(e.frameChan)
	}()
	debugPlayback := isDebugPlaybackEnabled()

	logger.Info("Starting encode loop")

	// PCM buffer: frameSize samples * channels * 2 bytes per sample
	pcmBufferSize := e.frameSize * e.channels * 2
	pcmBuffer := make([]byte, pcmBufferSize)
	pcmSamples := make([]int16, e.frameSize*e.channels)
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
			opusFrameBuffer, ok := e.framePool.borrow(e.stopChan)
			if !ok {
				return
			}
			encoded, err := e.opusEncoder.Encode(frameData, opusFrameBuffer)
			if err != nil {
				EncodedFrame{Data: opusFrameBuffer, pool: e.framePool}.Release()
				e.setTerminalError(fmt.Errorf("encode opus frame: %w", err))
				logger.Error("Opus encoding error", "err", err, "frames_encoded", frameCount)
				return
			}

			opusFrame := EncodedFrame{Data: opusFrameBuffer[:encoded], pool: e.framePool}
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
				opusFrame.Release()
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
				opusFrameBuffer, ok := e.framePool.borrow(e.stopChan)
				if !ok {
					return
				}
				encoded, err := e.opusEncoder.Encode(pcmSamples[:samplesPerFrame], opusFrameBuffer)
				if err != nil {
					EncodedFrame{Data: opusFrameBuffer, pool: e.framePool}.Release()
					e.setTerminalError(fmt.Errorf("encode final opus frame: %w", err))
					logger.Error("Opus encoding error on final partial frame", "err", err, "frames_encoded", frameCount)
					return
				}
				opusFrame := EncodedFrame{Data: opusFrameBuffer[:encoded], pool: e.framePool}
				select {
				case e.frameChan <- opusFrame:
				case <-e.stopChan:
					opusFrame.Release()
					logger.Info("Encode loop stopped while sending final frame", "frames_encoded", frameCount)
					return
				}
			}
			logger.Info("Stream ended normally", "frames_encoded", frameCount)
			return
		}
	}
}

func (e *StreamingEncoder) stopRequested() bool {
	select {
	case <-e.stopChan:
		return true
	default:
		return false
	}
}

// OpusFrame returns the next Opus frame from the encoding stream
func (e *StreamingEncoder) OpusFrame() (EncodedFrame, error) {
	frame, ok := <-e.frameChan
	if !ok {
		if err := e.getTerminalError(); err != nil {
			return EncodedFrame{}, err
		}
		return EncodedFrame{}, io.EOF
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
	applyPCMVolume(samples, e.volume.Load())
}

// Cleanup stops the encoder and releases resources
func (e *StreamingEncoder) Cleanup() error {
	e.mu.Lock()
	if !e.done {
		e.done = true
		e.mu.Unlock()
		e.stopOnce.Do(func() { close(e.stopChan) })
		e.stopProcesses()
		if err := e.waitProcesses(); err != nil {
			logger.Debug("Encoder processes exited during cleanup", "err", err)
		}
		return nil
	}
	e.mu.Unlock()
	if err := e.waitProcesses(); err != nil {
		logger.Debug("Encoder processes exited during repeated cleanup", "err", err)
	}
	return nil
}

func (e *StreamingEncoder) stopProcesses() {
	stopProcess(e.ffmpegCmd)
	stopProcess(e.ytdlpCmd)
}

func (e *StreamingEncoder) waitProcesses() error {
	var waitErr error
	e.waitOnce.Do(func() {
		if err := waitProcessError(e.ffmpegCmd); err != nil {
			waitErr = fmt.Errorf("ffmpeg: %w", err)
		}
		if err := waitProcessError(e.ytdlpCmd); err != nil && waitErr == nil {
			waitErr = fmt.Errorf("yt-dlp: %w", err)
		}
		e.mu.Lock()
		if e.terminalErr == nil && waitErr != nil && !e.stopRequested() {
			e.terminalErr = waitErr
		}
		e.mu.Unlock()
		if e.releaseYTDLPProcess != nil {
			e.releaseYTDLPProcess()
			e.releaseYTDLPProcess = nil
		}
	})
	return waitErr
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
	if err := waitProcessError(cmd); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			logger.Debug("Unable to wait for encoder process", "err", err)
		}
	}
}

func waitProcessError(cmd *exec.Cmd) error {
	if cmd == nil {
		return nil
	}

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			logger.Debug("Unable to wait for encoder process", "err", err)
		}
		return err
	}
	return nil
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
