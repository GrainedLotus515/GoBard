package cache

import (
	"errors"
	"os"
	"path/filepath"
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
		return os.WriteFile(path, []byte("audio-bytes"), 0o600)
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

func TestGetOrCreateRejectsOversizeWithoutEvictingExistingEntry(t *testing.T) {
	c, err := NewCache(t.TempDir(), 4)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	firstKey := GenerateKey("https://example.com/first")
	firstPath, err := c.GetOrCreate(firstKey, func(path string) error {
		return os.WriteFile(path, []byte("abc"), 0o600)
	})
	if err != nil {
		t.Fatalf("create first entry: %v", err)
	}

	_, err = c.GetOrCreate(GenerateKey("https://example.com/oversize"), func(path string) error {
		return os.WriteFile(path, []byte("12345"), 0o600)
	})
	if !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("oversize create error = %v, want ErrEntryTooLarge", err)
	}
	if path, ok := c.Get(firstKey); !ok || path != firstPath {
		t.Fatalf("existing entry after oversize create = (%q, %v), want (%q, true)", path, ok, firstPath)
	}
	if count, size, _ := c.GetStats(); count != 1 || size != 3 {
		t.Fatalf("GetStats() = (%d, %d), want (1, 3)", count, size)
	}
}

func TestNewCacheRemovesStalePartialFiles(t *testing.T) {
	dir := t.TempDir()
	partial := filepath.Join(dir, "track.webm.tmp-123")
	part := filepath.Join(dir, "other.webm.part")
	completed := filepath.Join(dir, "completed.webm")
	for path, data := range map[string]string{
		partial:   "partial",
		part:      "partial",
		completed: "complete",
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("seed %q: %v", path, err)
		}
	}

	c, err := NewCache(dir, 1024)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	for _, path := range []string{partial, part} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale partial %q still exists, stat error = %v", path, err)
		}
	}
	if count, size, _ := c.GetStats(); count != 1 || size != int64(len("complete")) {
		t.Fatalf("GetStats() = (%d, %d), want (1, %d)", count, size, len("complete"))
	}
}

func TestLeasePreventsEvictionUntilReleased(t *testing.T) {
	c, err := NewCache(t.TempDir(), 6)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	firstKey := GenerateKey("https://example.com/leased")
	if _, err := c.GetOrCreate(firstKey, func(path string) error {
		return os.WriteFile(path, []byte("1234"), 0o600)
	}); err != nil {
		t.Fatalf("create first entry: %v", err)
	}
	lease, ok := c.Acquire(firstKey)
	if !ok {
		t.Fatal("Acquire() = false, want true")
	}

	secondKey := GenerateKey("https://example.com/second")
	_, err = c.GetOrCreate(secondKey, func(path string) error {
		return os.WriteFile(path, []byte("5678"), 0o600)
	})
	if err == nil {
		t.Fatal("GetOrCreate() with only a leased eviction candidate succeeded, want error")
	}
	if _, ok := c.Get(firstKey); !ok {
		t.Fatal("leased entry was evicted")
	}

	lease.Release()
	if _, err := c.GetOrCreate(secondKey, func(path string) error {
		return os.WriteFile(path, []byte("5678"), 0o600)
	}); err != nil {
		t.Fatalf("GetOrCreate() after Release() error = %v", err)
	}
	if _, ok := c.Get(firstKey); ok {
		t.Fatal("released least-recently-used entry still present after capacity eviction")
	}
	if _, ok := c.Get(secondKey); !ok {
		t.Fatal("second entry missing after successful create")
	}
}

func TestCacheRejectsUnsafeKeys(t *testing.T) {
	c, err := NewCache(t.TempDir(), 1024)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	for _, key := range []string{"", "../outside.webm", "/tmp/outside.webm", "a/b.webm"} {
		if _, err := c.GetOrCreate(key, func(path string) error {
			return os.WriteFile(path, []byte("audio"), 0o600)
		}); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("GetOrCreate(%q) error = %v, want ErrInvalidKey", key, err)
		}
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
		return os.WriteFile(path, []byte("ok"), 0o600)
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
