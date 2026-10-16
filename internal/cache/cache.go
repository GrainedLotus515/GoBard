package cache

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
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
	ErrBusy          = errors.New("cache creator budget exhausted")
	ErrIndexLimit    = errors.New("cache index entry limit exceeded")
)

const maxEntries = 65536
const maxCreators = 16

// Cache manages cached audio files. All mutations of entries and currentSize
// are protected by mu; file creation happens outside that lock and is
// committed transactionally once complete.
type Cache struct {
	dir          string
	maxSize      int64
	mu           sync.RWMutex
	entries      map[string]*cacheEntry
	currentSize  int64
	inFlight     map[string]*inFlightCreate
	tempSequence atomic.Uint64
	mutation     chan struct{}
	writer       chan struct{}
	writeFailed  atomic.Bool
}

// cacheEntry represents one indexed file incarnation.
type cacheEntry struct {
	Path         string
	Size         int64
	LastAccessed time.Time
	persistedAt  time.Time

	leases   int
	evicting bool
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
	entry *cacheEntry
	once  sync.Once
}

// Release makes a previously acquired entry eligible for eviction again. It
// is safe to call more than once.
func (l *Lease) Release() {
	if l == nil || l.cache == nil {
		return
	}
	l.once.Do(func() {
		l.cache.release(l.key, l.entry)
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
		entries:  make(map[string]*cacheEntry),
		inFlight: make(map[string]*inFlightCreate),
		mutation: make(chan struct{}, 1),
		writer:   make(chan struct{}, 1),
	}

	if err := cache.loadEntries(); err != nil {
		return nil, err
	}
	return cache, nil
}

// loadEntries scans in bounded batches. A too-large index fails closed without
// silently deleting completed media to meet the metadata budget.
func (c *Cache) loadEntries() error {
	dir, err := os.Open(c.dir)
	if err != nil {
		return fmt.Errorf("open cache directory: %w", err)
	}
	defer dir.Close()
	for {
		files, readErr := dir.ReadDir(256)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("read cache directory: %w", readErr)
		}
		for _, file := range files {
			if file.IsDir() || file.Type()&os.ModeSymlink != 0 {
				continue
			}
			path := filepath.Join(c.dir, file.Name())
			if isPartialName(file.Name()) {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove stale cache partial: %w", err)
				}
				continue
			}
			info, err := file.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if len(c.entries) >= maxEntries {
				return ErrIndexLimit
			}
			if info.Size() > math.MaxInt64-c.currentSize {
				return errors.New("cache size accounting overflow")
			}
			c.entries[file.Name()] = &cacheEntry{Path: path, Size: info.Size(), LastAccessed: info.ModTime(), persistedAt: info.ModTime()}
			c.currentSize += info.Size()
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if c.currentSize > c.maxSize {
		return c.evict(c.currentSize - c.maxSize)
	}
	return nil
}

// Get gets a cached file path if it exists. Call Acquire when the returned
// file will stay in use while concurrent cache writers may evict entries.
func (c *Cache) Get(key string) (string, bool) {
	lease, ok := c.Acquire(key)
	if !ok {
		return "", false
	}
	path := lease.Path
	lease.Release()
	return path, true
}

// Acquire gets and pins a cached file until the returned lease is released.
func (c *Cache) Acquire(key string) (*Lease, bool) {
	if err := validateKey(key); err != nil {
		return nil, false
	}
	c.mu.Lock()
	entry, ok := c.entries[key]
	if !ok || entry.evicting {
		c.mu.Unlock()
		return nil, false
	}
	entry.leases++
	now := time.Now()
	entry.LastAccessed = now
	persistTouch := now.Sub(entry.persistedAt) >= time.Minute
	if persistTouch {
		entry.persistedAt = now
	}
	path := entry.Path
	wantSize := entry.Size
	c.mu.Unlock()

	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		c.mu.Lock()
		if c.entries[key] == entry {
			c.removeEntryLocked(key)
		}
		c.mu.Unlock()
		return nil, false
	}
	if info.Size() != wantSize {
		c.mu.Lock()
		if c.entries[key] == entry {
			c.currentSize += info.Size() - entry.Size
			entry.Size = info.Size()
		}
		c.mu.Unlock()
	}
	if persistTouch {
		// Persisted LRU is deliberately coarse. In-process eviction still uses
		// the exact LastAccessed value above.
		if err := os.Chtimes(path, now, now); err != nil {
			c.mu.Lock()
			if c.entries[key] == entry && entry.persistedAt.Equal(now) {
				entry.persistedAt = time.Time{}
			}
			c.mu.Unlock()
		}
	}
	return &Lease{Path: path, cache: c, key: key, entry: entry}, true
}

func (c *Cache) release(key string, expected *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok && entry == expected && entry.leases > 0 {
		entry.leases--
	}
}

// GetOrCreate gets a cached file or creates it using the supplied function.
// Concurrent requests for one key share a single creator and every creator
// writes to a temporary file that is atomically renamed only after completion.
func (c *Cache) GetOrCreate(key string, create func(path string) error) (string, error) {
	return c.GetOrCreateContext(context.Background(), key, create)
}

