package logger

import (
	"fmt"
	"sync/atomic"
	"time"
)

var traceCounter uint64

// Trace correlates debug logs that belong to the same workflow.
type Trace struct {
	id        string
	operation string
	startedAt time.Time
}

// NewTrace starts a new trace and emits an initial debug step.
func NewTrace(operation string, keyvals ...any) *Trace {
	trace := &Trace{
		id:        nextTraceID(),
		operation: operation,
		startedAt: time.Now(),
	}

	trace.Step("Trace started", keyvals...)
	return trace
}

// ResumeTrace restores a previously created trace.
func ResumeTrace(id, operation string, startedAt time.Time) *Trace {
	if id == "" || startedAt.IsZero() {
		return nil
	}

	return &Trace{
		id:        id,
		operation: operation,
		startedAt: startedAt,
	}
}

// ID returns the trace identifier.
func (t *Trace) ID() string {
	if t == nil {
		return ""
	}
	return t.id
}

// StartedAt returns the trace start time.
func (t *Trace) StartedAt() time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.startedAt
}

// Step logs a trace step with elapsed time when debug mode is enabled.
func (t *Trace) Step(msg string, keyvals ...any) {
	if t == nil || !debugMode {
		return
	}

	attrs := []any{
		"trace_id", t.id,
		"operation", t.operation,
		"elapsed_ms", time.Since(t.startedAt).Milliseconds(),
	}
	attrs = append(attrs, keyvals...)
	Logger.Debug(msg, attrs...)
}

// Finish logs the final trace step.
func (t *Trace) Finish(msg string, keyvals ...any) {
	t.Step(msg, keyvals...)
}

func nextTraceID() string {
	id := atomic.AddUint64(&traceCounter, 1)
	return fmt.Sprintf("%08x", id)
}
