// Package session serializes guild queue policy and joins playback attempts.
package session

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"sync"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

var (
	ErrBusy   = errors.New("session command mailbox full")
	ErrFull   = errors.New("session queue full")
	ErrClosed = errors.New("session closed")
)

const MailboxCapacity = 32
const MaxQueue = 500

// Ticket is a single-assignment request result. A canceled Wait does not roll
// back admission. Loop/seek/disconnect may create multiple attempts before the
// logical request ends; each attempt separately emits its terminal event.
type Ticket struct {
	done    chan struct{}
	request playback.Request
	result  playback.Result
}

func (t *Ticket) Wait(ctx context.Context) (playback.Result, error) {
	select {
	case <-t.done:
		return t.result, nil
	case <-ctx.Done():
		return playback.Result{}, ctx.Err()
	}
}

type command struct {
	ctx       context.Context
	tracks    []Track
	operation Operation
	reply     chan response
}
type response struct {
	tickets  []*Ticket
	snapshot Snapshot
	err      error
}

// Session owns one coordinator and at most one attempt goroutine. Dependencies
// must honor cancellation. Close cancels and joins the coordinator and attempt.
type Session struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	commands  chan command
	closeOnce sync.Once
}

func New(parent context.Context, id string, engine *playback.Engine, output playback.Output, capacity int) (*Session, error) {
	if parent == nil || id == "" || len(id) > 64 || engine == nil || output == nil || capacity < 1 || capacity > MaxQueue {
		return nil, errors.New("invalid session dependencies or capacity")
	}
	// #nosec G118 -- Session.Close owns cancellation; parent also terminates run.
	ctx, cancel := context.WithCancel(parent)
	s := &Session{ctx: ctx, cancel: cancel, done: make(chan struct{}), commands: make(chan command, MailboxCapacity)}
	c := &coordinator{session: s, id: id + "/" + rand.Text(), engine: engine, output: output, connected: true,
		queue:   queue{entries: make([]entry, 0, capacity), index: -1, capacity: capacity},
		results: make(chan playback.Result, 1), volume: 100, duckTarget: 70, speakers: make(map[string]bool)}
	go c.run()
	return s, nil
}

// Enqueue is the metadata-free operation used by test-media integrations.
func (s *Session) Enqueue(ctx context.Context, request playback.Request) (*Ticket, error) {
	if request.Offset != 0 {
		return nil, errors.New("initial offset requires a seek operation")
	}
	tickets, err := s.Add(ctx, []Track{{Provider: request.Provider, MediaID: request.MediaID}})
	if err != nil {
		return nil, err
	}
	return tickets[0], nil
}

// Add admits all tracks atomically or rejects the entire batch. Caller strings
// are bounded and the slice is copied before crossing the ownership boundary.
func (s *Session) Add(ctx context.Context, tracks []Track) ([]*Ticket, error) {
	if len(tracks) == 0 || len(tracks) > MaxQueue {
		return nil, ErrFull
	}
	for _, track := range tracks {
		if err := track.validate(); err != nil {
			return nil, err
		}
	}
	owned := make([]Track, len(tracks))
	for i, track := range tracks {
		owned[i] = track.clone()
	}
	r := s.submit(command{ctx: ctx, tracks: owned})
	return r.tickets, r.err
}
func (s *Session) Apply(ctx context.Context, operation Operation) (Snapshot, error) {
	r := s.submit(command{ctx: ctx, operation: operation})
	return r.snapshot, r.err
}
func (s *Session) Control(ctx context.Context, reason playback.Outcome) error {
	var action Action
	switch reason {
	case playback.Skipped:
		action = Skip
	case playback.Stopped:
		action = Stop
	default:
		return errors.New("invalid session control")
	}
	_, err := s.Apply(ctx, Operation{Action: action})
	return err
}
func (s *Session) submit(c command) response {
	if err := c.ctx.Err(); err != nil {
		return response{err: err}
	}
	if s.ctx.Err() != nil {
		return response{err: ErrClosed}
	}
	c.reply = make(chan response, 1)
	select {
	case s.commands <- c:
	default:
		return response{err: ErrBusy}
	}
	// The owner checks cancellation before admission. After acceptance, return
	// its definitive acknowledgement, never an ambiguous timeout/rollback claim.
	select {
	case r := <-c.reply:
		return r
	case <-s.done:
		select {
		case r := <-c.reply:
			return r
		default:
			return response{err: ErrClosed}
		}
	}
}
func (s *Session) Close() { s.closeOnce.Do(s.cancel); <-s.done }

type coordinator struct {
	session               *Session
	id                    string
	sequence              uint64
	engine                *playback.Engine
	output                playback.Output
	connected             bool
	queue                 queue
	results               chan playback.Result
	active                *Ticket
	cancel                context.CancelCauseFunc
	controls              *playback.Controls
	pending               playback.Outcome
	stopAfter             bool
	paused, loop, ducking bool
	volume, duckTarget    int
	speakers              map[string]bool
	last                  playback.Result
}

