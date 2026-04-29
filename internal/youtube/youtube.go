package youtube

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/player"
)

const (
	searchCacheTTL              = 6 * time.Hour
	negativeSearchCacheTTL      = 5 * time.Minute
	searchCacheCapacity         = 256
	negativeSearchCacheCapacity = 128
)

var execCommandContext = exec.CommandContext

// Client handles YouTube operations
type Client struct {
	apiKey string

	now func() time.Time

	mu                         sync.Mutex
	searchCache                *list.List
	searchCacheEntries         map[string]*list.Element
	negativeSearchCache        *list.List
	negativeSearchCacheEntries map[string]*list.Element
}

type searchCacheEntry struct {
	key      string
	storedAt time.Time
	tracks   []*player.Track
}

type negativeSearchCacheEntry struct {
	key        string
	storedAt   time.Time
	outcome    string
	errMessage string
}

// NewClient creates a new YouTube client
func NewClient(apiKey string) *Client {
	client := &Client{
		apiKey: apiKey,
		now:    time.Now,
	}
	client.initSearchCaches()
	return client
}

// SearchResult represents a YouTube search result from yt-dlp
type SearchResult struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Duration  float64  `json:"duration"`
	Thumbnail string   `json:"thumbnail"`
	Uploader  string   `json:"uploader"`
	URL       string   `json:"webpage_url"`
	IsLive    bool     `json:"is_live"`
	Formats   []Format `json:"formats"`
}

// Format represents an available format
type Format struct {
	FormatID    string            `json:"format_id"`
	URL         string            `json:"url"`
	Ext         string            `json:"ext"`
	AudioCodec  string            `json:"acodec"`
	VideoCodec  string            `json:"vcodec"`
	ABR         float64           `json:"abr"` // Audio bitrate in kbps
	HTTPHeaders map[string]string `json:"http_headers"`
}

type audioStream struct {
	URL       string
	Headers   map[string]string
	ExpiresAt time.Time
}

// extractBestAudioStream finds the best audio-only stream metadata from formats.
func extractBestAudioStream(formats []Format) audioStream {
	var best Format
	var foundBest bool

	for _, f := range formats {
		// Skip if no audio
		if f.AudioCodec == "none" || f.AudioCodec == "" {
			continue
		}
		// Prefer audio-only (no video)
		hasVideo := f.VideoCodec != "none" && f.VideoCodec != ""

		// Select highest bitrate audio-only
		if !hasVideo && f.URL != "" && (!foundBest || f.ABR > best.ABR) {
			best = f
			foundBest = true
		}
	}

	// Fallback: if no audio-only found, take any format with audio
	if !foundBest {
		for _, f := range formats {
			if f.AudioCodec != "none" && f.AudioCodec != "" && f.URL != "" {
				best = f
				foundBest = true
				break
			}
		}
	}

	if !foundBest {
		return audioStream{}
	}

	return audioStream{
		URL:       best.URL,
		Headers:   cloneHeaders(best.HTTPHeaders),
		ExpiresAt: parseStreamExpiry(best.URL),
	}
}

func parseStreamExpiry(rawURL string) time.Time {
	if rawURL == "" {
		return time.Time{}
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return time.Time{}
	}

	expiresAt, err := strconv.ParseInt(parsed.Query().Get("expire"), 10, 64)
	if err != nil || expiresAt <= 0 {
		return time.Time{}
	}

	return time.Unix(expiresAt, 0).UTC()
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}

	return cloned
}

func normalizeSearchKey(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}

func (c *Client) currentTime() time.Time {
	if c != nil && c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Client) initSearchCaches() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initSearchCachesLocked()
}

func (c *Client) initSearchCachesLocked() {
	if c.searchCache == nil {
		c.searchCache = list.New()
	}
	if c.searchCacheEntries == nil {
		c.searchCacheEntries = make(map[string]*list.Element)
	}
	if c.negativeSearchCache == nil {
		c.negativeSearchCache = list.New()
	}
	if c.negativeSearchCacheEntries == nil {
		c.negativeSearchCacheEntries = make(map[string]*list.Element)
	}
}

