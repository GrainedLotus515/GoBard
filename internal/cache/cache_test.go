package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCanceledWaiterDoesNotCancelCreator(t *testing.T) {
	c, err := NewCache(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := c.GetOrCreate("one.webm", func(path string) error {
			close(started)
			<-release
			return os.WriteFile(path, []byte("audio"), 0o600)
		})
		done <- err
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = c.GetOrCreateContext(ctx, "one.webm", func(string) error { t.Error("duplicate creator"); return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("wait error = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("one.webm"); !ok {
		t.Fatal("creator did not publish")
	}
}

func TestCanceledCreatorCannotPublish(t *testing.T) {
	c, err := NewCache(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = c.GetOrCreateContext(ctx, "track.webm", func(path string) error {
		if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("publish error = %v", err)
	}
	files, err := os.ReadDir(c.dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("canceled files = %v, %v", files, err)
	}
}

func TestCreatorAdmissionIsBoundedAndReleased(t *testing.T) {
	c, err := NewCache(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	started, release, done := make(chan struct{}, maxCreators), make(chan struct{}), make(chan error, maxCreators)
	for i := range maxCreators {
		go func() {
			_, err := c.GetOrCreate(fmt.Sprintf("%d.webm", i), func(path string) error {
				started <- struct{}{}
				<-release
				return os.WriteFile(path, []byte("audio"), 0o600)
			})
			done <- err
		}()
	}
	for range maxCreators {
		<-started
	}
	_, err = c.GetOrCreate("overflow.webm", func(string) error { t.Error("excess creator admitted"); return nil })
	if !errors.Is(err, ErrBusy) {
		t.Errorf("overflow error = %v", err)
	}
	close(release)
	for range maxCreators {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.GetOrCreate("after.webm", func(path string) error { return os.WriteFile(path, []byte("audio"), 0o600) }); err != nil {
		t.Fatal(err)
	}
}

func TestOldLeaseCannotUnpinReplacement(t *testing.T) {
	c, err := NewCache(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	create := func(path string) error { return os.WriteFile(path, []byte("audio"), 0o600) }
	path, err := c.GetOrCreate("track.webm", create)
	if err != nil {
		t.Fatal(err)
	}
	old, ok := c.Acquire("track.webm")
	if !ok {
		t.Fatal("missing original")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetOrCreate("track.webm", create); err != nil {
		t.Fatal(err)
	}
	replacement, ok := c.Acquire("track.webm")
	if !ok {
		t.Fatal("missing replacement")
	}
	old.Release()
	if err := c.Clear(); !errors.Is(err, ErrEntryLeased) {
		t.Fatalf("replacement lease lost: %v", err)
	}
	if _, err := os.Stat(replacement.Path); err != nil {
		t.Fatal(err)
	}
	replacement.Release()
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
}

func TestCreatorSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "outside")
	if err := os.WriteFile(target, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := NewCache(filepath.Join(dir, "cache"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetOrCreate("track.webm", func(path string) error { return os.Symlink(target, path) }); err == nil {
		t.Fatal("symlink published")
	}
	if count, size, _ := c.GetStats(); count != 0 || size != 0 {
		t.Fatal("rejected file indexed")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "private" {
		t.Fatal("symlink target changed")
	}
}

func BenchmarkCacheAcquire(b *testing.B) {
	dir := b.TempDir()
	cache, err := NewCache(dir, 1024*1024)
	if err != nil {
		b.Fatal(err)
	}
	key := GenerateKey("https://www.youtube.com/watch?v=benchmark")
	if _, err := cache.GetOrCreate(key, func(path string) error {
		return os.WriteFile(path, []byte("benchmark audio"), 0o600)
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			lease, ok := cache.Acquire(key)
			if !ok {
				b.Fatal("cache entry disappeared")
			}
			lease.Release()
		}
	})
}

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

func TestConcurrentAcquireClearDeletionResizeAndEviction(t *testing.T) {
	c, err := NewCache(t.TempDir(), 8)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	firstKey := GenerateKey("https://example.com/concurrent-first")
	firstPath, err := c.GetOrCreate(firstKey, func(path string) error {
		return os.WriteFile(path, []byte("1234"), 0o600)
	})
	if err != nil {
		t.Fatalf("create first entry: %v", err)
	}
	held, ok := c.Acquire(firstKey)
	if !ok {
		t.Fatal("Acquire(first) = false")
	}

	acquired := make(chan *Lease, 1)
	clearErr := make(chan error, 1)
	start := make(chan struct{})
	go func() {
		<-start
		lease, exists := c.Acquire(firstKey)
		if exists {
			acquired <- lease
			return
		}
		acquired <- nil
	}()
	go func() {
		<-start
		clearErr <- c.Clear()
	}()
	close(start)
	if lease := <-acquired; lease == nil {
		t.Fatal("concurrent Acquire() lost leased entry")
	} else {
		lease.Release()
	}
	if err := <-clearErr; !errors.Is(err, ErrEntryLeased) {
		t.Fatalf("concurrent Clear() error = %v, want ErrEntryLeased", err)
	}

	if err := os.WriteFile(firstPath, []byte("123456"), 0o600); err != nil {
		t.Fatalf("resize cached entry: %v", err)
	}
	resized, ok := c.Acquire(firstKey)
	if !ok {
		t.Fatal("Acquire() after external resize = false")
	}
	resized.Release()
	if _, size, _ := c.GetStats(); size != 6 {
		t.Fatalf("cache size after reconciliation = %d, want 6", size)
	}

	held.Release()
	secondKey := GenerateKey("https://example.com/concurrent-second")
	if _, err := c.GetOrCreate(secondKey, func(path string) error {
		return os.WriteFile(path, []byte("abcd"), 0o600)
	}); err != nil {
		t.Fatalf("evict resized entry: %v", err)
	}
	if _, ok := c.Get(firstKey); ok {
		t.Fatal("resized least-recently-used entry was not evicted")
	}

	secondPath, ok := c.Get(secondKey)
	if !ok {
		t.Fatal("second entry missing after eviction")
	}
	if err := os.Remove(secondPath); err != nil {
		t.Fatalf("delete cached file: %v", err)
	}
	if _, ok := c.Acquire(secondKey); ok {
		t.Fatal("Acquire() accepted externally deleted entry")
	}
	if count, size, _ := c.GetStats(); count != 0 || size != 0 {
		t.Fatalf("cache stats after deletion reconciliation = (%d, %d), want (0, 0)", count, size)
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
