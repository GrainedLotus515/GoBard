package session

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

type recoverySource struct {
	opened    chan playback.Request
	failEvery bool
	fail      *failure.Error
	empty     bool
	calls     int
}

func (s *recoverySource) Open(_ context.Context, request playback.Request) (playback.Stream, error) {
	s.calls++
	s.opened <- request
	var err error
	if s.calls == 1 || s.failEvery {
		err = s.fail
	}
	return &recoveryStream{err: err, empty: s.empty}, nil
}

type recoveryStream struct {
	sent, empty bool
	err         error
}

func (s *recoveryStream) Next(_ context.Context, packet []byte) (int, error) {
	if !s.sent && !s.empty {
		s.sent = true
		packet[0] = 1
		return 1, nil
	}
	if s.err != nil {
		return 0, s.err
	}
	return 0, io.EOF
}
func (*recoveryStream) Close() error { return nil }

func TestSourceResumeUsesDeliveredPositionAndOneRequest(t *testing.T) {
	source := &recoverySource{opened: make(chan playback.Request, 3), fail: &failure.Error{Stage: "acquire", Category: failure.Source, Reason: failure.ConnectionReset, Cause: errors.New("reset")}}
	result, requests := runSourceRecovery(t, source, Track{Provider: "fixture", MediaID: "audio", TrackID: "public-track", Duration: time.Minute})
	if result.Outcome != playback.Completed || len(requests) != 2 {
		t.Fatalf("result %+v, attempts %d", result, len(requests))
	}
	first, next := requests[0], requests[1]
	if first.ID != next.ID || first.AttemptID == next.AttemptID || next.Offset != playback.FrameDuration {
		t.Fatalf("resume lost identity/position: %+v %+v", first, next)
	}
	if first.TrackID != "public-track" || next.TrackID != first.TrackID || result.Request.TrackID != first.TrackID {
		t.Fatal("resume lost the diagnostic track identifier")
	}
}

func TestSourceResumeBudgetAndTerminalExclusions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		category    failure.Category
		reason      failure.Reason
		empty, live bool
		duration    time.Duration
		attempts    int
	}{
		{"reset", failure.Source, failure.ConnectionReset, false, false, time.Minute, 2},
		{"timeout", failure.Source, failure.NetworkTimeout, false, false, time.Minute, 2},
		{"unknown", failure.Source, "", false, false, time.Minute, 1},
		{"forbidden", failure.Source, failure.HTTPForbidden, false, false, time.Minute, 1},
		{"format unavailable", failure.Source, failure.FormatUnavailable, false, false, time.Minute, 1},
		{"auth", failure.Authentication, failure.ConnectionReset, false, false, time.Minute, 1},
		{"empty", failure.Source, failure.ConnectionReset, true, false, time.Minute, 1},
		{"live", failure.Source, failure.ConnectionReset, false, true, time.Minute, 1},
		{"unknown duration", failure.Source, failure.ConnectionReset, false, false, 0, 1},
		{"end reached", failure.Source, failure.ConnectionReset, false, false, playback.FrameDuration, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &recoverySource{opened: make(chan playback.Request, 3), failEvery: true, empty: tc.empty, fail: &failure.Error{Stage: "acquire", Category: tc.category, Reason: tc.reason, Cause: errors.New("failure")}}
			result, requests := runSourceRecovery(t, source, Track{Provider: "fixture", MediaID: "audio", Duration: tc.duration, Live: tc.live})
			if result.Outcome != playback.SourceFailure || len(requests) != tc.attempts {
				t.Fatalf("outcome %s attempts %d", result.Outcome, len(requests))
			}
		})
	}
}

func TestSourceResumeCanBeStoppedOrShutDown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "shutdown"}[shutdown], func(t *testing.T) {
			source := &recoverySource{opened: make(chan playback.Request, 3), failEvery: true, fail: &failure.Error{Stage: "acquire", Category: failure.Source, Reason: failure.ConnectionReset}}
			engine, err := playback.New(source, log.New(io.Discard))
			if err != nil {
				t.Fatal(err)
			}
			resumed := make(chan struct{})
			sends := 0 // Only the joined, sequential attempts use this counter.
			s, err := New(context.Background(), "cancel-resume", engine, outputFunc(func(ctx context.Context, _ []byte) error {
				sends++
				if sends == 1 {
					return nil
				}
				close(resumed)
				<-ctx.Done()
				return ctx.Err()
			}), 4)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			tickets, err := s.Add(context.Background(), []Track{{Provider: "fixture", MediaID: "audio", Duration: time.Minute}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			select {
			case <-resumed:
			case <-ctx.Done():
				t.Fatal("resume did not start")
			}
			want := playback.Stopped
			if shutdown {
				want = playback.Shutdown
				s.Close()
			} else if err := s.Control(ctx, playback.Stopped); err != nil {
				t.Fatal(err)
			}
			result, err := tickets[0].Wait(ctx)
			if err != nil || result.Outcome != want {
				t.Fatalf("terminal %+v, error %v", result, err)
			}
			s.Close()
			if source.calls != 2 {
				t.Fatalf("cancellation started another attempt: %d", source.calls)
			}
		})
	}
}

func runSourceRecovery(t *testing.T, source *recoverySource, track Track) (playback.Result, []playback.Request) {
	t.Helper()
	engine, err := playback.New(source, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), "recovery", engine, outputFunc(func(context.Context, []byte) error { return nil }), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tickets, err := s.Add(context.Background(), []Track{track})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := tickets[0].Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	requests := make([]playback.Request, 0, len(source.opened))
	for len(source.opened) > 0 {
		requests = append(requests, <-source.opened)
	}
	return result, requests
}
