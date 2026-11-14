package audio

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

func TestCancelDuringBlockedReadReportsOwnerCause(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	controls, err := playback.NewControls(100)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	stream, err := Decode(ctx, input, 0, controls)
	_ = input.Close()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		var packet [playback.MaxPacketBytes]byte
		_, err := stream.Next(ctx, packet[:])
		result <- err
	}()
	// FFmpeg is blocked probing an input that never arrives; Next is blocked
	// reading its stdout when the owner cancels.
	time.Sleep(100 * time.Millisecond)
	cause := playback.Cancellation{Outcome: playback.Skipped}
	cancel(cause)
	select {
	case err := <-result:
		if !errors.Is(err, cause) {
			t.Fatalf("Next = %v, want owner cause", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Next did not return after cancellation")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after owner cancellation = %v", err)
	}
}

func TestIndependentExitRacingCancellationStaysAuthoritative(t *testing.T) {
	exitErr := func(script string) error {
		err := exec.Command("sh", "-c", script).Run()
		if err == nil {
			t.Fatal("expected exit error")
		}
		return err
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		parent context.Context
		raw    error
		owner  bool
	}{
		{"normal failure races cancel", canceled, exitErr("exit 1"), false},
		{"signal kill after cancel", canceled, exitErr("kill -9 $$"), true},
		{"clean exit after cancel", canceled, nil, true},
		{"signal kill without cancel", context.Background(), exitErr("kill -9 $$"), false},
	} {
		s := &pcmStream{parent: tc.parent, waitRaw: tc.raw}
		if got := s.ownerKilled(); got != tc.owner {
			t.Errorf("%s: ownerKilled = %v, want %v", tc.name, got, tc.owner)
		}
	}
}
