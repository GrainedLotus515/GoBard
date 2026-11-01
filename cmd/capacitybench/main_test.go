package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/source"
)

func TestCapacityWorkloadUsesRealAudioAndJoins(t *testing.T) {
	dir := t.TempDir()
	path, err := createDeterministicMedia(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../scripts/bench-extractor.sh")
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G306 G703 -- fixed filename, owner-only executable in t.TempDir; no external path input.
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), fixture, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOBARD_BENCH_MEDIA", path)
	store, err := cache.NewCache(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	opener, err := source.NewYouTubeOpener(processlimit.New(1), store, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var measured measurements
	var workers sync.WaitGroup
	workers.Add(1)
	go runGuild(ctx, &workers, 0, opener, store, &measured)
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("workload did not join")
	}
	if measured.frames.Load() == 0 || measured.workerFailures.Load() != 0 {
		t.Fatalf("frames=%d failures=%d", measured.frames.Load(), measured.workerFailures.Load())
	}
	if measured.maximumShutdownNS.Load() > int64(2*time.Second) {
		t.Fatal("shutdown exceeded budget")
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
}

func TestFrameGapPercentileReportsDistribution(t *testing.T) {
	var measured measurements
	measured.frameGaps[20].Store(98)
	measured.frameGaps[40].Store(1)
	measured.frameGaps[1000].Store(1)
	if p99 := frameGapPercentile(&measured, 99); p99 != 40 {
		t.Fatalf("p99=%d", p99)
	}
}

func TestReducedProfileUsesDerivedAdmission(t *testing.T) {
	profile, err := selectedProfile("reduced")
	if err != nil {
		t.Fatal(err)
	}
	if profile.guilds != 5 || profile.memoryBytes != 512<<20 {
		t.Fatalf("profile = %+v, want five guilds under 512 MiB", profile)
	}
}