func buildTrackFromResult(result SearchResult) *player.Track {
	videoURL := result.URL
	if videoURL == "" && result.ID != "" {
		videoURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", result.ID)
	}

	return &player.Track{
		ID:        result.ID,
		Title:     result.Title,
		Artist:    result.Uploader,
		URL:       videoURL,
		Duration:  time.Duration(result.Duration) * time.Second,
		Source:    player.SourceYouTube,
		Thumbnail: result.Thumbnail,
		IsLive:    result.IsLive,
	}
}

func cloneStableTrack(track *player.Track) *player.Track {
	if track == nil {
		return nil
	}

	return &player.Track{
		ID:        track.ID,
		Title:     track.Title,
		Artist:    track.Artist,
		URL:       track.URL,
		Duration:  track.Duration,
		Source:    track.Source,
		Thumbnail: track.Thumbnail,
		IsLive:    track.IsLive,
	}
}

func cloneStableTracks(tracks []*player.Track) []*player.Track {
	cloned := make([]*player.Track, 0, len(tracks))
	for _, track := range tracks {
		if stableTrack := cloneStableTrack(track); stableTrack != nil {
			cloned = append(cloned, stableTrack)
		}
	}
	return cloned
}

func (c *Client) removeSearchCacheElement(element *list.Element) {
	if element == nil {
		return
	}

	entry, _ := element.Value.(*searchCacheEntry)
	if entry != nil {
		delete(c.searchCacheEntries, entry.key)
	}
	c.searchCache.Remove(element)
}

func (c *Client) removeNegativeSearchCacheElement(element *list.Element) {
	if element == nil {
		return
	}

	entry, _ := element.Value.(*negativeSearchCacheEntry)
	if entry != nil {
		delete(c.negativeSearchCacheEntries, entry.key)
	}
	c.negativeSearchCache.Remove(element)
}

func (c *Client) lookupSearchCache(key string, now time.Time) ([]*player.Track, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.initSearchCachesLocked()

	element, ok := c.searchCacheEntries[key]
	if !ok {
		return nil, false
	}

	entry, ok := element.Value.(*searchCacheEntry)
	if !ok || entry == nil {
		c.removeSearchCacheElement(element)
		return nil, false
	}

	age := now.Sub(entry.storedAt)
	if age < 0 {
		age = 0
	}

	if age > searchCacheTTL {
		c.removeSearchCacheElement(element)
		logger.Timing("Search cache stale", "query_key", key, "age_ms", age.Milliseconds(), "cache_kind", "positive")
		return nil, false
	}

	c.searchCache.MoveToFront(element)
	logger.Timing("Search cache hit", "query_key", key, "age_ms", age.Milliseconds())
	return cloneStableTracks(entry.tracks), true
}

func (c *Client) lookupNegativeSearchCache(key string, now time.Time) ([]*player.Track, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.initSearchCachesLocked()

	element, ok := c.negativeSearchCacheEntries[key]
	if !ok {
		return nil, nil, false
	}

	entry, ok := element.Value.(*negativeSearchCacheEntry)
	if !ok || entry == nil {
		c.removeNegativeSearchCacheElement(element)
		return nil, nil, false
	}

	age := now.Sub(entry.storedAt)
	if age < 0 {
		age = 0
	}

	if age > negativeSearchCacheTTL {
		c.removeNegativeSearchCacheElement(element)
		logger.Timing("Search cache stale", "query_key", key, "age_ms", age.Milliseconds(), "cache_kind", "negative")
		return nil, nil, false
	}

	c.negativeSearchCache.MoveToFront(element)
	logger.Timing("Search cache negative hit", "query_key", key, "age_ms", age.Milliseconds(), "outcome", entry.outcome)
	if entry.outcome == "empty_result" {
		return []*player.Track{}, nil, true
	}
	if entry.errMessage == "" {
		return nil, nil, true
	}
	return nil, errors.New(entry.errMessage), true
}

