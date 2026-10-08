package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/GrainedLotus515/gobard/internal/cache"
)

func TestCapacityWorkloadUsesRealAudioAndJoins(t *testing.T) {
	dir := t.TempDir()
	path, err := createDeterministicMedia(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewCache(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var measured measurements
	var workers sync.WaitGroup
	workers.Add(1)
	go runGuild(ctx, &workers, 0, path, store, &measured)
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
