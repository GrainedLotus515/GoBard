package player

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/hraban/opus"
	"github.com/lotus/gobard/internal/logger"
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
	stopChan    chan bool
}

// NewCustomEncoder creates a new audio encoder using FFmpeg + libopus
func NewCustomEncoder(source string, sampleRate, channels int) (*CustomEncoder, error) {
	frameSize := 960 // 20ms at 48kHz
	if sampleRate != 48000 {
		frameSize = (sampleRate * 20) / 1000
	}

	// FFmpeg command to convert audio to PCM s16le
	cmd := exec.Command(
		"ffmpeg",
		"-i", source,
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", fmt.Sprintf("%d", channels),
		"-",
	)

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
		cmd.Process.Kill()
		return nil, fmt.Errorf("failed to create opus encoder: %w", err)
	}

	// Set bitrate to 128kbps
	opusEnc.SetBitrate(128000)

	encoder := &CustomEncoder{
		cmd:         cmd,
		stdout:      stdout,
		opusEncoder: opusEnc,
		frameSize:   frameSize,
		channels:    channels,
		sampleRate:  sampleRate,
		done:        false,
		frameChan:   make(chan []byte, 100),
		stopChan:    make(chan bool, 1),
	}

	// Start the encoding goroutine
	go encoder.encodeLoop()

	return encoder, nil
}

// encodeLoop reads PCM data and encodes to Opus frames
func (e *CustomEncoder) encodeLoop() {
	defer close(e.frameChan)

	// PCM buffer: frameSize samples * channels * 2 bytes per sample
	pcmBufferSize := e.frameSize * e.channels * 2
	pcmBuffer := make([]byte, pcmBufferSize)
	pcmSamples := make([]int16, e.frameSize*e.channels)

	for {
		select {
		case <-e.stopChan:
			e.cmd.Process.Kill()
			return
		default:
		}

		// Read PCM data from FFmpeg
		n, err := e.stdout.Read(pcmBuffer)
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
				e.cmd.Process.Kill()
				return
			}
		}
	}
}

// OpusFrame returns the next Opus frame from the encoding stream
func (e *CustomEncoder) OpusFrame() ([]byte, error) {
	frame, ok := <-e.frameChan
	if !ok {
		return nil, io.EOF
	}
	return frame, nil
}

// Cleanup stops the encoder and releases resources
func (e *CustomEncoder) Cleanup() error {
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

	// Kill the FFmpeg process
	if e.cmd.Process != nil {
		e.cmd.Process.Kill()
	}

	return e.cmd.Wait()
}
