package source

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
)

// Explicit opt-in only. These public IDs are the two historical NF album
// startup failures, not private/signed URLs. No Discord, credentials or cookies.
func TestProviderNFColdAndWarm(t *testing.T) {
	if os.Getenv("GOBARD_PROVIDER_CHECK") != "1" {
		t.Skip("external provider gate; opt in with GOBARD_PROVIDER_CHECK=1")
	}
	for _, id := range []string{"8fM1xnLTApg", "zpp6ZvJ351Q"} {
		t.Run(id, func(t *testing.T) {
			store, err := cache.NewCache(t.TempDir(), 16<<20)
			if err != nil {
				t.Fatal(err)
			}
			opener, err := NewYouTubeOpener(processlimit.New(1), store, log.New(io.Discard))
			if err != nil {
				t.Fatal(err)
			}
			engine, err := playback.New(opener, log.New(io.Discard))
			if err != nil {
				t.Fatal(err)
			}
			request := playback.Request{ID: "provider-check", AttemptID: "cold-" + id, Provider: "youtube", TrackID: id, MediaID: "https://www.youtube.com/watch?v=" + id}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			cold := engine.Run(ctx, request, &pacedProbe{})
			reportProviderResult(t, "cold paced", cold)
			if cold.Outcome != playback.Completed || cold.Frames == 0 {
				t.FailNow()
			}
			// Prove warm reuse without allowing an unnoticed network fallback.
			opener.executable = "/unavailable-provider-check-extractor"
			request.AttemptID = "warm-" + id
			warm := engine.Run(ctx, request, discardOutput{})
			reportProviderResult(t, "warm unpaced", warm)
			if warm.Outcome != playback.Completed || warm.Frames != cold.Frames {
				t.Fatal("warm replay did not reproduce all cold frames")
			}
		})
	}
}

func reportProviderResult(t *testing.T, mode string, result playback.Result) {
	t.Helper()
	// Never print raw extractor errors or diagnostics. Failure fields are from
	// the production classifier's allowlist, including an opaque fingerprint.
	t.Logf("mode=%s outcome=%s frames=%d elapsed=%s first_frame=%s", mode, result.Outcome, result.Frames, result.Elapsed, result.Timing.FirstFrame)
	if f := result.Failure; f != nil {
		t.Logf("stage=%s category=%s reason=%s exit=%d fingerprint=%s", f.Stage, f.Category, f.Reason, f.ExitCode, f.Fingerprint)
	}
}

type pacedProbe struct{ next time.Time }

func (p *pacedProbe) Send(ctx context.Context, _ []byte) error {
	if p.next.IsZero() {
		p.next = time.Now()
	}
	timer := time.NewTimer(max(0, time.Until(p.next)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		p.next = p.next.Add(playback.FrameDuration)
		return nil
	}
}