func (c *Client) storeSearchCache(key string, tracks []*player.Track, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.initSearchCachesLocked()

	if element, ok := c.searchCacheEntries[key]; ok {
		c.removeSearchCacheElement(element)
	}
	if element, ok := c.negativeSearchCacheEntries[key]; ok {
		c.removeNegativeSearchCacheElement(element)
	}

	entry := &searchCacheEntry{
		key:      key,
		storedAt: now,
		tracks:   cloneStableTracks(tracks),
	}

	element := c.searchCache.PushFront(entry)
	c.searchCacheEntries[key] = element

	for c.searchCache.Len() > searchCacheCapacity {
		c.removeSearchCacheElement(c.searchCache.Back())
	}
}

func (c *Client) storeNegativeSearchCache(key, outcome, errMessage string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.initSearchCachesLocked()

	if element, ok := c.negativeSearchCacheEntries[key]; ok {
		c.removeNegativeSearchCacheElement(element)
	}
	if element, ok := c.searchCacheEntries[key]; ok {
		c.removeSearchCacheElement(element)
	}

	entry := &negativeSearchCacheEntry{
		key:        key,
		storedAt:   now,
		outcome:    outcome,
		errMessage: errMessage,
	}

	element := c.negativeSearchCache.PushFront(entry)
	c.negativeSearchCacheEntries[key] = element

	for c.negativeSearchCache.Len() > negativeSearchCacheCapacity {
		c.removeNegativeSearchCacheElement(c.negativeSearchCache.Back())
	}

	logger.Timing("Search cache negative store", "query_key", key, "outcome", outcome, "age_ms", int64(0))
}

func classifyNegativeSearchFailure(err error) (string, string, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return "", "", false
	}

	rawStderr := strings.TrimSpace(string(exitErr.Stderr))
	if rawStderr == "" {
		return "", "", false
	}
	stderr := strings.ToLower(rawStderr)

	switch {
	case strings.Contains(stderr, "no matches found"),
		strings.Contains(stderr, "did not match any results"),
		strings.Contains(stderr, "no search results"),
		strings.Contains(stderr, "no results found"):
		return "empty_result", "", true
	case strings.Contains(stderr, "unsupported url"),
		strings.Contains(stderr, "incomplete youtube id"),
		strings.Contains(stderr, "invalid url"),
		strings.Contains(stderr, "not a valid url"):
		return "query_error", fmt.Sprintf("failed to search YouTube: %s", rawStderr), true
	default:
		return "", "", false
	}
}

// Search searches for videos and returns track information
func (c *Client) Search(query string) ([]*player.Track, error) {
	start := time.Now()
	now := c.currentTime()
	normalizedKey := normalizeSearchKey(query)

	if normalizedKey != "" {
		if tracks, ok := c.lookupSearchCache(normalizedKey, now); ok {
			return tracks, nil
		}
		if tracks, err, ok := c.lookupNegativeSearchCache(normalizedKey, now); ok {
			return tracks, err
		}
		logger.Timing("Search cache miss", "query_key", normalizedKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := execCommandContext(ctx,
		"yt-dlp",
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
		"--default-search", "ytsearch1",
		"--",
		query,
	)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("search timed out after 30 seconds")
		}
		if normalizedKey != "" {
			if outcome, errMessage, ok := classifyNegativeSearchFailure(err); ok {
				c.storeNegativeSearchCache(normalizedKey, outcome, errMessage, now)
				if outcome == "empty_result" {
					return []*player.Track{}, nil
				}
				return nil, errors.New(errMessage)
			}
		}
		return nil, fmt.Errorf("failed to search YouTube: %w", err)
	}

	trimmedOutput := strings.TrimSpace(string(output))
	if trimmedOutput == "" {
		logger.Timing("YouTube search completed", "query", query, "duration_ms", time.Since(start).Milliseconds(), "has_stream_url", false)
		if normalizedKey != "" {
			c.storeNegativeSearchCache(normalizedKey, "empty_result", "", now)
		}
		return []*player.Track{}, nil
	}

	var result SearchResult
	if err := json.Unmarshal(output, &result); err != nil {
		errMessage := fmt.Sprintf("failed to parse search result: %v", err)
		if normalizedKey != "" {
			c.storeNegativeSearchCache(normalizedKey, "query_error", errMessage, now)
		}
		return nil, fmt.Errorf("failed to parse search result: %w", err)
	}

	track := buildTrackFromResult(result)
	if track.ID == "" && track.Title == "" && track.URL == "" {
		logger.Timing("YouTube search completed", "query", query, "duration_ms", time.Since(start).Milliseconds(), "has_stream_url", false)
		if normalizedKey != "" {
			c.storeNegativeSearchCache(normalizedKey, "empty_result", "", now)
		}
		return []*player.Track{}, nil
	}

	stream := extractBestAudioStream(result.Formats)
	logger.Timing("YouTube search completed", "query", query, "duration_ms", time.Since(start).Milliseconds(), "has_stream_url", stream.URL != "")

	track.SetPrefetchedStream(stream.URL, stream.Headers, stream.ExpiresAt)
	if normalizedKey != "" {
		c.storeSearchCache(normalizedKey, []*player.Track{track}, now)
	}

	return []*player.Track{track}, nil
}

