package discordvoice

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type packetFake struct {
	mu       sync.Mutex
	packets  [][]byte
	writes   []time.Time
	deadline time.Time
	closed   chan struct{}
	entered  chan struct{}
	once     sync.Once
	stall    bool
	short    bool
}

func (f *packetFake) SetWriteDeadline(t time.Time) error {
	f.mu.Lock()
	f.deadline = t
	f.mu.Unlock()
	return nil
}
func (f *packetFake) Write(b []byte) (int, error) {
	if f.stall {
		f.once.Do(func() { close(f.entered) })
		f.mu.Lock()
		deadline := f.deadline
		f.mu.Unlock()
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-f.closed:
			return 0, io.ErrClosedPipe
		case <-timer.C:
			return 0, context.DeadlineExceeded
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.packets = append(f.packets, append([]byte(nil), b...))
	f.writes = append(f.writes, time.Now())
	if f.short {
		return 0, nil
	}
	return len(b), nil
}
func (f *packetFake) Close() error { close(f.closed); return nil }

func TestOutputPacketOwnershipAndPacing(t *testing.T) {
	f := &packetFake{closed: make(chan struct{})}
	o, err := newOutput(f)
	if err != nil {
		t.Fatal(err)
	}
	packet := []byte{1, 2, 3}
	for range 3 {
		if err := o.Send(context.Background(), packet); err != nil {
			t.Fatal(err)
		}
	}
	packet[0] = 9
	if f.packets[0][0] != 1 {
		t.Fatal("retained caller packet")
	}
	for i := 1; i < len(f.writes); i++ {
		if f.writes[i].Sub(f.writes[i-1]) < 19*time.Millisecond {
			t.Fatal("packets sent in a burst")
		}
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.Send(context.Background(), packet); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestOutputCloseJoinsStalledWrite(t *testing.T) {
	f := &packetFake{closed: make(chan struct{}), entered: make(chan struct{}), stall: true}
	o, err := newOutput(f)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- o.Send(context.Background(), []byte{1}) }()
	<-f.entered
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestOutputDeadlineCancellationAndShortWrite(t *testing.T) {
	f := &packetFake{closed: make(chan struct{}), entered: make(chan struct{}), stall: true}
	o, err := newOutput(f)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = o.Send(ctx, []byte{1})
	if err == nil || time.Since(start) > time.Second {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	f = &packetFake{closed: make(chan struct{}), short: true}
	o, err = newOutput(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Send(context.Background(), []byte{1}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
}
