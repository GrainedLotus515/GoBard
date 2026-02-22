package player

import (
	"testing"
	"time"
)

func TestSeekSignalsPlaybackRestart(t *testing.T) {
	p := NewManager().GetPlayer("guild-1")

	track := &Track{
		Title:    "test",
		Duration: 2 * time.Minute,
	}
	p.Queue.Add(track)
	if got := p.Queue.Next(); got == nil {
		t.Fatal("Queue.Next() returned nil, expected current track")
	}

	p.Playing = true

	if err := p.Seek(30 * time.Second); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}

	if got := p.GetCurrentPosition(); got != 30*time.Second {
		t.Fatalf("current position = %v, want %v", got, 30*time.Second)
	}
	if p.requestedStartOffset != 30*time.Second {
		t.Fatalf("requested start offset = %v, want %v", p.requestedStartOffset, 30*time.Second)
	}

	if !p.ConsumeSeekRequest() {
		t.Fatal("ConsumeSeekRequest() = false, want true")
	}
	if p.ConsumeSeekRequest() {
		t.Fatal("ConsumeSeekRequest() should clear pending request")
	}

	if p.Playing {
		t.Fatal("playing should be false after seek stop signal")
	}

	if got := p.Queue.Current(); got != track {
		t.Fatalf("current track changed after seek: got %v want %v", got, track)
	}

	select {
	case <-p.stopChan:
	default:
		t.Fatal("seek should signal stop channel")
	}
}

func TestStopClearsSeekStateAndPosition(t *testing.T) {
	p := NewManager().GetPlayer("guild-2")

	track := &Track{
		Title:    "test",
		Duration: time.Minute,
	}
	p.Queue.Add(track)
	if got := p.Queue.Next(); got == nil {
		t.Fatal("Queue.Next() returned nil, expected current track")
	}

	if err := p.Seek(15 * time.Second); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}

	p.Stop()

	if got := p.GetCurrentPosition(); got != 0 {
		t.Fatalf("current position after Stop() = %v, want 0", got)
	}
	if p.requestedStartOffset != 0 {
		t.Fatalf("requested start offset after Stop() = %v, want 0", p.requestedStartOffset)
	}

	if p.ConsumeSeekRequest() {
		t.Fatal("seek request should be cleared by Stop()")
	}
}
