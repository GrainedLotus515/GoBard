package discordvoice

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

const writeTimeout = 250 * time.Millisecond
const maxVoicePacket = 1400

// PacketWriter is the consumed SDK capability, not a mirror of its UDP API.
// SetWriteDeadline and Close must be safe concurrently with Write.
type PacketWriter interface {
	Write([]byte) (int, error)
	SetWriteDeadline(time.Time) error
	Close() error
}

// Output owns pacing and synchronous packet delivery on an already-connected
// Discord UDP transport. The session must close it before closing its voice
// connection. It never retains caller packets or starts a sender goroutine.
type Output struct {
	conn      PacketWriter
	gate      chan struct{}
	done      chan struct{}
	timer     *time.Timer
	next      time.Time
	closeOnce sync.Once
	closeErr  error
}

// Output adapts the established SDK voice connection to the v2 playback core.
// A connection has exactly one output owner.
func (c *Connection) Output() (*Output, error) {
	if c == nil || c.conn == nil {
		return nil, errors.New("voice connection required")
	}
	return NewOutput(c.conn.UDP())
}

// NewOutput takes ownership of a connected packet writer until Close. This
// boundary also permits offline workloads to exercise the actual pacing path.
// Typed nil writers are prohibited; an interface nil check cannot detect them.
func NewOutput(conn PacketWriter) (*Output, error) {
	if conn == nil {
		return nil, errors.New("voice UDP transport required")
	}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	return &Output{conn: conn, gate: make(chan struct{}, 1), done: make(chan struct{}), timer: timer}, nil
}

// Send paces a single 20 ms packet. Cancellation before Write is immediate;
// an already-running SDK Write has a maximum 250 ms network deadline. Encryption
// itself must be cooperative; the adapter cannot preempt native library code.
func (o *Output) Send(ctx context.Context, packet []byte) error {
	if len(packet) == 0 || len(packet) > maxVoicePacket {
		return failure.Wrap("output", failure.InvalidFrame, errors.New("invalid Discord packet size"))
	}
	select {
	case o.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-o.done:
		return io.ErrClosedPipe
	}
	defer func() { <-o.gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-o.done:
		return io.ErrClosedPipe
	default:
	}
	if delay := time.Until(o.next); delay > 0 {
		o.timer.Reset(delay)
		select {
		case <-o.timer.C:
		case <-ctx.Done():
			o.timer.Stop()
			return ctx.Err()
		case <-o.done:
			o.timer.Stop()
			return io.ErrClosedPipe
		}
	}
	deadline := time.Now().Add(writeTimeout)
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	if err := o.conn.SetWriteDeadline(deadline); err != nil {
		return failure.Wrap("output", failure.Transport, err)
	}
	sentAt := time.Now()
	n, err := o.conn.Write(packet)
	o.next = nextPacketDeadline(o.next, sentAt)
	if err != nil {
		return failure.Wrap("output", failure.Transport, err)
	}
	if n != len(packet) {
		return failure.Wrap("output", failure.Transport, io.ErrShortWrite)
	}
	return nil
}

// Keep the media clock's phase through ordinary timer jitter. After a long
// stall reset the phase, rather than emitting a burst of overdue packets.
func nextPacketDeadline(previous, sentAt time.Time) time.Time {
	next := previous.Add(playback.FrameDuration)
	if previous.IsZero() || next.Before(sentAt.Add(playback.FrameDuration/2)) {
		return sentAt.Add(playback.FrameDuration)
	}
	return next
}

// Close unblocks writes, then acquires the send gate as a join barrier. A
// compliant transport's Close interrupts any pending Write. Repeated Close is
// safe and returns the same error. No lock is held across the transport close.
func (o *Output) Close() error {
	o.closeOnce.Do(func() {
		close(o.done)
		o.closeErr = o.conn.Close()
		o.gate <- struct{}{}
		o.timer.Stop()
		<-o.gate
	})
	return o.closeErr
}
