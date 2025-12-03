package cache

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cache manages cached audio files
type Cache struct {
	dir     string
	maxSize int64
	mu      sync.RWMutex
	entries map[string]*CacheEntry
}

// CacheEntry represents a cached file
type CacheEntry struct {
	Path         string
	Size         int64
	LastAccessed time.Time
	URL          string
}

// NewCache creates a new cache manager
func NewCache(dir string, maxSize int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	cache := &Cache{
		dir:     dir,
		maxSize: maxSize,
		entries: make(map[string]*CacheEntry),
	}

	// Load existing cache entries
	if err := cache.loadEntries(); err != nil {
		return nil, err
	}

	return cache, nil
}

// loadEntries loads existing cache entries from disk
func (c *Cache) loadEntries() error {
	files, err := os.ReadDir(c.dir)
	if err != nil {
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	var totalSize int64

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		info, err := file.Info()
		if err != nil {
			continue
		}

		path := filepath.Join(c.dir, file.Name())
		c.entries[file.Name()] = &CacheEntry{
			Path:         path,
			Size:         info.Size(),
			LastAccessed: info.ModTime(),
		}

		totalSize += info.Size()
	}

	// Evict old entries if cache is too large
	if totalSize > c.maxSize {
		c.evict(totalSize - c.maxSize)
	}

	return nil
}

// Get gets a cached file path if it exists
func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return "", false
	}

	// Update access time
	entry.LastAccessed = time.Now()

	// Verify file still exists
	if _, err := os.Stat(entry.Path); os.IsNotExist(err) {
		delete(c.entries, key)
		return "", false
	}

	return entry.Path, true
}

// Set adds a file to the cache
func (c *Cache) Set(key, sourcePath string, size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict if necessary
	currentSize := c.getCurrentSize()
	if currentSize+size > c.maxSize {
		c.evict(currentSize + size - c.maxSize)
	}

	destPath := filepath.Join(c.dir, key)

	// Copy file to cache
	if err := copyFile(sourcePath, destPath); err != nil {
		return fmt.Errorf("failed to copy file to cache: %w", err)
	}

	c.entries[key] = &CacheEntry{
		Path:         destPath,
		Size:         size,
		LastAccessed: time.Now(),
	}

	return nil
}

// GetOrCreate gets a cached file or creates it using the provided function
func (c *Cache) GetOrCreate(key string, create func(path string) error) (string, error) {
	// Check if already cached
	if path, exists := c.Get(key); exists {
		return path, nil
	}

	destPath := filepath.Join(c.dir, key)

	// Create the file WITHOUT holding the lock
	// This allows other cache operations to proceed during download
	if err := create(destPath); err != nil {
		return "", fmt.Errorf("failed to create cached file: %w", err)
	}

	// Get file size
	info, err := os.Stat(destPath)
	if err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("failed to stat created file: %w", err)
	}

	size := info.Size()

	// NOW acquire lock only for registration
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if another goroutine already created this entry while we were downloading
	if entry, exists := c.entries[key]; exists {
		// Remove our duplicate download
		os.Remove(destPath)
		return entry.Path, nil
	}

	// Evict if necessary
	currentSize := c.getCurrentSize()
	if currentSize+size > c.maxSize {
		c.evict(currentSize + size - c.maxSize)
	}

	c.entries[key] = &CacheEntry{
		Path:         destPath,
		Size:         size,
		LastAccessed: time.Now(),
	}

	return destPath, nil
}

// evict removes old cache entries to free up space
func (c *Cache) evict(targetSize int64) {
	// Sort entries by last accessed time
	type entrySort struct {
		key   string
		entry *CacheEntry
	}

	entries := make([]entrySort, 0, len(c.entries))
	for key, entry := range c.entries {
		entries = append(entries, entrySort{key, entry})
	}

	// Sort by last accessed (oldest first)
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].entry.LastAccessed.After(entries[j].entry.LastAccessed) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	var freedSize int64
	for _, e := range entries {
		if freedSize >= targetSize {
			break
		}

		// Delete file
		os.Remove(e.entry.Path)
		freedSize += e.entry.Size
		delete(c.entries, e.key)
	}
}

// getCurrentSize returns the current total cache size
func (c *Cache) getCurrentSize() int64 {
	var total int64
	for _, entry := range c.entries {
		total += entry.Size
	}
	return total
}

// Clear removes all cache entries
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		os.Remove(entry.Path)
		delete(c.entries, key)
	}

	return nil
}

// GenerateKey generates a cache key from a URL
func GenerateKey(url string) string {
	hash := sha256.Sum256([]byte(url))
	// Use .webm extension for cached audio files (yt-dlp downloads webm format)
	return fmt.Sprintf("%x.webm", hash[:16])
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// GetStats returns cache statistics
func (c *Cache) GetStats() (int, int64, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	count := len(c.entries)
	size := c.getCurrentSize()
	return count, size, c.maxSize
}