// GetVideoInfo gets information about a YouTube video
func (c *Client) GetVideoInfo(url string) (*player.Track, error) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := execCommandContext(ctx,
		"yt-dlp",
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
		"--",
		url,
	)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("video info fetch timed out after 30 seconds")
		}
		return nil, fmt.Errorf("failed to get video info: %w", err)
	}

	var result SearchResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse video info: %w", err)
	}

	stream := extractBestAudioStream(result.Formats)
	logger.Timing("Video info fetch completed", "url", url, "duration_ms", time.Since(start).Milliseconds(), "has_stream_url", stream.URL != "")

	track := buildTrackFromResult(result)
	track.SetPrefetchedStream(stream.URL, stream.Headers, stream.ExpiresAt)

	return track, nil
}

// GetPlaylistInfo gets information about a YouTube playlist
func (c *Client) GetPlaylistInfo(url string) ([]*player.Track, error) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := execCommandContext(ctx,
		"yt-dlp",
		"--dump-json",
		"--flat-playlist",
		"--no-warnings",
		"--",
		url,
	)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("playlist fetch timed out after 60 seconds")
		}
		return nil, fmt.Errorf("failed to get playlist info: %w", err)
	}

	// yt-dlp outputs one JSON object per line for playlists
	lines := strings.Split(string(output), "\n")
	tracks := make([]*player.Track, 0)

	for _, line := range lines {
		if line == "" {
			continue
		}

		var result SearchResult
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			continue // Skip malformed entries
		}

		track := buildTrackFromResult(result)
		// Drop entries with no resolvable URL/ID (e.g. deleted or
		// private videos that appear in a playlist).
		if track.URL == "" && track.ID == "" {
			continue
		}

		tracks = append(tracks, track)
	}

	logger.Timing("Playlist fetch completed", "url", url, "track_count", len(tracks), "duration_ms", time.Since(start).Milliseconds())

	// Pre-fetch stream URLs for first 3 tracks in parallel
	if len(tracks) > 0 {
		c.prefetchStreamURLs(tracks, 3)
	}

	return tracks, nil
}

