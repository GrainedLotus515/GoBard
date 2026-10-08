// Package session serializes guild queue policy and joins playback attempts.
package session

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

var (
	ErrBusy        = errors.New("session command mailbox full")
	ErrFull        = errors.New("session queue full")
	ErrClosed      = errors.New("session closed")
	ErrSuperseded  = errors.New("command superseded by stop or disconnect")
	ErrVoiceAccess = errors.New("join the bot's active voice channel to control playback")
	ErrManageGuild = errors.New("manage server permission required")
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
	ctx         context.Context
	tracks      []Track
	operation   Operation
	reply       chan response
	access      *Access
	generation  *uint64
	commandID   string
	joinChannel string
	reserve     string
	release     string
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
	changed   chan struct{}
}

func New(parent context.Context, id string, engine *playback.Engine, output playback.Output, capacity int) (*Session, error) {
	return newSession(parent, id, engine, output, nil, nil, capacity)
}

func NewVoice(parent context.Context, id string, engine *playback.Engine, voice Voice, logger *log.Logger, capacity int) (*Session, error) {
	if voice == nil || logger == nil {
		return nil, errors.New("voice and logger required")
	}
	return newSession(parent, id, engine, nil, voice, logger, capacity)
}

func newSession(parent context.Context, id string, engine *playback.Engine, output playback.Output, voice Voice, logger *log.Logger, capacity int) (*Session, error) {
	if parent == nil || id == "" || len(id) > 64 || engine == nil || output == nil && voice == nil || capacity < 1 || capacity > MaxQueue {
		return nil, errors.New("invalid session dependencies or capacity")
	}
	// #nosec G118 -- Session.Close owns cancellation; parent also terminates run.
	ctx, cancel := context.WithCancel(parent)
	s := &Session{ctx: ctx, cancel: cancel, done: make(chan struct{}), commands: make(chan command, MailboxCapacity), changed: make(chan struct{}, 1)}
	c := &coordinator{session: s, id: id + "/" + rand.Text(), engine: engine, output: output, connected: output != nil, voice: voice, logger: logger,
		queue:   queue{entries: make([]entry, 0, capacity), index: -1, capacity: capacity},
		results: make(chan playback.Result, 1), volume: 100, duckTarget: 70, speakers: make(map[string]bool), reservations: make(map[string]struct{}), transportResults: make(chan transportResult, 1)}
	go c.run()
	return s, nil
}

// Changes coalesces state-change notifications into one slot. Consumers inspect
// a fresh snapshot; no payload or unbounded event history is retained.
func (s *Session) Changes() <-chan struct{} { return s.changed }
func (s *Session) Done() <-chan struct{}    { return s.done }

func (s *Session) Join(ctx context.Context, channel string) (Snapshot, error) {
	if channel == "" || len(channel) > 32 {
		return Snapshot{}, ErrVoiceAccess
	}
	r := s.submit(command{ctx: ctx, joinChannel: channel})
	return r.snapshot, r.err
}

func (s *Session) BeginResolution(ctx context.Context, access Access, id string) (Snapshot, error) {
	if id == "" || len(id) > 128 {
		return Snapshot{}, errors.New("invalid resolution identifier")
	}
	r := s.submit(command{ctx: ctx, access: &access, reserve: id})
	return r.snapshot, r.err
}
func (s *Session) EndResolution(id string) { s.submit(command{ctx: s.ctx, release: id}) }

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
	return s.add(command{ctx: ctx}, tracks)
}

// AddResolved admits results only into the same session generation that began
// resolution. Stop/disconnect supersede pending lookups even after reconnect.
func (s *Session) AddResolved(ctx context.Context, access Access, generation uint64, commandID string, tracks []Track) ([]*Ticket, error) {
	if len(commandID) == 0 || len(commandID) > 128 {
		return nil, errors.New("invalid command correlation identifier")
	}
	for _, c := range commandID {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '/':
		default:
			return nil, errors.New("invalid command correlation identifier")
		}
	}
	return s.add(command{ctx: ctx, access: &access, generation: &generation, commandID: commandID}, tracks)
}

func (s *Session) add(cmd command, tracks []Track) ([]*Ticket, error) {
	if len(tracks) == 0 || len(tracks) > MaxQueue {
		return nil, ErrFull
	}
	for _, track := range tracks {
		if err := track.Validate(); err != nil {
			return nil, err
		}
	}
	owned := make([]Track, len(tracks))
	for i, track := range tracks {
		owned[i] = track.Clone()
	}
	cmd.tracks = owned
	r := s.submit(cmd)
	return r.tickets, r.err
}

