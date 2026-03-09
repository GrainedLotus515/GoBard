package bot

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapVoiceJoinErrorExplainsDAVERequirement(t *testing.T) {
	err := errors.New("voice websocket closed, websocket: close 4017: E2EE/DAVE protocol required")

	got := wrapVoiceJoinError(err)
	if got == nil {
		t.Fatal("wrapVoiceJoinError() returned nil")
	}

	message := got.Error()
	want := []string{
		"close code 4017",
		"DAVE-capable voice stack",
		"libdave",
		"installed correctly",
	}
	for _, fragment := range want {
		if !strings.Contains(message, fragment) {
			t.Fatalf("wrapVoiceJoinError() message %q does not contain %q", message, fragment)
		}
	}
}

func TestWrapVoiceJoinErrorPreservesOtherFailures(t *testing.T) {
	err := errors.New("context deadline exceeded")

	got := wrapVoiceJoinError(err)
	if got == nil {
		t.Fatal("wrapVoiceJoinError() returned nil")
	}

	const want = "failed to join voice channel: context deadline exceeded"
	if got.Error() != want {
		t.Fatalf("wrapVoiceJoinError() = %q, want %q", got.Error(), want)
	}
}
