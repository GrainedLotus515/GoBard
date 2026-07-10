package cache

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrEntryTooLarge means a single file cannot ever fit in this cache. It is
	// deliberately not followed by eviction: evicting useful entries for an
	// entry that still exceeds the limit violates the configured size bound.
	ErrEntryTooLarge = errors.New("cache entry exceeds configured size limit")
	ErrInvalidKey    = errors.New("invalid cache key")
	ErrEntryLeased   = errors.New("cache entry is in use")
)

// Cache manages cached audio files. All mutations of entries and currentSize
// are protected by mu; file creation happens outside that lock and is
// committed transactionally once complete.
type Cache struct {
	dir          string
	maxSize      int64
	mu           sync.RWMutex
	entries      map[string]*CacheEntry
	currentSize  int64
	inFlight     map[string]*inFlightCreate
	tempSequence atomic.Uint64
}

// CacheEntry represents a cached file.
type CacheEntry struct {
	Path         string
	Size         int64
	LastAccessed time.Time
	URL          string

	leases int
}

type inFlightCreate struct {
	done chan struct{}
	path string
	err  error
}

// Lease pins a cache entry against eviction until Release is called. It is
// intended for playback code that passes a cached file to FFmpeg.
type Lease struct {
	Path string

	cache *Cache
	key   string
	once  sync.Once
}

// Release makes a previously acquired entry eligible for eviction again. It
// is safe to call more than once.
func (l *Lease) Release() {
	if l == nil || l.cache == nil {
		return
	}
	l.once.Do(func() {
		l.cache.release(l.key)
	})
}

// NewCache creates a new cache manager.
func NewCache(dir string, maxSize int64) (*Cache, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("cache size must be positive")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	cache := &Cache{
		dir:      dir,
		maxSize:  maxSize,
		entries:  make(map[string]*CacheEntry),
		inFlight: make(map[string]*inFlightCreate),
	}

	if err := cache.loadEntries(); err != nil {
		return nil, err
	}
	return cache, nil
}

// loadEntries loads completed entries and removes crash-left temporary files.
func (c *Cache) loadEntries() error {
	files, err := os.ReadDir(c.dir)
	if err != nil {
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || file.Type()&os.ModeSymlink != 0 {
			continue
		}

		path := filepath.Join(c.dir, file.Name())
		if isPartialName(file.Name()) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale cache partial %q: %w", file.Name(), err)
			}
			continue
		}

		info, err := file.Info()
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}

		c.entries[file.Name()] = &CacheEntry{
			Path:         path,
			Size:         info.Size(),
			LastAccessed: info.ModTime(),
		}
		c.currentSize += info.Size()
	}

	if c.currentSize > c.maxSize {
		if err := c.evictLocked(c.currentSize - c.maxSize); err != nil {
			return fmt.Errorf("shrink cache to configured limit: %w", err)
		}
	}
	return nil
}

// Get gets a cached file path if it exists. Call Acquire when the returned
// file will stay in use while concurrent cache writers may evict entries.
func (c *Cache) Get(key string) (string, bool) {
	if err := validateKey(key); err != nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getLocked(key)
}

// Acquire gets and pins a cached file until the returned lease is released.
func (c *Cache) Acquire(key string) (*Lease, bool) {
	if err := validateKey(key); err != nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	path, ok := c.getLocked(key)
	if !ok {
		return nil, false
	}
	c.entries[key].leases++
	return &Lease{Path: path, cache: c, key: key}, true
}

func (c *Cache) release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok && entry.leases > 0 {
		entry.leases--
	}
}

func (c *Cache) getLocked(key string) (string, bool) {
	entry, exists := c.entries[key]
	if !exists {
		return "", false
	}

	info, err := os.Stat(entry.Path)
	if err != nil || !info.Mode().IsRegular() {
		c.removeEntryLocked(key)
		return "", false
	}
	if info.Size() != entry.Size {
		c.currentSize += info.Size() - entry.Size
		entry.Size = info.Size()
	}
	c.touchLocked(entry)
	return entry.Path, true
}

func (c *Cache) touchLocked(entry *CacheEntry) {
	now := time.Now()
	entry.LastAccessed = now
	// Persist LRU access where the filesystem permits it. A cache remains
	// usable on read-only mounts even when updating atime is unavailable.
	if err := os.Chtimes(entry.Path, now, now); err != nil {
		// LastAccessed remains authoritative in this process. Persisting it is
		// opportunistic because a read-only cache mount must still be readable.
		return
	}
}

// Set adds a completed file to the cache. The actual source size is
// authoritative; the legacy size parameter is retained for API compatibility.
func (c *Cache) Set(key, sourcePath string, _ int64) error {
	if err := validateKey(key); err != nil {
		return err
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("stat cache source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cache source is not a regular file")
	}
	if info.Size() > c.maxSize {
		return fmt.Errorf("%w: %d bytes exceeds %d bytes", ErrEntryTooLarge, info.Size(), c.maxSize)
	}

	tmpPath, err := c.tempPath(key)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	if err := copyFile(sourcePath, tmpPath); err != nil {
		return fmt.Errorf("failed to copy file to cache: %w", err)
	}
	_, err = c.commitTemp(key, tmpPath, info.Size())
	return err
}