// prefetchStreamURLs fetches stream URLs for the first N tracks in parallel.
// Results are collected locally in each goroutine and applied after all
// workers finish to avoid racing with concurrent readers of the tracks.
func (c *Client) prefetchStreamURLs(tracks []*player.Track, count int) {
	if count > len(tracks) {
		count = len(tracks)
	}

	start := time.Now()
	var wg sync.WaitGroup
	var successCount int
	var mu sync.Mutex

	type prefetchResult struct {
		track           *player.Track
		streamURL       string
		streamHeaders   map[string]string
		streamExpiresAt time.Time
		title           string
		artist          string
		ok              bool
	}

	results := make([]prefetchResult, count)

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(track *player.Track, index int) {
			defer wg.Done()

			// Skip if already has stream URL or is live
			if track.StreamURL != "" || track.IsLive || track.URL == "" {
				return
			}

			// Fetch full video info to get stream URL (10 second timeout)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			cmd := execCommandContext(ctx,
				"yt-dlp",
				"--dump-json",
				"--no-playlist",
				"--no-warnings",
				"--",
				track.URL,
			)

			output, err := cmd.Output()
			if err != nil {
				logger.Debug("Prefetch failed for track", "index", index, "title", track.Title, "err", err)
				return // Silently fail, will be fetched later
			}

			var result SearchResult
			if err := json.Unmarshal(output, &result); err != nil {
				return
			}

			stream := extractBestAudioStream(result.Formats)
			res := prefetchResult{track: track, ok: true}
			res.streamURL = stream.URL
			res.streamHeaders = stream.Headers
			res.streamExpiresAt = stream.ExpiresAt
			// Also update title if it was missing from flat playlist
			if track.Title == "" && result.Title != "" {
				res.title = result.Title
			}
			if track.Artist == "" && result.Uploader != "" {
				res.artist = result.Uploader
			}

			mu.Lock()
			results[index] = res
			successCount++
			mu.Unlock()
		}(tracks[i], i)
	}

	wg.Wait()

	// Apply all mutations sequentially now that workers are done.
	for _, res := range results {
		if !res.ok {
			continue
		}
		res.track.SetPrefetchedStream(res.streamURL, res.streamHeaders, res.streamExpiresAt)
		if res.title != "" {
			res.track.Title = res.title
		}
		if res.artist != "" {
			res.track.Artist = res.artist
		}
	}

	logger.Timing("Playlist prefetch completed", "requested", count, "success", successCount, "duration_ms", time.Since(start).Milliseconds())
}

// Download downloads a video to the cache directory
func (c *Client) Download(url, outputPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := execCommandContext(ctx,
		"yt-dlp",
		"-f", "bestaudio[ext=webm]/bestaudio",
		"--no-post-overwrites",
		"--no-warnings",
		"-o", outputPath,
		"--",
		url,
	)

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("download timed out after 5 minutes")
		}
		return fmt.Errorf("failed to download video: %w", err)
	}

	return nil
}

// GetStreamURL gets the direct stream URL for a video
func (c *Client) GetStreamURL(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := execCommandContext(ctx,
		"yt-dlp",
		"-f", "bestaudio",
		"-g", // Get URL
		"--no-warnings",
		"--",
		url,
	)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("stream URL fetch timed out after 30 seconds")
		}
		return "", fmt.Errorf("failed to get stream URL: %w", err)
	}

	stream := strings.TrimSpace(string(output))
	return stream, nil
}

// IsPlaylist checks if a URL is a YouTube playlist.
// It parses the URL and requires a known YouTube host plus a valid
// playlist indicator (either the /playlist path or a non-empty list= query
// parameter).  This avoids false positives on search results or channel
// pages that happen to contain the word "playlist".
func IsPlaylist(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}

	host := strings.ToLower(u.Hostname())
	switch host {
	case "youtube.com", "www.youtube.com", "music.youtube.com":
		if u.Path == "/playlist" {
			return true
		}
	case "youtu.be":
		// Short links never host playlists.
	}

	return strings.TrimSpace(u.Query().Get("list")) != ""
}

// NormalizeSingleVideoURL extracts a locally resolvable YouTube video ID and
// canonicalizes it to the standard watch URL without network access.
func NormalizeSingleVideoURL(raw string) (canonicalURL string, videoID string, ok bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", false
	}

	// Treat playlist-context URLs as non-fast-path inputs so they continue
	// through the existing playlist handling flow.
	if strings.TrimSpace(parsed.Query().Get("list")) != "" {
		return "", "", false
	}

	switch strings.ToLower(parsed.Hostname()) {
	case "youtube.com", "www.youtube.com", "music.youtube.com":
		if parsed.Path != "/watch" {
			return "", "", false
		}

		videoID = strings.TrimSpace(parsed.Query().Get("v"))
		if videoID == "" {
			return "", "", false
		}
	case "youtu.be":
		videoID = strings.Trim(parsed.Path, "/")
		if videoID == "" || strings.Contains(videoID, "/") {
			return "", "", false
		}
	default:
		return "", "", false
	}

	return fmt.Sprintf("https://www.youtube.com/watch?v=%s", url.QueryEscape(videoID)), videoID, true
}

// IsYouTubeURL checks if a URL is a YouTube URL
func IsYouTubeURL(url string) bool {
	return strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be")
}
