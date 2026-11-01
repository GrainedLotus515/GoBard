package playback

import (
	"context"
	"testing"
)

func TestPauseResumeAndCancellation(t *testing.T) {
	c, err := NewControls(100)
	if err != nil {
		t.Fatal(err)
	}
	c.SetPaused(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.wait(ctx) }()
	select {
	case <-done:
		t.Fatal("paused gate passed")
	default:
	}
	c.SetPaused(false)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	c.SetPaused(true)
	go func() { done <- c.wait(ctx) }()
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
	if err := c.SetVolume(-1); err == nil {
		t.Fatal("invalid volume accepted")
	}
}
