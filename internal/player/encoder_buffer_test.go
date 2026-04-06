package player

import "testing"

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
