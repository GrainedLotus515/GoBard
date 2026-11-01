// Package failure classifies external failures without exposing their payloads.
package failure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

type Category string

type Reason string

const (
	ConnectionReset    Reason = "connection_reset"
	NetworkTimeout     Reason = "network_timeout"
	HTTPForbidden      Reason = "http_forbidden"
	FormatUnavailable  Reason = "format_unavailable"
	StartupTimeout     Reason = "startup_timeout"
	PermissionDenied   Reason = "permission_denied"
	NotFound           Reason = "not_found"
	NoSpace            Reason = "no_space"
	ResourceLimit      Reason = "resource_limit"
	ConnectionRefused  Reason = "connection_refused"
	DNSFailure         Reason = "dns_failure"
	NetworkUnreachable Reason = "network_unreachable"
	TLSFailure         Reason = "tls_failure"
	InvalidMedia       Reason = "invalid_media"
	CodecUnavailable   Reason = "codec_unavailable"
	BrokenPipe         Reason = "broken_pipe"
	ShortWrite         Reason = "short_write"
	Unknown            Reason = "unrecognized_failure"
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
	f := &Error{Stage: stage, Category: category, Cause: cause, ExitCode: -1, Reason: safeReason(cause)}
	if classified := From(cause); classified != nil {
		f.Category, f.Reason = classified.Category, classified.Reason
		f.Fingerprint, f.ExitCode = classified.Fingerprint, classified.ExitCode
	} else if cause != nil {
		hash := sha256.Sum256([]byte(bounded(cause.Error())))
		f.Fingerprint = hex.EncodeToString(hash[:8])
	}
	return f
}

// From selects the originating classification through contextual wrappers.
// All consumers use this rule; adding context must not erase provider guidance.
func From(err error) *Error {
	var result *Error
	for {
		var current *Error
		if !errors.As(err, &current) {
			return result
		}
		result, err = current, current.Cause
	}
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
	if category == Authentication || category == Throttled || category == Unavailable {
		f.Reason = "" // Category takes precedence over unrelated diagnostic text.
	} else if reason := reasonForText(text); reason != Unknown {
		f.Reason = reason
	}
	f.Fingerprint = hex.EncodeToString(hash[:8])
	var status interface{ ExitCode() int }
	if errors.As(cause, &status) {
		f.ExitCode = status.ExitCode()
	}
	return f
}

// Arbitrary causes and diagnostics never cross the logging boundary. Match
// typed OS/network errors first, then a bounded vocabulary, emitting only our
// own codes and guidance. Unknown payloads remain correlated by fingerprint.
func safeReason(err error) Reason {
	if err == nil {
		return Unknown
	}
	for _, pair := range []struct {
		err    error
		reason Reason
	}{
		{context.DeadlineExceeded, NetworkTimeout}, {syscall.ETIMEDOUT, NetworkTimeout},
		{syscall.ECONNRESET, ConnectionReset}, {syscall.ECONNREFUSED, ConnectionRefused},
		{syscall.EACCES, PermissionDenied}, {syscall.EPERM, PermissionDenied},
		{syscall.ENOENT, NotFound}, {syscall.ENOSPC, NoSpace},
		{syscall.ENOMEM, ResourceLimit}, {syscall.EMFILE, ResourceLimit}, {syscall.ENFILE, ResourceLimit},
		{syscall.ENETUNREACH, NetworkUnreachable}, {syscall.EHOSTUNREACH, NetworkUnreachable},
		{syscall.EPIPE, BrokenPipe}, {io.ErrClosedPipe, BrokenPipe}, {io.ErrShortWrite, ShortWrite},
	} {
		if errors.Is(err, pair.err) {
			return pair.reason
		}
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		if dns.IsTimeout {
			return NetworkTimeout
		}
		return DNSFailure
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return NetworkTimeout
	}
	return reasonForText(strings.ToLower(bounded(err.Error())))
}

func bounded(text string) string { return text[:min(len(text), 16384)] }

func reasonForText(text string) Reason {
	for _, pair := range []struct {
		phrase string
		reason Reason
	}{
		{"http error 403", HTTPForbidden}, {"requested format is not available", FormatUnavailable},
		{"connection reset", ConnectionReset}, {"timed out", NetworkTimeout}, {"timeout error", NetworkTimeout},
		{"permission denied", PermissionDenied}, {"no such file or directory", NotFound},
		{"no space left", NoSpace}, {"cannot allocate memory", ResourceLimit}, {"too many open files", ResourceLimit},
		{"connection refused", ConnectionRefused}, {"network is unreachable", NetworkUnreachable},
		{"name or service not known", DNSFailure}, {"temporary failure in name resolution", DNSFailure},
		{"certificate verify failed", TLSFailure}, {"certificate signed by unknown authority", TLSFailure},
		{"invalid data found when processing input", InvalidMedia}, {"error while decoding", InvalidMedia},
		{"unknown encoder", CodecUnavailable}, {"decoder not found", CodecUnavailable}, {"encoder not found", CodecUnavailable},
		{"broken pipe", BrokenPipe},
	} {
		if strings.Contains(text, pair.phrase) {
			return pair.reason
		}
	}
	return Unknown
}

// Guidance is fixed operator copy, never a formatted external payload. Even an
// unrecognized diagnostic gives the operator a bounded next investigation step.
func (e *Error) Guidance() string {
	switch e.Category {
	case Authentication:
		return "Provider denied access; verify public availability without adding personal credentials."
	case Throttled:
		return "Provider rate limit; wait and reduce request concurrency."
	case Unavailable:
		return "Media unavailable; check the public track identifier."
	}
	switch e.Reason {
	case PermissionDenied:
		return "Check container user and file or channel permissions."
	case NotFound:
		return "Check required runtime tools and cache mount availability."
	case NoSpace:
		return "Check cache filesystem and bounded tmpfs free space."
	case ResourceLimit:
		return "Check container memory, process and file descriptor limits; reduce concurrency."
	case NetworkTimeout, StartupTimeout:
		return "Check provider or voice connectivity and resource pressure."
	case ConnectionReset, ConnectionRefused, NetworkUnreachable:
		return "Check outbound connectivity and remote service availability."
	case DNSFailure:
		return "Check container DNS resolution."
	case TLSFailure:
		return "Check container CA certificates and system clock."
	case HTTPForbidden:
		return "Provider refused download; verify public availability and extractor compatibility."
	case FormatUnavailable:
		return "Provider offered no compatible format; verify extractor compatibility."
	case InvalidMedia:
		return "Decoder rejected media; compare cold acquisition with cached media and check codec support."
	case CodecUnavailable:
		return "Check the pinned runtime FFmpeg encoder and decoder inventory."
	case BrokenPipe, ShortWrite:
		return "Downstream transport or codec closed; inspect correlated attempt and cleanup events."
	}
	switch e.Category {
	case Codec, InvalidFrame, ZeroFrames:
		return "Unrecognized audio failure; check FFmpeg/libopus compatibility and reproduce with isolated media using this fingerprint."
	case Transport:
		return "Unrecognized voice failure; check Connect/Speak permissions, outbound UDP and pinned SDK compatibility."
	default:
		return "Unrecognized source or cleanup failure; check tool versions, cache permissions and provider availability using this fingerprint."
	}
}
