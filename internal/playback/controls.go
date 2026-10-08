package playback

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// Controls is an attempt's small shared data plane. The session is the only
// settings writer; the codec reads gain and the engine increments deliveries.
// Pause uses a notification channel rather than polling or a parked producer.
type Controls struct {
	volume  atomic.Int32
	frames  atomic.Uint64
	mu      sync.Mutex
	paused  bool
	changed chan struct{}
}

func NewControls(volume int) (*Controls, error) {
	c := &Controls{changed: make(chan struct{})}
	if err := c.SetVolume(volume); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Controls) SetVolume(volume int) error {
	if volume < 0 || volume > 100 {
		return errors.New("volume must be between 0 and 100")
	}
	c.volume.Store(int32(volume))
	return nil
}

func (c *Controls) Volume() int32 {
	if c == nil {
		return 100
	}
	return c.volume.Load()
}
func (c *Controls) Frames() uint64 {
	if c == nil {
		return 0
	}
	return c.frames.Load()
}

func (c *Controls) SetPaused(paused bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused == paused {
		return
	}
	c.paused = paused
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *Controls) wait(ctx context.Context) error {
	if c == nil {
		return ctx.Err()
	}
	for {
		c.mu.Lock()
		paused, changed := c.paused, c.changed
		c.mu.Unlock()
		if !paused {
			return ctx.Err()
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
