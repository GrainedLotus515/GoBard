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

// packetConn is the consumed SDK capability, not a mirror of its UDP API.
// SetWriteDeadline and Close must be safe concurrently with Write.
type packetConn interface {
	Write([]byte) (int, error)
	SetWriteDeadline(time.Time) error
	Close() error
}

// Output owns pacing and synchronous packet delivery on an already-connected
// Discord UDP transport. The session must close it before closing its voice
// connection. It never retains caller packets or starts a sender goroutine.
type Output struct {
	conn      packetConn
	gate      chan struct{}
	done      chan struct{}
	timer     *time.Timer
	next      time.Time
	closeOnce sync.Once
	closeErr  error
}

// Output adapts the established SDK voice connection to the v2 playback core.
// Do not use SendOpusFrame concurrently with this output on the same connection.
func (c *Connection) Output() (*Output, error) {
	if c == nil || c.conn == nil {
		return nil, errors.New("voice connection required")
	}
	return newOutput(c.conn.UDP())
}

func newOutput(conn packetConn) (*Output, error) {
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
	o.next = sentAt.Add(playback.FrameDuration)
	if err != nil {
		return failure.Wrap("output", failure.Transport, err)
	}
	if n != len(packet) {
		return failure.Wrap("output", failure.Transport, io.ErrShortWrite)
	}
	return nil
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
