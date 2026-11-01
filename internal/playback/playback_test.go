package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/failure"
)

type openFunc func(context.Context, Request) (Stream, error)

func TestTerminalLogsOnlyPublicTrackIdentifier(t *testing.T) {
	for _, id := range []string{"8fM1xnLTApg", "", "https://signed.invalid/?token=private", "@everyone", strings.Repeat("x", 33)} {
		var logs bytes.Buffer
		logger := log.NewWithOptions(&logs, log.Options{Formatter: log.JSONFormatter})
		engine, err := New(openFunc(func(context.Context, Request) (Stream, error) {
			return &testStream{frames: 1, end: io.EOF}, nil
		}), logger)
		if err != nil {
			t.Fatal(err)
		}
		result := engine.Run(context.Background(), Request{TrackID: id, MediaID: "https://signed.invalid/?token=private"}, sendFunc(func(context.Context, []byte) error { return nil }))
		var event map[string]any
		if err := json.Unmarshal(logs.Bytes(), &event); err != nil || result.Outcome != Completed {
			t.Fatal("expected one completed JSON terminal event", err)
		}
		if id == "8fM1xnLTApg" {
			if event["track_id"] != id {
				t.Fatal("missing public identifier")
			}
		} else if _, ok := event["track_id"]; ok {
			t.Fatal("unsafe identifier logged")
		}
		if strings.Contains(logs.String(), "signed.invalid") || strings.Contains(logs.String(), "@everyone") {
			t.Fatal("private or untrusted text logged")
		}
	}
}

func (f openFunc) Open(ctx context.Context, r Request) (Stream, error) { return f(ctx, r) }

type sendFunc func(context.Context, []byte) error

func (f sendFunc) Send(ctx context.Context, b []byte) error { return f(ctx, b) }

type testStream struct {
	frames       int
	end          error
	closeErr     error
	closed       int
	closeStarted chan struct{}
	releaseClose chan struct{}
}

func (s *testStream) Next(_ context.Context, b []byte) (int, error) {
	if s.frames == 0 {
		return 0, s.end
	}
	s.frames--
	b[0] = 1
	return 1, nil
}
func (s *testStream) Close() error {
	s.closed++
	if s.closeStarted != nil {
		close(s.closeStarted)
	}
	if s.releaseClose != nil {
		<-s.releaseClose
	}
	return s.closeErr
}

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
		{"source_finalization", 3, io.EOF, nil, failure.Wrap("acquire", failure.Source, cause), SourceFailure, failure.Source},
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

func TestCancellationDoesNotEraseEstablishedTransportFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	s := &testStream{frames: 1, closeStarted: make(chan struct{}), releaseClose: make(chan struct{})}
	e, err := New(openFunc(func(context.Context, Request) (Stream, error) { return s, nil }), log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Result, 1)
	go func() {
		done <- e.Run(ctx, Request{}, sendFunc(func(context.Context, []byte) error { return errors.New("transport lost") }))
	}()
	<-s.closeStarted
	cancel(Cancellation{Outcome: Stopped})
	close(s.releaseClose)
	result := <-done
	if result.Outcome != TransportFailure || result.Failure == nil || result.Failure.Category != failure.Transport {
		t.Fatalf("cancellation replaced terminal failure: %+v", result)
	}
}

func TestCancellationDoesNotEraseCleanupFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	s := &testStream{frames: 1, closeErr: errors.New("cleanup failed"), closeStarted: make(chan struct{}), releaseClose: make(chan struct{})}
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
	<-s.closeStarted
	close(s.releaseClose)
	result := <-done
	if result.Outcome != EncodeFailure || result.Failure == nil || result.Failure.Category != failure.Cleanup {
		t.Fatalf("cancellation replaced cleanup failure: %+v", result)
	}
}

func TestCancellationAfterEOFDoesNotReplaceCompletion(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	s := &testStream{frames: 1, end: io.EOF, closeStarted: make(chan struct{}), releaseClose: make(chan struct{})}
	e, err := New(openFunc(func(context.Context, Request) (Stream, error) { return s, nil }), log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Result, 1)
	go func() { done <- e.Run(ctx, Request{}, sendFunc(func(context.Context, []byte) error { return nil })) }()
	<-s.closeStarted
	cancel(Cancellation{Outcome: Stopped})
	close(s.releaseClose)
	if result := <-done; result.Outcome != Completed || result.Failure != nil {
		t.Fatalf("cancellation replaced natural completion: %+v", result)
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
