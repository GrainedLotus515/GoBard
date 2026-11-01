package session

import "errors"

var ErrStreamLimit = errors.New("active stream memory budget reached")

// The coordinator holds one global slot before queue admission/start, until
// its attempt is joined. No waiter goroutine or unbounded admission queue.
func (c *coordinator) acquireStream() bool {
	if c.streamSlots == nil || c.streamHeld {
		return true
	}
	select {
	case c.streamSlots <- struct{}{}:
		c.streamHeld = true
		return true
	default:
		return false
	}
}
func (c *coordinator) releaseStream() {
	if c.streamHeld {
		<-c.streamSlots
		c.streamHeld = false
	}
}
