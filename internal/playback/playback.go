// Package playback owns one synchronous audio attempt, never queue policy.
package playback

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/failure"
)

const MaxPacketBytes = 4000
const FrameDuration = 20 * time.Millisecond

type Outcome string

const (
	Completed        Outcome = "completed"
	Skipped          Outcome = "skipped"
	Stopped          Outcome = "stopped"
	Seeked           Outcome = "seeked"
	Removed          Outcome = "removed"
	Disconnected     Outcome = "disconnected"
	Shutdown         Outcome = "shutdown"
	SourceFailure    Outcome = "source_failure"
	EncodeFailure    Outcome = "encode_failure"
	TransportFailure Outcome = "transport_failure"
)

// Cancellation is an explicit control reason passed to WithCancelCause.
type Cancellation struct{ Outcome Outcome }

func (c Cancellation) Error() string { return string(c.Outcome) }

// Request is immutable after admission. IDs must be application-generated,
// bounded correlation identifiers, not user text or media URLs.
type Request struct {
	ID        string
	AttemptID string
	CommandID string
	Provider  string
	MediaID   string
	Offset    time.Duration
	Controls  *Controls
}

// Stream has one consumer. Next writes one 20 ms stereo 48 kHz Opus packet
// into dst. EOF is valid only after acquisition/codec success is established.
// Close cancels and joins all resources; it is required even after Next fails.
// Both Next and Open must unblock when the opening context is canceled.
type Stream interface {
	Next(context.Context, []byte) (int, error)
	Close() error
}

type Opener interface {
	Open(context.Context, Request) (Stream, error)
}

// Output owns pacing and transport. Send must honor ctx, consume/copy packet
// before return, and never retain caller storage. Nil means packet acceptance,
// not human audibility. Output lifecycle belongs to the session/adapter.
type Output interface {
	Send(context.Context, []byte) error
}

type Result struct {
	Request Request
	Outcome Outcome
	Frames  uint64
	Elapsed time.Duration
	Failure *failure.Error
	Timing  Timing
}

// Engine is immutable and safe for concurrent attempts with independent
// outputs. Opener must support concurrent Open. A typed nil interface is not
// accepted by contract; Go interface nil checks cannot detect every typed nil.
type Engine struct {
	opener Opener
	logger *log.Logger
}

func New(opener Opener, logger *log.Logger) (*Engine, error) {
	if opener == nil || logger == nil {
		return nil, errors.New("playback requires opener and logger")
	}
	return &Engine{opener: opener, logger: logger}, nil
}

// Run returns only after stream cleanup and emits exactly one terminal event.
// The caller owns the goroutine if it chooses to run this asynchronously.
func (e *Engine) Run(ctx context.Context, request Request, output Output) (result Result) {
	started := time.Now()
	result.Request = request
	result.Outcome = SourceFailure
	defer func() {
		result.Request.Controls = nil
		if cause := context.Cause(ctx); cause != nil {
			var control Cancellation
			result.Outcome = Shutdown
			if errors.As(cause, &control) {
				result.Outcome = control.Outcome
			}
			// Cancellation-induced errors do not become spurious source failures.
			result.Failure = nil
		}
		result.Elapsed = time.Since(started)
		e.terminal(result)
	}()
	if err := ctx.Err(); err != nil {
		return result
	}
	if output == nil {
		result.Outcome = TransportFailure
		result.Failure = failure.Wrap("output", failure.Transport, errors.New("output required"))
		return result
	}
	stream, err := e.opener.Open(ctx, request)
	if err != nil {
		result.Failure = classify("source", failure.Source, err)
		result.Outcome = failedOutcome(result.Failure.Category)
		return result
	}
	if stream == nil {
		result.Failure = failure.Wrap("source", failure.Source, errors.New("opener returned nil stream"))
		return result
	}
	defer func() {
		if err := stream.Close(); err != nil && result.Failure == nil {
			result.Failure = classify("close", failure.Cleanup, err)
			result.Outcome = failedOutcome(result.Failure.Category)
		}
	}()
	var packet [MaxPacketBytes]byte
	for {
		if request.Controls.wait(ctx) != nil {
			return result
		}
		readStarted := time.Now()
		n, err := stream.Next(ctx, packet[:])
		readElapsed := time.Since(readStarted)
		if err != nil {
			if errors.Is(err, io.EOF) && n == 0 && result.Frames > 0 {
				result.Outcome = Completed
				return result
			}
			result.Outcome = EncodeFailure
			category := failure.Codec
			if errors.Is(err, io.EOF) {
				category = failure.ZeroFrames
			}
			result.Failure = classify("read", category, err)
			result.Outcome = failedOutcome(result.Failure.Category)
			return result
		}
		if n <= 0 || n > len(packet) {
			result.Outcome = EncodeFailure
			result.Failure = failure.Wrap("read", failure.InvalidFrame, errors.New("invalid packet size"))
			return result
		}
		if request.Controls.wait(ctx) != nil {
			return result
		}
		sendStarted := time.Now()
		if err := output.Send(ctx, packet[:n]); err != nil {
			result.Outcome = TransportFailure
			result.Failure = classify("output", failure.Transport, err)
			return result
		}
		accepted := time.Now()
		if result.Frames == 0 {
			result.Timing.FirstFrame = accepted.Sub(started)
		} else {
			result.Timing.record(readElapsed, accepted.Sub(sendStarted))
		}
		result.Frames++
		if request.Controls != nil {
			request.Controls.frames.Add(1)
		}
	}
}

func failedOutcome(category failure.Category) Outcome {
	switch category {
	case failure.Source, failure.Authentication, failure.Unavailable, failure.Throttled:
		return SourceFailure
	case failure.Transport:
		return TransportFailure
	default:
		return EncodeFailure
	}
}

func classify(stage string, category failure.Category, err error) *failure.Error {
	var classified *failure.Error
	if errors.As(err, &classified) {
		return classified
	}
	return failure.Wrap(stage, category, err)
}

func (e *Engine) terminal(r Result) {
	fields := []any{"event", "playback.terminal", "request_id", r.Request.ID, "attempt_id", r.Request.AttemptID,
		"command_id", r.Request.CommandID, "provider", r.Request.Provider, "outcome", r.Outcome, "recovery", "none", "frames", r.Frames, "elapsed_ms", r.Elapsed.Milliseconds(),
		"first_frame_ms", r.Timing.FirstFrame.Milliseconds(), "max_read_ms", r.Timing.MaxRead.Milliseconds(), "max_send_ms", r.Timing.MaxSend.Milliseconds(),
		"slow_reads", r.Timing.SlowReads, "slow_sends", r.Timing.SlowSends}
	if r.Failure != nil {
		fields = append(fields, "stage", r.Failure.Stage, "category", r.Failure.Category, "reason", r.Failure.Reason, "fingerprint", r.Failure.Fingerprint, "exit_code", r.Failure.ExitCode)
		e.logger.Error("Playback failed", fields...)
		return
	}
	e.logger.Info("Playback ended", fields...)
}