func (c *coordinator) run() {
	defer close(c.session.done)
	for {
		if c.active == nil && c.connected && c.queue.current() != nil && c.session.ctx.Err() == nil {
			c.start()
		}
		select {
		case <-c.session.ctx.Done():
			if c.active != nil {
				c.cancel(playback.Cancellation{Outcome: playback.Shutdown})
				c.last = <-c.results
			}
			c.settleAll(playback.Shutdown)
			return
		case result := <-c.results:
			c.complete(result)
		case cmd := <-c.session.commands:
			// Observe already-published completion before applying the next control.
			select {
			case result := <-c.results:
				c.complete(result)
			default:
			}
			if err := cmd.ctx.Err(); err != nil {
				cmd.reply <- response{err: err}
				continue
			}
			if c.session.ctx.Err() != nil {
				cmd.reply <- response{err: ErrClosed}
				continue
			}
			if len(cmd.tracks) > 0 {
				cmd.reply <- c.add(cmd.tracks)
				continue
			}
			err := c.apply(cmd.operation)
			cmd.reply <- response{snapshot: c.snapshot(), err: err}
		}
	}
}

func (c *coordinator) add(tracks []Track) response {
	if c.stopAfter {
		return response{err: ErrBusy}
	}
	if len(c.queue.entries)+len(tracks) > c.queue.capacity {
		return response{err: ErrFull}
	}
	tickets := make([]*Ticket, 0, len(tracks))
	for _, track := range tracks {
		c.sequence++
		request := playback.Request{ID: c.id + "/" + strconv.FormatUint(c.sequence, 10), Provider: track.Provider, MediaID: track.MediaID}
		ticket := &Ticket{done: make(chan struct{}), request: request}
		c.queue.entries = append(c.queue.entries, entry{track: track, ticket: ticket})
		tickets = append(tickets, ticket)
	}
	if c.queue.index < 0 {
		c.queue.index = 0
	}
	return response{tickets: tickets}
}

func (c *coordinator) start() {
	e := c.queue.current()
	e.attempts++
	request := e.ticket.request
	request.AttemptID = request.ID + "/" + strconv.FormatUint(e.attempts, 10)
	// Values are validated by the owner before being stored.
	controls, err := playback.NewControls(c.effectiveVolume())
	if err != nil {
		// All settings enter through validated operations; failure is an
		// impossible internal state, not an external runtime failure.
		panic("session volume invariant violated")
	}
	controls.SetPaused(c.paused)
	request.Controls = controls
	c.controls = controls
	c.active = e.ticket
	ctx, cancel := context.WithCancelCause(c.session.ctx)
	c.cancel = cancel
	output := c.output
	go func() {
		defer cancel(nil)
		c.results <- c.engine.Run(ctx, request, output)
	}()
}

func (c *coordinator) complete(result playback.Result) {
	c.cancel(nil)
	c.active = nil
	c.controls = nil
	c.last = result
	if c.stopAfter {
		c.stopAfter = false
		c.pending = ""
		c.settleAll(playback.Stopped)
		c.queue.reset()
		c.paused = false
		return
	}
	e := c.queue.current()
	reason := result.Outcome
	if c.pending != "" {
		reason = c.pending
		c.pending = ""
	}
	switch reason {
	case playback.Seeked:
		return
	case playback.Disconnected, playback.TransportFailure:
		c.connected = false
		return
	case playback.Stopped:
		c.settleAll(playback.Stopped)
		c.queue.reset()
		c.paused = false
		return
	case playback.Removed:
		c.settle(e, result)
		c.queue.remove(c.queue.index)
		if c.queue.index >= len(c.queue.entries) {
			c.queue.reset()
		}
		return
	case playback.Completed:
		if c.loop {
			e.ticket.request.Offset = 0
			return
		}
	}
	c.settle(e, result)
	c.queue.advance()
	c.paused = false
}

func (c *coordinator) settle(e *entry, result playback.Result) {
	if e == nil || e.settled {
		return
	}
	e.settled = true
	e.ticket.result = result
	close(e.ticket.done)
}
func (c *coordinator) settleAll(reason playback.Outcome) {
	for i := range c.queue.entries {
		e := &c.queue.entries[i]
		result := playback.Result{Request: e.ticket.request, Outcome: reason}
		if c.last.Request.ID == e.ticket.request.ID {
			result = c.last
			result.Outcome = reason
		}
		c.settle(e, result)
	}
}

func (c *coordinator) interrupt(reason playback.Outcome) {
	if c.active != nil {
		// The first accepted transition owns cancellation; do not overwrite its cause.
		if c.pending == "" {
			c.pending = reason
			c.cancel(playback.Cancellation{Outcome: reason})
		}
	}
}

func (c *coordinator) snapshot() Snapshot {
	s := Snapshot{Current: c.queue.index, Active: c.active != nil, Connected: c.connected, Paused: c.paused,
		Loop: c.loop, Volume: c.volume, Ducking: c.ducking, DuckTarget: c.duckTarget, Last: c.last}
	s.Last.Request.Controls = nil
	s.Tracks = make([]Track, len(c.queue.entries))
	for i, e := range c.queue.entries {
		s.Tracks[i] = e.track
	}
	if e := c.queue.current(); e != nil {
		s.Position = e.ticket.request.Offset
	}
	if c.controls != nil {
		s.Frames = c.controls.Frames()
		s.Position += timeForFrames(s.Frames)
	}
	return s
}