// GetOrCreate gets a cached file or creates it using the supplied function.
// Concurrent requests for one key share a single creator and every creator
// writes to a temporary file that is atomically renamed only after completion.
func (c *Cache) GetOrCreate(key string, create func(path string) error) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	if create == nil {
		return "", fmt.Errorf("cache creator is required")
	}

	c.mu.Lock()
	if path, exists := c.getLocked(key); exists {
		c.mu.Unlock()
		return path, nil
	}
	if pending, exists := c.inFlight[key]; exists {
		c.mu.Unlock()
		<-pending.done
		return pending.path, pending.err
	}
	pending := &inFlightCreate{done: make(chan struct{})}
	c.inFlight[key] = pending
	c.mu.Unlock()

	path, err := c.createAndStore(key, create)

	c.mu.Lock()
	pending.path = path
	pending.err = err
	delete(c.inFlight, key)
	close(pending.done)
	c.mu.Unlock()

	return path, err
}

func (c *Cache) createAndStore(key string, create func(path string) error) (string, error) {
	tmpPath, err := c.tempPath(key)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpPath)

	if err := create(tmpPath); err != nil {
		return "", fmt.Errorf("failed to create cached file: %w", err)
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat created file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("created cache file is not regular")
	}
	if info.Size() > c.maxSize {
		return "", fmt.Errorf("%w: %d bytes exceeds %d bytes", ErrEntryTooLarge, info.Size(), c.maxSize)
	}

	return c.commitTemp(key, tmpPath, info.Size())
}

func (c *Cache) commitTemp(key, tmpPath string, size int64) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.entries[key]; exists {
		c.touchLocked(entry)
		return entry.Path, nil
	}
	if size > c.maxSize {
		return "", fmt.Errorf("%w: %d bytes exceeds %d bytes", ErrEntryTooLarge, size, c.maxSize)
	}
	if required := c.currentSize + size - c.maxSize; required > 0 {
		if err := c.evictLocked(required); err != nil {
			return "", fmt.Errorf("make room for cache entry: %w", err)
		}
	}

	destPath := filepath.Join(c.dir, key)
	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("failed to finalize cached file: %w", err)
	}
	c.entries[key] = &CacheEntry{
		Path:         destPath,
		Size:         size,
		LastAccessed: time.Now(),
	}
	c.currentSize += size
	return destPath, nil
}

// evictLocked removes least-recently-used, unleased entries until targetSize
// bytes have been freed. c.mu must be held by the caller.
func (c *Cache) evictLocked(targetSize int64) error {
	if targetSize <= 0 {
		return nil
	}
	type entrySort struct {
		key   string
		entry *CacheEntry
	}

	entries := make([]entrySort, 0, len(c.entries))
	for key, entry := range c.entries {
		if entry.leases == 0 {
			entries = append(entries, entrySort{key: key, entry: entry})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].entry.LastAccessed.Before(entries[j].entry.LastAccessed)
	})

	var (
		freed int64
		errs  []error
	)
	for _, candidate := range entries {
		if freed >= targetSize {
			break
		}
		err := os.Remove(candidate.entry.Path)
		if err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove cache entry %q: %w", candidate.key, err))
			continue
		}
		freed += candidate.entry.Size
		c.removeEntryLocked(candidate.key)
	}

	if freed < targetSize {
		errs = append(errs, fmt.Errorf("cache capacity unavailable: need %d more bytes", targetSize-freed))
	}
	return errors.Join(errs...)
}

func (c *Cache) removeEntryLocked(key string) {
	entry, ok := c.entries[key]
	if !ok {
		return
	}
	c.currentSize -= entry.Size
	if c.currentSize < 0 {
		c.currentSize = 0
	}
	delete(c.entries, key)
}

// Clear removes all unleased cache entries. Active leases are retained rather
// than unlinking a file while FFmpeg may still be opening it.
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error
	for key, entry := range c.entries {
		if entry.leases > 0 {
			errs = append(errs, fmt.Errorf("%w: %q", ErrEntryLeased, key))
			continue
		}
		if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove cache entry %q: %w", key, err))
			continue
		}
		c.removeEntryLocked(key)
	}
	return errors.Join(errs...)
}

// GenerateKey generates a stable cache key from a URL. Keep the historical
// SHA-prefix and .webm extension so existing cache entries remain reusable.
func GenerateKey(url string) string {
	hash := sha256.Sum256([]byte(url))
	return fmt.Sprintf("%x.webm", hash[:16])
}

func (c *Cache) tempPath(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.tmp-%d-%d", filepath.Join(c.dir, key), time.Now().UnixNano(), c.tempSequence.Add(1)), nil
}

func validateKey(key string) error {
	if key == "" || key == "." || filepath.IsAbs(key) || filepath.Base(key) != key || strings.ContainsAny(key, `/\\`) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	return nil
}

func isPartialName(name string) bool {
	return strings.Contains(name, ".tmp-") || strings.HasSuffix(name, ".part") || strings.Contains(name, ".part-")
}

// copyFile copies src to dst and syncs the completed temporary destination
// before it is atomically renamed into the cache.
func copyFile(src, dst string) (err error) {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := destFile.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	if _, err = io.Copy(destFile, sourceFile); err != nil {
		return err
	}
	return destFile.Sync()
}

// GetStats returns cache entry count, current size, and configured maximum.
func (c *Cache) GetStats() (int, int64, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries), c.currentSize, c.maxSize
}
