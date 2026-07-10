package player

import (
	"errors"
	"testing"
)

func TestCustomEncoderBufferLevel(t *testing.T) {
	encoder := &CustomEncoder{
		frameChan: make(chan []byte, 4),
	}

	encoder.frameChan <- []byte("a")
	encoder.frameChan <- []byte("b")

	buffered, capacity := encoder.BufferLevel()
	if buffered != 2 || capacity != 4 {
		t.Fatalf("BufferLevel() = (%d, %d), want (2, 4)", buffered, capacity)
	}
}

func TestStreamingEncoderBufferLevel(t *testing.T) {
	encoder := &StreamingEncoder{
		frameChan: make(chan []byte, 6),
	}

	encoder.frameChan <- []byte("a")
	encoder.frameChan <- []byte("b")
	encoder.frameChan <- []byte("c")

	buffered, capacity := encoder.BufferLevel()
	if buffered != 3 || capacity != 6 {
		t.Fatalf("BufferLevel() = (%d, %d), want (3, 6)", buffered, capacity)
	}
}

func TestEncoderReturnsTerminalErrorAfterFramesClose(t *testing.T) {
	want := errors.New("ffmpeg failed")
	custom := &CustomEncoder{
		frameChan:   make(chan []byte),
		terminalErr: want,
	}
	close(custom.frameChan)
	if _, err := custom.OpusFrame(); !errors.Is(err, want) {
		t.Fatalf("CustomEncoder.OpusFrame() error = %v, want %v", err, want)
	}

	streaming := &StreamingEncoder{
		frameChan:   make(chan []byte),
		terminalErr: want,
	}
	close(streaming.frameChan)
	if _, err := streaming.OpusFrame(); !errors.Is(err, want) {
		t.Fatalf("StreamingEncoder.OpusFrame() error = %v, want %v", err, want)
	}
}