// ApplyAs is the untrusted interaction boundary. Apply remains for trusted
// lifecycle operations (connect, speaking events and shutdown composition).
func (s *Session) ApplyAs(ctx context.Context, access Access, operation Operation) (Snapshot, error) {
	r := s.submit(command{ctx: ctx, access: &access, operation: operation})
	return r.snapshot, r.err
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
	generation            uint64
	channel               string
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
	voice                 Voice
	logger                *log.Logger
	reservations          map[string]struct{}
	drained               bool
	transportResults      chan transportResult
	transportCancel       context.CancelFunc
	transportKind         string
	joinReply             chan response
	needDisconnect        bool
	recoveryAttempts      int
	retryTimer            *time.Timer
	retry                 <-chan time.Time
	dirty                 bool
}

func (c *coordinator) run() {
	defer close(c.session.done)
	defer c.closeVoice()
	c.dirty = true
	for {
		c.driveTransport()
		if c.active == nil && c.connected && c.transportCancel == nil && c.queue.current() != nil && c.session.ctx.Err() == nil {
			c.start()
			c.dirty = true
		}
		if c.dirty {
			select {
			case c.session.changed <- struct{}{}:
			default:
			}
			c.dirty = false
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
			c.dirty = true
			c.complete(result)
		case result := <-c.transportResults:
			c.dirty = true
			c.transportComplete(result)
		case <-c.retry:
			c.dirty = true
			c.retry = nil
			c.startRecovery()
		case cmd := <-c.session.commands:
			c.handle(cmd)
		}
	}
}

func (c *coordinator) handle(cmd command) {
	if cmd.operation.Action != Inspect {
		c.dirty = true
	}
	// Observe already-published completion before applying the next control.
	select {
	case result := <-c.results:
		c.dirty = true
		c.complete(result)
	default:
	}
	if err := cmd.ctx.Err(); err != nil {
		cmd.reply <- response{err: err}
		return
	}
	if c.session.ctx.Err() != nil {
		cmd.reply <- response{err: ErrClosed}
		return
	}
	if cmd.joinChannel != "" {
		c.join(cmd)
		return
	}
	if cmd.reserve != "" || cmd.release != "" {
		c.resolution(cmd)
		return
	}
	if len(cmd.tracks) > 0 {
		if cmd.generation != nil && *cmd.generation != c.generation {
			cmd.reply <- response{err: ErrSuperseded}
		} else if cmd.access != nil && (!c.connected || c.channel == "" || cmd.access.Channel != c.channel) {
			cmd.reply <- response{err: ErrVoiceAccess}
		} else {
			cmd.reply <- c.add(cmd.tracks, cmd.commandID)
		}
		return
	}
	if cmd.access != nil {
		if err := c.authorize(*cmd.access, cmd.operation.Action); err != nil {
			cmd.reply <- response{err: err}
			return
		}
	}
	err := c.apply(cmd.operation)
	cmd.reply <- response{snapshot: c.snapshot(), err: err}
}

func (c *coordinator) add(tracks []Track, commandID string) response {
	if c.stopAfter {
		return response{err: ErrBusy}
	}
	if len(c.queue.entries)+len(tracks) > c.queue.capacity {
		return response{err: ErrFull}
	}
	tickets := make([]*Ticket, 0, len(tracks))
	for _, track := range tracks {
		c.sequence++
		request := playback.Request{ID: c.id + "/" + strconv.FormatUint(c.sequence, 10), CommandID: commandID, Provider: track.Provider, MediaID: track.MediaID}
		ticket := &Ticket{done: make(chan struct{}), request: request}
		c.queue.entries = append(c.queue.entries, entry{track: track, ticket: ticket})
		tickets = append(tickets, ticket)
	}
	if c.queue.index < 0 {
		c.queue.index = 0
	}
	c.drained = false
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
	defer func() {
		if c.queue.current() == nil {
			c.drained = true
		}
	}()
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
		if reason == playback.TransportFailure {
			c.generation++
			c.scheduleRecovery()
		}
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
	c.recoveryAttempts = 0
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
	s := Snapshot{Generation: c.generation, Channel: c.channel, Current: c.queue.index, Active: c.active != nil, Connected: c.connected, Connecting: c.transportKind == "connect" || c.transportKind == "recover", Paused: c.paused,
		Loop: c.loop, Volume: c.volume, Ducking: c.ducking, DuckTarget: c.duckTarget, Last: c.last}
	s.Last.Request.Controls = nil
	s.Tracks = make([]Track, len(c.queue.entries))
	for i, e := range c.queue.entries {
		s.Tracks[i] = e.track
	}
	s.Position = c.position()
	if c.controls != nil {
		s.Frames = c.controls.Frames()
	}
	return s
}

func (c *coordinator) position() time.Duration {
	var offset time.Duration
	if e := c.queue.current(); e != nil {
		offset = e.ticket.request.Offset
	}
	if c.controls != nil {
		elapsed := timeForFrames(c.controls.Frames())
		if elapsed > time.Duration(math.MaxInt64)-offset {
			return time.Duration(math.MaxInt64)
		}
		offset += elapsed
	}
	return offset
}
