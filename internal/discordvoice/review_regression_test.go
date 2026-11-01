package discordvoice

import (
	"context"
	"testing"
	"time"
)

type reviewDelayedWriter struct{ accepted []time.Time }

func (*reviewDelayedWriter) SetWriteDeadline(time.Time) error { return nil }
func (*reviewDelayedWriter) Close() error                     { return nil }
func (w *reviewDelayedWriter) Write(b []byte) (int, error) {
	if len(w.accepted) == 0 {
		time.Sleep(80 * time.Millisecond)
	}
	w.accepted = append(w.accepted, time.Now())
	return len(b), nil
}

func TestReviewNoBurstAfterSuccessfulSlowWrite(t *testing.T) {
	w := &reviewDelayedWriter{}
	o, err := NewOutput(w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	for range 2 {
		if err := o.Send(context.Background(), []byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	if gap := w.accepted[1].Sub(w.accepted[0]); gap < 9*time.Millisecond {
		t.Fatalf("successful slow Write caused packet burst: acceptance gap=%v", gap)
	}
}

func TestOutputPauseRebasesAndCanceledWaitDoesNotWrite(t *testing.T) {
	w := &reviewDelayedWriter{}
	o, err := NewOutput(w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	if err := o.Send(context.Background(), []byte{1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.Send(ctx, []byte{1}); err != context.Canceled {
		t.Fatal(err)
	}
	if len(w.accepted) != 1 {
		t.Fatal("canceled wait wrote a packet")
	}
	time.Sleep(80 * time.Millisecond)
	for range 2 {
		if err := o.Send(context.Background(), []byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	if gap := w.accepted[2].Sub(w.accepted[1]); gap < 9*time.Millisecond {
		t.Fatalf("pause caused burst: %v", gap)
	}
}
