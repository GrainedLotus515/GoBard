// Package session serializes guild queue policy and joins playback attempts.
package session

import (
	"context"
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

// Ticket is a single-assignment result for an admitted request. Waiting with a
// canceled context does not cancel an accepted request. Session owns completion.
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
	ctx     context.Context
	request playback.Request
	reason  playback.Outcome
	reply   chan response
}

type response struct {
	ticket *Ticket
	err    error
}

// Session owns one coordinator and at most one attempt goroutine. Dependencies
// must honor playback cancellation contracts. Close cancels and joins both.
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
	// #nosec G118 -- Session.Close owns cancellation; run also exits on parent cancellation.
	ctx, cancel := context.WithCancel(parent)
	s := &Session{ctx: ctx, cancel: cancel, done: make(chan struct{}), commands: make(chan command, MailboxCapacity)}
	go s.run(id, engine, output, capacity)
	return s, nil
}

// Enqueue copies bounded request metadata; IDs are assigned by the session.
func (s *Session) Enqueue(ctx context.Context, request playback.Request) (*Ticket, error) {
	if len(request.Provider) > 32 || len(request.MediaID) > 256 || request.Offset < 0 {
		return nil, errors.New("invalid playback request")
	}
	r := s.submit(command{ctx: ctx, request: request})
	return r.ticket, r.err
}

// Control acknowledges application of a reason, not completion of cleanup.
// Skip advances after cleanup; stop clears all admitted queued requests.
func (s *Session) Control(ctx context.Context, reason playback.Outcome) error {
	if reason != playback.Skipped && reason != playback.Stopped {
		return errors.New("invalid session control")
	}
	return s.submit(command{ctx: ctx, reason: reason}).err
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
	// Once submitted, await the definitive owner acknowledgement. The owner
	// checks cancellation before accepting; a timeout never implies rollback.
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

func (s *Session) Close() {
	s.closeOnce.Do(s.cancel)
	<-s.done
}

func (s *Session) run(id string, engine *playback.Engine, output playback.Output, capacity int) {
	defer close(s.done)
	queue := make([]*Ticket, 0, capacity)
	var current *Ticket
	var cancel context.CancelCauseFunc
	results := make(chan playback.Result, 1)
	var sequence uint64
	finish := func(ticket *Ticket, result playback.Result) { ticket.result = result; close(ticket.done) }
	clearQueue := func(reason playback.Outcome) {
		for _, ticket := range queue {
			finish(ticket, playback.Result{Request: ticket.request, Outcome: reason})
		}
		clear(queue)
		queue = queue[:0]
	}
	for {
		if current == nil && len(queue) > 0 && s.ctx.Err() == nil {
			current = queue[0]
			copy(queue, queue[1:])
			queue[len(queue)-1] = nil
			queue = queue[:len(queue)-1]
			cancel = startAttempt(s.ctx, engine, current.request, output, results)
		}
		select {
		case <-s.ctx.Done():
			clearQueue(playback.Shutdown)
			if current != nil {
				cancel(playback.Cancellation{Outcome: playback.Shutdown})
				finish(current, <-results)
			}
			return
		case result := <-results:
			cancel(nil)
			finish(current, result)
			current = nil
		case c := <-s.commands:
			if err := c.ctx.Err(); err != nil {
				c.reply <- response{err: err}
				continue
			}
			if c.reason != "" {
				if c.reason == playback.Stopped {
					clearQueue(playback.Stopped)
				}
				if current != nil {
					cancel(playback.Cancellation{Outcome: c.reason})
				}
				c.reply <- response{}
				continue
			}
			count := len(queue)
			if current != nil {
				count++
			}
			if count >= capacity {
				c.reply <- response{err: ErrFull}
				continue
			}
			sequence++
			c.request.ID = id + "/" + strconv.FormatUint(sequence, 10)
			c.request.AttemptID = c.request.ID + "/1"
			ticket := &Ticket{done: make(chan struct{}), request: c.request}
			queue = append(queue, ticket)
			c.reply <- response{ticket: ticket}
		}
	}
}

func startAttempt(parent context.Context, engine *playback.Engine, request playback.Request, output playback.Output, results chan<- playback.Result) context.CancelCauseFunc {
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		defer cancel(nil)
		results <- engine.Run(ctx, request, output)
	}()
	return cancel
}
