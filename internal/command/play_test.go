package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/media"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

type resolverFunc func(context.Context, string) ([]media.Track, error)

func (f resolverFunc) Resolve(ctx context.Context, query string) ([]media.Track, error) {
	return f(ctx, query)
}

type accessFixture struct {
	mu      sync.Mutex
	channel string
}

func (a *accessFixture) Access(context.Context, string, string) (session.Access, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return session.Access{Channel: a.channel}, nil
}
func (a *accessFixture) move(channel string) { a.mu.Lock(); a.channel = channel; a.mu.Unlock() }

type openFixture struct{ requests chan playback.Request }

func (o *openFixture) Open(ctx context.Context, r playback.Request) (playback.Stream, error) {
	select {
	case o.requests <- r:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &streamFixture{}, nil
}

type streamFixture struct{}

func (*streamFixture) Next(ctx context.Context, _ []byte) (int, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}
func (*streamFixture) Close() error { return nil }

type outputFixture struct{}

func (outputFixture) Send(context.Context, []byte) error { return nil }

func newFixture(t *testing.T, resolve resolverFunc, workers int) (*Player, *session.Session, *accessFixture, *openFixture) {
	t.Helper()
	access := &accessFixture{channel: "voice"}
	opener := &openFixture{requests: make(chan playback.Request, session.MaxQueue)}
	engine, err := playback.New(opener, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	target, err := session.New(context.Background(), "guild", engine, outputFixture{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(target.Close)
	if _, err := target.Apply(context.Background(), session.Operation{Action: session.Connect, Channel: "voice", Output: outputFixture{}}); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlayer(context.Background(), resolve, access, log.New(io.Discard), workers)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p, target, access, opener
}

func resolved() []media.Track {
	return []media.Track{{Provider: "test", MediaID: "track", Title: "Example"}}
}

func TestPlayCorrelationReachesAttempt(t *testing.T) {
	p, target, _, opener := newFixture(t, func(context.Context, string) ([]media.Track, error) { return resolved(), nil }, 1)
	admission, err := p.Play(context.Background(), target, "guild", "user", "query")
	if err != nil || len(admission.Tickets) != 1 {
		t.Fatalf("admission = %+v, %v", admission, err)
	}
	select {
	case request := <-opener.requests:
		if request.CommandID != admission.CommandID || request.ID == "" || request.AttemptID == "" {
			t.Fatalf("correlation lost: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("attempt did not start")
	}
}

func TestStopOrDisconnectSupersedesLookupAcrossReconnect(t *testing.T) {
	for _, action := range []session.Action{session.Stop, session.Disconnect} {
		t.Run(string(action), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			p, target, _, opener := newFixture(t, func(ctx context.Context, _ string) ([]media.Track, error) {
				close(started)
				select {
				case <-release:
					return resolved(), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}, 1)
			done := make(chan error, 1)
			go func() { _, err := p.Play(context.Background(), target, "guild", "user", "query"); done <- err }()
			<-started
			for _, op := range []session.Operation{{Action: action}, {Action: session.Connect, Channel: "voice", Output: outputFixture{}}} {
				if _, err := target.Apply(context.Background(), op); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			if err := <-done; !errors.Is(err, session.ErrSuperseded) {
				t.Fatalf("late lookup = %v", err)
			}
			if len(opener.requests) != 0 {
				t.Fatal("superseded lookup started playback")
			}
			snap, err := target.Apply(context.Background(), session.Operation{Action: session.Inspect})
			if err != nil || len(snap.Tracks) != 0 {
				t.Fatalf("queue changed: %+v %v", snap, err)
			}
		})
	}
}

func TestVoiceAccessRefreshedAfterLookup(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	p, target, access, _ := newFixture(t, func(ctx context.Context, _ string) ([]media.Track, error) {
		close(started)
		select {
		case <-release:
			return resolved(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, 1)
	done := make(chan error, 1)
	go func() { _, err := p.Play(context.Background(), target, "guild", "user", "query"); done <- err }()
	<-started
	access.move("another-channel")
	close(release)
	if err := <-done; !errors.Is(err, session.ErrVoiceAccess) {
		t.Fatalf("moved caller admission = %v", err)
	}
}

func TestWorkerSaturationAndCloseJoin(t *testing.T) {
	started := make(chan struct{}, 2)
	p, target, _, _ := newFixture(t, func(ctx context.Context, _ string) ([]media.Track, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}, 2)
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := p.Play(context.Background(), target, "guild", "user", "query"); done <- err }()
	}
	for range 2 {
		<-started
	}
	if _, err := p.Play(context.Background(), target, "guild", "user", "overflow"); !errors.Is(err, ErrBusy) {
		t.Fatalf("saturation = %v", err)
	}
	p.Close()
	p.Close()
	for range 2 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown = %v", err)
		}
	}
	if _, err := p.Play(context.Background(), target, "guild", "user", "after"); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed admission = %v", err)
	}
}

func TestRejectedVoiceDoesNotResolve(t *testing.T) {
	p, target, access, _ := newFixture(t, func(context.Context, string) ([]media.Track, error) {
		t.Error("unauthorized metadata lookup")
		return nil, nil
	}, 1)
	access.move("")
	if _, err := p.Play(context.Background(), target, "guild", "user", "query"); !errors.Is(err, session.ErrVoiceAccess) {
		t.Fatalf("authorization = %v", err)
	}
}

func TestSafeProviderFailureHasOneCommandTerminal(t *testing.T) {
	secret := "signed-token-secret"
	p, target, _, _ := newFixture(t, func(context.Context, string) ([]media.Track, error) {
		return nil, failure.Wrap("metadata", failure.Authentication, errors.New(secret))
	}, 1)
	var out bytes.Buffer
	p.logger = log.New(&out)
	_, err := p.Play(context.Background(), target, "guild", "user", "query")
	if err == nil {
		t.Fatal("provider failure accepted")
	}
	if text := out.String(); strings.Count(text, "command.terminal") != 1 || strings.Contains(text, secret) || !strings.Contains(text, "authentication") {
		t.Fatalf("unsafe or incomplete log: %s", text)
	}
}
