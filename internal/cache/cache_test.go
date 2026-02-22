package cache

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetOrCreateSingleFlight(t *testing.T) {
	c, err := NewCache(t.TempDir(), 16*1024*1024)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	key := GenerateKey("https://example.com/audio")
	var createCalls int32

	create := func(path string) error {
		atomic.AddInt32(&createCalls, 1)
		time.Sleep(25 * time.Millisecond)
		return os.WriteFile(path, []byte("audio-bytes"), 0o644)
	}

	const workers = 24
	var wg sync.WaitGroup
	paths := make(chan string, workers)
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := c.GetOrCreate(key, create)
			if err != nil {
				errs <- err
				return
			}
			paths <- path
		}()
	}

	wg.Wait()
	close(paths)
	close(errs)

	for err := range errs {
		t.Fatalf("GetOrCreate() unexpected error: %v", err)
	}

	var firstPath string
	count := 0
	for path := range paths {
		count++
		if firstPath == "" {
			firstPath = path
			continue
		}
		if path != firstPath {
			t.Fatalf("GetOrCreate() returned different paths: %q vs %q", firstPath, path)
		}
	}

	if count != workers {
		t.Fatalf("GetOrCreate() returned %d successful paths, want %d", count, workers)
	}

	if got := atomic.LoadInt32(&createCalls); got != 1 {
		t.Fatalf("create() called %d times, want 1", got)
	}

	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("cached file missing at %q: %v", firstPath, err)
	}
}

func TestGetOrCreateFailureReleasesInFlight(t *testing.T) {
	c, err := NewCache(t.TempDir(), 16*1024*1024)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	key := GenerateKey("https://example.com/failure")
	var failCalls int32

	failCreate := func(_ string) error {
		atomic.AddInt32(&failCalls, 1)
		time.Sleep(20 * time.Millisecond)
		return errors.New("forced create failure")
	}

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetOrCreate(key, failCreate)
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err == nil {
			t.Fatal("GetOrCreate() expected error, got nil")
		}
	}

	if got := atomic.LoadInt32(&failCalls); got != 1 {
		t.Fatalf("fail create() called %d times, want 1", got)
	}

	var successCalls int32
	successCreate := func(path string) error {
		atomic.AddInt32(&successCalls, 1)
		return os.WriteFile(path, []byte("ok"), 0o644)
	}

	path, err := c.GetOrCreate(key, successCreate)
	if err != nil {
		t.Fatalf("GetOrCreate() after failure error = %v", err)
	}

	if got := atomic.LoadInt32(&successCalls); got != 1 {
		t.Fatalf("success create() called %d times, want 1", got)
	}

	path2, err := c.GetOrCreate(key, func(_ string) error {
		atomic.AddInt32(&successCalls, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("second GetOrCreate() error = %v", err)
	}
	if path2 != path {
		t.Fatalf("second GetOrCreate() path = %q, want %q", path2, path)
	}
	if got := atomic.LoadInt32(&successCalls); got != 1 {
		t.Fatalf("cached lookup should not call create(), called %d times", got)
	}
}
