// Package command owns bounded, cancellable command work above guild sessions.
package command

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/media"
	"github.com/GrainedLotus515/gobard/internal/session"
)

var ErrBusy = errors.New("all metadata command workers are busy; try again shortly")
var ErrClosed = errors.New("command service closed")

// Resolver and AccessSource are consumed capabilities, not SDK facades. Their
// calls are synchronous and must honor context cancellation. Typed nil values
// are prohibited by contract, as with other interface dependencies.
type Resolver interface {
	Resolve(context.Context, string) ([]media.Track, error)
}
type AccessSource interface {
	Access(context.Context, string, string) (session.Access, error)
}

// Admission acknowledges queue acceptance, not playback or human audibility.
// One command can admit several logical requests, each with its own ticket.
type Admission struct {
	CommandID string
	Tickets   []*session.Ticket
}

// Player owns at most workers synchronous resolution calls and no pending
// queue. The caller owns each executing goroutine; Close cancels and joins all
// admitted calls before the source/session dependencies may be closed.
type Player struct {
	ctx      context.Context
	cancel   context.CancelFunc
	resolver Resolver
	access   AccessSource
	logger   *log.Logger
	slots    chan struct{}
	mu       sync.Mutex
	stopping bool
	work     sync.WaitGroup
	once     sync.Once
}

func NewPlayer(parent context.Context, resolver Resolver, access AccessSource, logger *log.Logger, workers int) (*Player, error) {
	if parent == nil || resolver == nil || access == nil || logger == nil || workers < 1 || workers > 16 {
		return nil, errors.New("invalid command dependencies or worker budget")
	}
	// #nosec G118 -- Close cancels and joins all admitted operations.
	ctx, cancel := context.WithCancel(parent)
	return &Player{ctx: ctx, cancel: cancel, resolver: resolver, access: access, logger: logger, slots: make(chan struct{}, workers)}, nil
}

// Play requires an already connected target supplied by the lifecycle adapter.
// It refreshes permissions after metadata lookup, then asks the session owner
// to atomically validate voice access and the stop/disconnect generation.
func (p *Player) Play(caller context.Context, target *session.Session, guild, user, query string) (admission Admission, err error) {
	if target == nil || guild == "" || len(guild) > 32 || user == "" || len(user) > 32 || len(query) == 0 || len(query) > 512 || !utf8.ValidString(query) {
		return admission, errors.New("invalid play command")
	}
	if err := caller.Err(); err != nil {
		return admission, err
	}
	p.mu.Lock()
	if p.stopping || p.ctx.Err() != nil {
		p.mu.Unlock()
		return admission, ErrClosed
	}
	select {
	case p.slots <- struct{}{}:
	default:
		p.mu.Unlock()
		return admission, ErrBusy
	}
	p.work.Add(1)
	p.mu.Unlock()
	defer p.work.Done()
	defer func() { <-p.slots }()

	ctx, cancel := context.WithCancel(p.ctx)
	finished := make(chan struct{})
	stop := context.AfterFunc(caller, func() { cancel(); close(finished) })
	defer func() {
		cancel()
		if !stop() {
			<-finished
		}
	}()
	admission.CommandID = rand.Text()
	defer target.EndResolution(admission.CommandID)
	started := time.Now()
	defer func() { p.terminal(admission, err, time.Since(started)) }()
	access, err := p.access.Access(ctx, guild, user)
	if err != nil {
		return admission, failure.Wrap("voice_access", failure.Transport, err)
	}
	snapshot, err := target.BeginResolution(ctx, access, admission.CommandID)
	if err != nil {
		return admission, err
	}
	if !snapshot.Connected || snapshot.Channel == "" || access.Channel != snapshot.Channel {
		return admission, session.ErrVoiceAccess
	}
	tracks, err := p.resolver.Resolve(ctx, strings.Clone(query))
	if err != nil {
		return admission, failure.Wrap("metadata", failure.Source, err)
	}
	if len(tracks) == 0 || len(tracks) > session.MaxQueue {
		return admission, session.ErrFull
	}
	for i := range tracks {
		tracks[i].RequestedBy = strings.Clone(user)
	}
	access, err = p.access.Access(ctx, guild, user)
	if err != nil {
		return admission, failure.Wrap("voice_access", failure.Transport, err)
	}
	admission.Tickets, err = target.AddResolved(ctx, access, snapshot.Generation, admission.CommandID, tracks)
	return admission, err
}

func (p *Player) terminal(a Admission, err error, elapsed time.Duration) {
	fields := []any{"event", "command.terminal", "command_id", a.CommandID, "command", "play", "elapsed_ms", elapsed.Milliseconds(), "accepted_tracks", len(a.Tickets), "recovery", "none"}
	if err == nil {
		p.logger.Info("Queue admission completed", append(fields, "outcome", "accepted")...)
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrClosed) {
		p.logger.Info("Command canceled", append(fields, "outcome", "canceled")...)
		return
	}
	var f *failure.Error
	if errors.As(err, &f) {
		// Preserve the provider's more specific diagnosis without logging causes.
		for {
			var inner *failure.Error
			if !errors.As(f.Cause, &inner) {
				break
			}
			f = inner
		}
		p.logger.Error("Command failed", append(fields, "outcome", "failed", "stage", f.Stage, "category", f.Category, "reason", f.Reason, "fingerprint", f.Fingerprint, "exit_code", f.ExitCode)...)
		return
	}
	reason := "invalid_operation"
	switch {
	case errors.Is(err, session.ErrSuperseded):
		reason = "superseded"
	case errors.Is(err, session.ErrVoiceAccess):
		reason = "voice_access"
	case errors.Is(err, session.ErrFull):
		reason = "queue_full"
	case errors.Is(err, session.ErrBusy):
		reason = "session_busy"
	}
	p.logger.Info("Command rejected", append(fields, "outcome", "rejected", "reason", reason)...)
}

func (p *Player) Close() {
	p.once.Do(func() {
		p.mu.Lock()
		p.stopping = true
		p.cancel()
		p.mu.Unlock()
		p.work.Wait()
	})
}