// GetOrCreateContext allows a waiter to cancel without canceling another caller's creator.
// A creator runs synchronously and must honor its own caller context.
func (c *Cache) GetOrCreateContext(ctx context.Context, key string, create func(path string) error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateKey(key); err != nil {
		return "", err
	}
	if create == nil {
		return "", fmt.Errorf("cache creator is required")
	}

	if lease, exists := c.Acquire(key); exists {
		path := lease.Path
		lease.Release()
		return path, nil
	}
	c.mu.Lock()
	if pending, exists := c.inFlight[key]; exists {
		c.mu.Unlock()
		select {
		case <-pending.done:
			return pending.path, pending.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if len(c.inFlight) >= maxCreators {
		c.mu.Unlock()
		return "", ErrBusy
	}
	pending := &inFlightCreate{done: make(chan struct{})}
	c.inFlight[key] = pending
	c.mu.Unlock()

	// A preceding creator may have published between the initial lookup and
	// our admission. Recheck after reserving this key to avoid redundant work.
	var path string
	var err error
	if lease, exists := c.Acquire(key); exists {
		path = lease.Path
		lease.Release()
	} else {
		path, err = c.createAndStore(ctx, key, create)
	}

	c.mu.Lock()
	pending.path = path
	pending.err = err
	delete(c.inFlight, key)
	close(pending.done)
	c.mu.Unlock()

	return path, err
}

func (c *Cache) createAndStore(ctx context.Context, key string, create func(path string) error) (string, error) {
	tmpPath, err := c.tempPath(key)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpPath)

	if err := create(tmpPath); err != nil {
		return "", fmt.Errorf("failed to create cached file: %w", err)
	}

	info, err := os.Lstat(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat created file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("created cache file is not regular")
	}
	if info.Size() > c.maxSize {
		return "", fmt.Errorf("%w: %d bytes exceeds %d bytes", ErrEntryTooLarge, info.Size(), c.maxSize)
	}

	return c.commitTemp(ctx, key, tmpPath, info.Size())
}

// commitTemp serializes mutations with a bounded admission gate while leaving
// the metadata mutex available to leases and snapshots during filesystem I/O.
func (c *Cache) commitTemp(ctx context.Context, key, tmpPath string, size int64) (string, error) {
	select {
	case c.mutation <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-c.mutation }()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		entry.LastAccessed = time.Now()
		path := entry.Path
		c.mu.Unlock()
		return path, nil
	}
	if len(c.entries) >= maxEntries {
		c.mu.Unlock()
		return "", ErrIndexLimit
	}
	if size > c.maxSize {
		c.mu.Unlock()
		return "", ErrEntryTooLarge
	}
	required := c.currentSize - (c.maxSize - size)
	c.mu.Unlock()
	if required > 0 {
		if err := c.evict(required); err != nil {
			return "", err
		}
	}
	// External file resizing may have changed accounting during eviction.
	c.mu.RLock()
	fits := c.currentSize <= c.maxSize-size
	c.mu.RUnlock()
	if !fits {
		return "", errors.New("cache capacity changed during commit")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path := filepath.Join(c.dir, key)
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("publish cache entry: %w", err)
	}
	c.mu.Lock()
	c.entries[key] = &cacheEntry{Path: path, Size: size, LastAccessed: time.Now(), persistedAt: time.Now()}
	c.currentSize += size
	c.mu.Unlock()
	return path, nil
}

type candidate struct {
	key      string
	entry    *cacheEntry
	accessed time.Time
}

func (c *Cache) candidates() []candidate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entries := make([]candidate, 0, len(c.entries))
	for key, entry := range c.entries {
		entries = append(entries, candidate{key: key, entry: entry, accessed: entry.LastAccessed})
	}
	return entries
}

// unlink reserves an unleased entry under the metadata mutex, then performs
// removal without that lock. Acquires reject reserved entries.
func (c *Cache) unlink(item candidate) (int64, error) {
	c.mu.Lock()
	if c.entries[item.key] != item.entry {
		c.mu.Unlock()
		return 0, nil
	}
	if item.entry.leases > 0 {
		c.mu.Unlock()
		return 0, ErrEntryLeased
	}
	item.entry.evicting = true
	path, size := item.entry.Path, item.entry.Size
	c.mu.Unlock()
	err := os.Remove(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil && !os.IsNotExist(err) {
		item.entry.evicting = false
		return 0, fmt.Errorf("remove cache entry: %w", err)
	}
	c.removeEntryLocked(item.key)
	return size, nil
}

// evict requires mutation admission (or unpublished constructor ownership).
func (c *Cache) evict(target int64) error {
	entries := c.candidates()
	sort.Slice(entries, func(i, j int) bool { return entries[i].accessed.Before(entries[j].accessed) })
	var freed int64
	var firstErr error
	for _, item := range entries {
		if freed >= target {
			break
		}
		size, err := c.unlink(item)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		freed += size
	}
	if freed < target {
		return errors.Join(firstErr, errors.New("cache capacity unavailable"))
	}
	return nil
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

// Clear removes unleased entries, preserving active decoder leases.
func (c *Cache) Clear() error {
	c.mutation <- struct{}{}
	defer func() { <-c.mutation }()
	var firstErr error
	for _, item := range c.candidates() {
		if _, err := c.unlink(item); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
	if len(key) > 255 || key == "" || key == "." || filepath.IsAbs(key) || filepath.Base(key) != key || strings.ContainsAny(key, `/\\`) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	return nil
}

func isPartialName(name string) bool {
	return strings.Contains(name, ".tmp-") || strings.HasSuffix(name, ".part") || strings.Contains(name, ".part-")
}

// GetStats returns cache entry count, current size, and configured maximum.
func (c *Cache) GetStats() (int, int64, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries), c.currentSize, c.maxSize
}
