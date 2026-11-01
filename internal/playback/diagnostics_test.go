package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/failure"
)

func TestProductionDiagnosticRecordsAreSafeAndActionable(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		category   failure.Category
		reason     failure.Reason
	}{
		{"decoder", "Invalid data found when processing input", failure.Codec, failure.InvalidMedia},
		{"encoder", "Unknown encoder", failure.Codec, failure.CodecUnavailable},
		{"storage", "No space left on device", failure.Source, failure.NoSpace},
		{"unknown", "unrecognized failure", failure.Codec, failure.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var records bytes.Buffer
			var diagnostic failure.Diagnostic
			secret := " https://signed.invalid/?token=SECRET Cookie: SECRET Authorization: SECRET @everyone\x1b[31m\n"
			_, err := diagnostic.Write([]byte(tc.text + strings.Repeat(secret, 10000)))
			if err != nil {
				t.Fatal(err)
			}
			f := diagnostic.Failure("decode", tc.category, errors.New(secret))
			engine, err := New(openFunc(func(context.Context, Request) (Stream, error) { return nil, f }), log.NewWithOptions(&records, log.Options{Formatter: log.JSONFormatter}))
			if err != nil {
				t.Fatal(err)
			}
			engine.Run(context.Background(), Request{ID: "request", AttemptID: "attempt", CommandID: "command", MediaID: secret}, sendFunc(func(context.Context, []byte) error { return nil }))
			var event map[string]any
			if err := json.Unmarshal(records.Bytes(), &event); err != nil {
				t.Fatal("expected exactly one record", err)
			}
			if event["reason"] != string(tc.reason) || event["guidance"] != f.Guidance() || event["request_id"] != "request" || event["attempt_id"] != "attempt" || event["command_id"] != "command" {
				t.Fatalf("record=%v", event)
			}
			for _, forbidden := range []string{"SECRET", "signed.invalid", "Cookie", "Authorization", "@everyone", "31m"} {
				if strings.Contains(records.String(), forbidden) {
					t.Fatal("unsafe record", forbidden)
				}
			}
			if len(records.Bytes()) > 2048 {
				t.Fatal("unbounded record")
			}
		})
	}
	// Paths and syscall wrappers must retain errors.Is while exposing only a code.
	cause := &os.PathError{Op: "open", Path: "/SECRET", Err: syscall.EACCES}
	f := failure.Wrap("open", failure.Source, cause)
	if f.Reason != failure.PermissionDenied || !errors.Is(f, syscall.EACCES) {
		t.Fatal(f)
	}
}
