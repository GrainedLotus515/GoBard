// Package failure classifies external failures without exposing their payloads.
package failure

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

type Category string

type Reason string

const (
	ConnectionReset   Reason = "connection_reset"
	NetworkTimeout    Reason = "network_timeout"
	HTTPForbidden     Reason = "http_forbidden"
	FormatUnavailable Reason = "format_unavailable"
)

const (
	Source         Category = "source"
	Authentication Category = "authentication"
	Unavailable    Category = "unavailable"
	Throttled      Category = "throttled"
	Codec          Category = "codec"
	Transport      Category = "transport"
	ZeroFrames     Category = "zero_frames"
	InvalidFrame   Category = "invalid_frame"
	Cleanup        Category = "cleanup"
)

// Error retains the cause for errors.Is/As, but Error never formats that cause.
// Stage and Category are application-owned values, never external diagnostics.
type Error struct {
	Stage       string
	Category    Category
	Reason      Reason
	Fingerprint string
	ExitCode    int
	Cause       error
}

func (e *Error) Error() string { return e.Stage + ": " + string(e.Category) }
func (e *Error) Unwrap() error { return e.Cause }

func Wrap(stage string, category Category, cause error) *Error {
	return &Error{Stage: stage, Category: category, Cause: cause, ExitCode: -1}
}

// Diagnostic retains a bounded prefix and drains all remaining bytes. It has
// one writer (exec.Cmd's stderr copier); inspect only after Cmd.Wait returns.
type Diagnostic struct {
	data [16384]byte
	n    int
}

func (d *Diagnostic) Write(p []byte) (int, error) {
	d.n += copy(d.data[d.n:], p)
	return len(p), nil
}

func (d *Diagnostic) Failure(stage string, fallback Category, cause error) *Error {
	category := fallback
	text := strings.ToLower(string(d.data[:d.n]))
	switch {
	case strings.Contains(text, "sign in to confirm"), strings.Contains(text, "login required"), strings.Contains(text, "authentication required"):
		category = Authentication
	case strings.Contains(text, "http error 429"), strings.Contains(text, "too many requests"):
		category = Throttled
	case strings.Contains(text, "video unavailable"), strings.Contains(text, "private video"), strings.Contains(text, "has been removed"):
		category = Unavailable
	}
	hash := sha256.Sum256(d.data[:d.n])
	f := Wrap(stage, category, cause)
	if category == Source {
		switch {
		case strings.Contains(text, "http error 403"):
			f.Reason = HTTPForbidden
		case strings.Contains(text, "requested format is not available"):
			f.Reason = FormatUnavailable
		case strings.Contains(text, "connection reset"):
			f.Reason = ConnectionReset
		case strings.Contains(text, "timed out"), strings.Contains(text, "timeout error"):
			f.Reason = NetworkTimeout
		}
	}
	f.Fingerprint = hex.EncodeToString(hash[:8])
	var status interface{ ExitCode() int }
	if errors.As(cause, &status) {
		f.ExitCode = status.ExitCode()
	}
	return f
}
