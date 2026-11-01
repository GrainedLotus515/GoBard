package session

import (
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/failure"
)

func TestVoiceConnectionRecordIncludesSafeNetworkReason(t *testing.T) {
	var records bytes.Buffer
	c := &coordinator{session: &Session{}, id: "session", logger: log.NewWithOptions(&records, log.Options{Formatter: log.JSONFormatter}), transportCancel: func() {}, transportKind: "connect"}
	c.session.ctx = t.Context()
	c.transportComplete(transportResult{err: &net.OpError{Op: "dial SECRET", Net: "udp", Err: syscall.ECONNREFUSED}})
	var event map[string]any
	if err := json.Unmarshal(records.Bytes(), &event); err != nil {
		t.Fatal("expected one record", err)
	}
	if event["event"] != "voice.connection" || event["reason"] != string(failure.ConnectionRefused) || event["guidance"] == "" || event["session_id"] != "session" {
		t.Fatalf("record=%v", event)
	}
	if strings.Contains(records.String(), "SECRET") {
		t.Fatal("raw network cause logged")
	}
}
