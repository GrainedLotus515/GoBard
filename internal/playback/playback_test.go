package playback

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/failure"
)

type openFunc func(context.Context, Request) (Stream, error)

func (f openFunc) Open(ctx context.Context, r Request) (Stream, error) { return f(ctx, r) }

type sendFunc func(context.Context, []byte) error

func (f sendFunc) Send(ctx context.Context, b []byte) error { return f(ctx, b) }

type testStream struct {
	frames   int
	end      error
	closeErr error
	closed   int
}

func (s *testStream) Next(_ context.Context, b []byte) (int, error) {
	if s.frames == 0 {
		return 0, s.end
	}
	s.frames--
	b[0] = 1
	return 1, nil
}
func (s *testStream) Close() error { s.closed++; return s.closeErr }

func TestTerminalOutcomes(t *testing.T) {
	cause := errors.New("secret=https://signed.invalid/?token=do-not-log")
	for _, tc := range []struct {
		name                string
		frames              int
		end, send, closeErr error
		outcome             Outcome
		category            failure.Category
	}{
		{"completion", 3, io.EOF, nil, nil, Completed, ""},
		{"empty", 0, io.EOF, nil, nil, EncodeFailure, failure.ZeroFrames},
		{"codec", 1, cause, nil, nil, EncodeFailure, failure.Codec},
		{"transport", 3, io.EOF, cause, nil, TransportFailure, failure.Transport},
		{"cleanup", 3, io.EOF, nil, cause, EncodeFailure, failure.Cleanup},
		{"auth", 0, failure.Wrap("extract", failure.Authentication, cause), nil, nil, SourceFailure, failure.Authentication},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			s := &testStream{frames: tc.frames, end: tc.end, closeErr: tc.closeErr}
			e, err := New(openFunc(func(context.Context, Request) (Stream, error) { return s, nil }), log.New(&logs))
			if err != nil {
				t.Fatal(err)
			}
			r := e.Run(context.Background(), Request{ID: "r", AttemptID: "a"}, sendFunc(func(context.Context, []byte) error { return tc.send }))
			if r.Outcome != tc.outcome || s.closed != 1 {
				t.Fatalf("result=%+v closed=%d", r, s.closed)
			}
			if tc.category != "" && (r.Failure == nil || r.Failure.Category != tc.category) {
				t.Fatalf("failure=%v", r.Failure)
			}
			if tc.outcome == Completed && r.Frames != 3 {
				t.Fatal(r)
			}
			if strings.Count(logs.String(), "playback.terminal") != 1 || strings.Contains(logs.String(), "do-not-log") {
				t.Fatal(logs.String())
			}
		})
	}
}

func TestCancellationUnblocksStalledOutput(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	s := &testStream{frames: 10, end: io.EOF}
	e, err := New(openFunc(func(context.Context, Request) (Stream, error) { return s, nil }), log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	done := make(chan Result, 1)
	go func() {
		done <- e.Run(ctx, Request{}, sendFunc(func(ctx context.Context, _ []byte) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}))
	}()
	<-entered
	cancel(Cancellation{Outcome: Skipped})
	r := <-done
	if r.Outcome != Skipped || r.Frames != 0 || r.Failure != nil || s.closed != 1 {
		t.Fatalf("%+v closed=%d", r, s.closed)
	}
}

func BenchmarkAttempt(b *testing.B) {
	e, err := New(openFunc(func(context.Context, Request) (Stream, error) { return &testStream{frames: 1000, end: io.EOF}, nil }), log.New(io.Discard))
	if err != nil {
		b.Fatal(err)
	}
	output := sendFunc(func(context.Context, []byte) error { return nil })
	b.ReportAllocs()
	for b.Loop() {
		if r := e.Run(context.Background(), Request{}, output); r.Frames != 1000 {
			b.Fatal(r)
		}
	}
}
