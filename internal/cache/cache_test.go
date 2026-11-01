package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOldLeaseCannotUnpinReplacement(t *testing.T) {
	c, err := NewCache(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	path, err := storeBytes(c, "track.webm", []byte("audio"))
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
	if _, err := storeBytes(c, "track.webm", []byte("audio")); err != nil {
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

func BenchmarkCacheAcquire(b *testing.B) {
	dir := b.TempDir()
	cache, err := NewCache(dir, 1024*1024)
	if err != nil {
		b.Fatal(err)
	}
	key := GenerateKey("https://www.youtube.com/watch?v=benchmark")
	if _, err := storeBytes(cache, key, []byte("benchmark audio")); err != nil {
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

func TestWriteRejectsOversizeWithoutEvictingExistingEntry(t *testing.T) {
	c, err := NewCache(t.TempDir(), 4)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	firstKey := GenerateKey("https://example.com/first")
	firstPath, err := storeBytes(c, firstKey, []byte("abc"))
	if err != nil {
		t.Fatalf("create first entry: %v", err)
	}

	_, err = storeBytes(c, GenerateKey("https://example.com/oversize"), []byte("12345"))
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
	if _, err := storeBytes(c, firstKey, []byte("1234")); err != nil {
		t.Fatalf("create first entry: %v", err)
	}
	lease, ok := c.Acquire(firstKey)
	if !ok {
		t.Fatal("Acquire() = false, want true")
	}

	secondKey := GenerateKey("https://example.com/second")
	_, err = storeBytes(c, secondKey, []byte("5678"))
	if err == nil {
		t.Fatal("cache write with only a leased eviction candidate succeeded, want error")
	}
	if _, ok := c.Get(firstKey); !ok {
		t.Fatal("leased entry was evicted")
	}

	lease.Release()
	if _, err := storeBytes(c, secondKey, []byte("5678")); err != nil {
		t.Fatalf("cache write after Release() error = %v", err)
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
	firstPath, err := storeBytes(c, firstKey, []byte("1234"))
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
	if _, err := storeBytes(c, secondKey, []byte("abcd")); err != nil {
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
		if _, err := storeBytes(c, key, []byte("audio")); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("cache write(%q) error = %v, want ErrInvalidKey", key, err)
		}
	}
}

// Test setup uses the same bounded transaction as acquisition; no pathname
// creator callback bypasses the writer's byte budget.
func storeBytes(c *Cache, key string, data []byte) (string, error) {
	if path, ok := c.Get(key); ok {
		return path, nil
	}
	w, err := c.TryBegin(key, c.maxSize)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(data); err != nil {
		return "", errors.Join(err, w.Abort())
	}
	if err := w.Commit(context.Background()); err != nil {
		return "", err
	}
	path, _ := c.Get(key)
	return path, nil
}

func TestExistingSymlinkIsNotAudio(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "outside")
	if err := os.WriteFile(target, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := NewCache(filepath.Join(dir, "cache"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(c.dir, "track.webm")); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewCache(c.dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if lease, ok := restarted.Acquire("track.webm"); ok {
		lease.Release()
		t.Fatal("symlink accepted as audio")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "private" {
		t.Fatal("symlink target modified")
	}
}
