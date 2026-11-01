package session

import (
	"context"
	"errors"
	"testing"
)

func TestInteractionAuthorityCheckedByOwner(t *testing.T) {
	s, _ := newControlsTest(t)
	apply(t, s, Operation{Action: Connect, Channel: "voice", Output: outputFunc(func(context.Context, []byte) error { return nil })})
	for _, action := range []Action{Pause, Resume, TogglePause, Skip, Stop, Disconnect, Clear, Shuffle, Loop, Remove, Move, Seek, ForwardSeek, Volume} {
		if _, err := s.ApplyAs(context.Background(), Access{Channel: "other"}, Operation{Action: action}); !errors.Is(err, ErrVoiceAccess) {
			t.Errorf("%s: %v", action, err)
		}
	}
	for _, action := range []Action{Ducking, DuckTarget} {
		if _, err := s.ApplyAs(context.Background(), Access{Channel: "voice"}, Operation{Action: action}); !errors.Is(err, ErrManageGuild) {
			t.Errorf("%s without permission: %v", action, err)
		}
		if _, err := s.ApplyAs(context.Background(), Access{Channel: "other", ManageGuild: true}, Operation{Action: action}); !errors.Is(err, ErrVoiceAccess) {
			t.Errorf("%s wrong channel: %v", action, err)
		}
	}
	if _, err := s.ApplyAs(context.Background(), Access{}, Operation{Action: Inspect}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyAs(context.Background(), Access{Channel: "voice", ManageGuild: true}, Operation{Action: Connect}); err == nil {
		t.Fatal("interaction replaced transport")
	}
	apply(t, s, Operation{Action: Disconnect})
	if _, err := s.ApplyAs(context.Background(), Access{ManageGuild: true}, Operation{Action: Ducking, Enabled: true}); err != nil {
		t.Fatal("disconnected administrator configuration:", err)
	}
}
