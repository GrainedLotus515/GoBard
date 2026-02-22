package bot

import (
	"testing"
	"time"

	"github.com/GrainedLotus515/gobard/internal/player"
)

func TestGetDisplayPositionClampsForNonLiveTracks(t *testing.T) {
	track := &player.Track{
		Duration: 2 * time.Minute,
		IsLive:   false,
	}

	got := getDisplayPosition(track, 3*time.Minute)
	if got != 2*time.Minute {
		t.Fatalf("getDisplayPosition() = %v, want %v", got, 2*time.Minute)
	}
}

func TestGetDisplayPositionDoesNotClampLiveTracks(t *testing.T) {
	track := &player.Track{
		Duration: 2 * time.Minute,
		IsLive:   true,
	}

	got := getDisplayPosition(track, 3*time.Minute)
	if got != 3*time.Minute {
		t.Fatalf("getDisplayPosition() = %v, want %v", got, 3*time.Minute)
	}
}
