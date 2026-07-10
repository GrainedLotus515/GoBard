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
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/sourceurl"
)

const (
	searchCacheTTL              = 6 * time.Hour
	negativeSearchCacheTTL      = 5 * time.Minute
	searchCacheCapacity         = 256
	negativeSearchCacheCapacity = 128
	defaultMaxPlaylistTracks    = 500
	defaultMaxConcurrency       = 4
)

var execCommandContext = exec.CommandContext

// Client handles YouTube operations
type Client struct {
	now func() time.Time

	maxPlaylistTracks int
	commandLimiter    *processlimit.Limiter

	mu                         sync.Mutex
	searchCache                *list.List
	searchCacheEntries         map[string]*list.Element
	negativeSearchCache        *list.List
	negativeSearchCacheEntries map[string]*list.Element
}

// Options controls bounded external-tool use for a YouTube client.
type Options struct {
	MaxPlaylistTracks int
	MaxConcurrency    int
	ProcessLimiter    *processlimit.Limiter
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
func NewClient() *Client {
	return NewClientWithOptions(Options{})
}

// NewClientWithOptions creates a client with a shared bounded yt-dlp process
// budget. MaxConcurrency is retained for isolated construction paths: when a
// caller does not inject a limiter, it configures the process-wide budget so
// playback fallback uses the same cap rather than a second semaphore.
func NewClientWithOptions(options Options) *Client {
	if options.MaxPlaylistTracks < 1 || options.MaxPlaylistTracks > defaultMaxPlaylistTracks {
		options.MaxPlaylistTracks = defaultMaxPlaylistTracks
	}
	if options.ProcessLimiter == nil && options.MaxConcurrency != 0 {
		if options.MaxConcurrency < 1 || options.MaxConcurrency > 16 {
			options.MaxConcurrency = defaultMaxConcurrency
		}
		processlimit.ConfigureGlobal(options.MaxConcurrency)
	}
	limiter := options.ProcessLimiter
	if limiter == nil {
		limiter = processlimit.Global()
	}
	client := &Client{
		now:               time.Now,
		maxPlaylistTracks: options.MaxPlaylistTracks,
		commandLimiter:    limiter,
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

// URLKind identifies the supported YouTube resource represented by a
// canonical URL. GoBard intentionally accepts only video and playlist URLs;
// accepting arbitrary extractor URLs turns yt-dlp into an SSRF primitive.
type URLKind uint8

const (
	URLKindUnknown URLKind = iota
	URLKindVideo
	URLKindPlaylist
)

var (
	errUnsupportedYouTubeURL = errors.New("unsupported YouTube URL")
)

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
		if !hasVideo && isPublicHTTPURL(f.URL) && (!foundBest || f.ABR > best.ABR) {
			best = f
			foundBest = true
		}
	}

	// Fallback: if no audio-only found, take any format with audio
	if !foundBest {
		for _, f := range formats {
			if f.AudioCodec != "none" && f.AudioCodec != "" && isPublicHTTPURL(f.URL) {
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

func (c *Client) acquireCommandSlot(ctx context.Context) (func(), error) {
	if c == nil {
		return nil, fmt.Errorf("YouTube client is not initialized")
	}
	limiter := c.commandLimiter
	if limiter == nil {
		// Preserve safe behavior for manually-constructed clients in tests while
		// still sharing the production process budget.
		limiter = processlimit.Global()
	}
	return limiter.Acquire(ctx)
}

func (c *Client) playlistLimit() int {
	if c == nil || c.maxPlaylistTracks < 1 || c.maxPlaylistTracks > defaultMaxPlaylistTracks {
		return defaultMaxPlaylistTracks
	}
	return c.maxPlaylistTracks
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
	videoURL := ""
	if canonicalURL, kind, err := ClassifyYouTubeURL(result.URL); err == nil && kind == URLKindVideo {
		videoURL = canonicalURL
	}
	if videoURL == "" && isSafeYouTubeIdentifier(result.ID, 32) {
		videoURL = canonicalVideoURL(result.ID)
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

	entry, ok := element.Value.(*searchCacheEntry)
	if ok && entry != nil {
		delete(c.searchCacheEntries, entry.key)
	}
	c.searchCache.Remove(element)
}

func (c *Client) removeNegativeSearchCacheElement(element *list.Element) {
	if element == nil {
		return
	}

	entry, ok := element.Value.(*negativeSearchCacheEntry)
	if ok && entry != nil {
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
		logger.Timing("Search cache stale", "age_ms", age.Milliseconds(), "cache_kind", "positive")
		return nil, false
	}

	c.searchCache.MoveToFront(element)
	logger.Timing("Search cache hit", "age_ms", age.Milliseconds())
	return cloneStableTracks(entry.tracks), true
}

func (c *Client) lookupNegativeSearchCache(key string, now time.Time) ([]*player.Track, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.initSearchCachesLocked()

	element, ok := c.negativeSearchCacheEntries[key]
	if !ok {
		return nil, false, nil
	}

	entry, ok := element.Value.(*negativeSearchCacheEntry)
	if !ok || entry == nil {
		c.removeNegativeSearchCacheElement(element)
		return nil, false, nil
	}

	age := now.Sub(entry.storedAt)
	if age < 0 {
		age = 0
	}

	if age > negativeSearchCacheTTL {
		c.removeNegativeSearchCacheElement(element)
		logger.Timing("Search cache stale", "age_ms", age.Milliseconds(), "cache_kind", "negative")
		return nil, false, nil
	}

	c.negativeSearchCache.MoveToFront(element)
	logger.Timing("Search cache negative hit", "age_ms", age.Milliseconds(), "outcome", entry.outcome)
	if entry.outcome == "empty_result" {
		return []*player.Track{}, true, nil
	}
	if entry.errMessage == "" {
		return nil, true, nil
	}
	return nil, true, errors.New(entry.errMessage)
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

	logger.Timing("Search cache negative store", "outcome", outcome, "age_ms", int64(0))
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
		// Extractor stderr can reflect the submitted query. Preserve the
		// classification but never surface potentially sensitive input.
		return "query_error", "failed to search YouTube", true
	default:
		return "", "", false
	}
}

// Search searches for videos and returns track information
func (c *Client) Search(query string) ([]*player.Track, error) {
	if IsURLLike(query) {
		return nil, fmt.Errorf("%w: use an HTTPS YouTube video or playlist URL", errUnsupportedYouTubeURL)
	}

	start := time.Now()
	now := c.currentTime()
	normalizedKey := normalizeSearchKey(query)

	if normalizedKey != "" {
		if tracks, ok := c.lookupSearchCache(normalizedKey, now); ok {
			return tracks, nil
		}
		if tracks, ok, err := c.lookupNegativeSearchCache(normalizedKey, now); ok {
			return tracks, err
		}
		logger.Timing("Search cache miss")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	releaseCommandSlot, err := c.acquireCommandSlot(ctx)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("search timed out after 30 seconds")
		}
		return nil, fmt.Errorf("acquire YouTube search slot: %w", err)
	}
	defer releaseCommandSlot()

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
		logger.Timing("YouTube search completed", "query_length", len(query), "duration_ms", time.Since(start).Milliseconds(), "has_stream_url", false)
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
		logger.Timing("YouTube search completed", "query_length", len(query), "duration_ms", time.Since(start).Milliseconds(), "has_stream_url", false)
		if normalizedKey != "" {
			c.storeNegativeSearchCache(normalizedKey, "empty_result", "", now)
		}
		return []*player.Track{}, nil
	}

	stream := extractBestAudioStream(result.Formats)
	logger.Timing("YouTube search completed", "query_length", len(query), "duration_ms", time.Since(start).Milliseconds(), "has_stream_url", stream.URL != "")

	track.SetPrefetchedStream(stream.URL, stream.Headers, stream.ExpiresAt)
	if normalizedKey != "" {
		c.storeSearchCache(normalizedKey, []*player.Track{track}, now)
	}

	return []*player.Track{track}, nil
}

// GetVideoInfo gets information about a YouTube video
func (c *Client) GetVideoInfo(rawURL string) (*player.Track, error) {
	return c.GetVideoInfoContext(context.Background(), rawURL)
}

// GetVideoInfoContext gets video metadata while honoring cancellation from a
// caller-owned operation such as background fast-path hydration.
func (c *Client) GetVideoInfoContext(parent context.Context, rawURL string) (*player.Track, error) {
	url, kind, err := ClassifyYouTubeURL(rawURL)
	if err != nil {
		return nil, err
	}
	if kind != URLKindVideo {
		return nil, fmt.Errorf("%w: expected a video URL", errUnsupportedYouTubeURL)
	}

	start := time.Now()

	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	releaseCommandSlot, err := c.acquireCommandSlot(ctx)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("video info fetch timed out after 30 seconds")
		}
		return nil, fmt.Errorf("acquire video info slot: %w", err)
	}
	defer releaseCommandSlot()

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
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("video info fetch canceled")
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
func (c *Client) GetPlaylistInfo(rawURL string) ([]*player.Track, error) {
	url, kind, err := ClassifyYouTubeURL(rawURL)
	if err != nil {
		return nil, err
	}
	if kind != URLKindPlaylist {
		return nil, fmt.Errorf("%w: expected a playlist URL", errUnsupportedYouTubeURL)
	}

	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	releaseCommandSlot, err := c.acquireCommandSlot(ctx)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("playlist fetch timed out after 60 seconds")
		}
		return nil, fmt.Errorf("acquire playlist fetch slot: %w", err)
	}
	defer releaseCommandSlot()

	cmd := execCommandContext(ctx,
		"yt-dlp",
		"--dump-json",
		"--flat-playlist",
		"--playlist-end", strconv.Itoa(c.playlistLimit()),
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
		if len(tracks) >= c.playlistLimit() {
			break
		}
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
			releaseCommandSlot, err := c.acquireCommandSlot(ctx)
			if err != nil {
				logger.Debug("Prefetch slot unavailable", "index", index, "title", track.Title, "err", err)
				return
			}
			defer releaseCommandSlot()

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
func (c *Client) Download(rawURL, outputPath string) error {
	return c.DownloadContext(context.Background(), rawURL, outputPath)
}

// DownloadContext downloads a video while honoring cancellation from playback
// lifecycle events. A canceled cache fill is discarded transactionally by the
// cache's temporary-file creator.
func (c *Client) DownloadContext(parent context.Context, rawURL, outputPath string) error {
	url, kind, err := ClassifyYouTubeURL(rawURL)
	if err != nil {
		return err
	}
	if kind != URLKindVideo {
		return fmt.Errorf("%w: expected a video URL", errUnsupportedYouTubeURL)
	}

	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	releaseCommandSlot, err := c.acquireCommandSlot(ctx)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("download timed out after 5 minutes")
		}
		return fmt.Errorf("acquire download slot: %w", err)
	}
	defer releaseCommandSlot()

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
		if ctx.Err() == context.Canceled {
			return fmt.Errorf("download canceled")
		}
		return fmt.Errorf("failed to download video: %w", err)
	}

	return nil
}

// ClassifyYouTubeURL validates an externally supplied URL and returns a
// canonical video or playlist URL. It intentionally accepts a small, explicit
// grammar rather than passing arbitrary URLs through to yt-dlp.
func ClassifyYouTubeURL(raw string) (canonicalURL string, kind URLKind, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", URLKindUnknown, fmt.Errorf("%w: URL is empty", errUnsupportedYouTubeURL)
	}

	parsed, parseErr := url.ParseRequestURI(trimmed)
	if parseErr != nil || parsed == nil {
		return "", URLKindUnknown, fmt.Errorf("%w: invalid URL", errUnsupportedYouTubeURL)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", URLKindUnknown, fmt.Errorf("%w: only HTTPS URLs are accepted", errUnsupportedYouTubeURL)
	}
	if parsed.User != nil {
		return "", URLKindUnknown, fmt.Errorf("%w: userinfo is not allowed", errUnsupportedYouTubeURL)
	}
	if parsed.Host == "" || (parsed.Port() != "" && parsed.Port() != "443") {
		return "", URLKindUnknown, fmt.Errorf("%w: invalid host", errUnsupportedYouTubeURL)
	}

	host := strings.ToLower(parsed.Hostname())
	if !isYouTubeHost(host) {
		return "", URLKindUnknown, fmt.Errorf("%w: host is not YouTube", errUnsupportedYouTubeURL)
	}

	query := parsed.Query()
	playlistID := strings.TrimSpace(query.Get("list"))
	if playlistID != "" {
		if !isSafeYouTubeIdentifier(playlistID, 128) {
			return "", URLKindUnknown, fmt.Errorf("%w: invalid playlist identifier", errUnsupportedYouTubeURL)
		}
		if host == "youtu.be" {
			shortID := strings.Trim(parsed.EscapedPath(), "/")
			if strings.Contains(shortID, "/") {
				return "", URLKindUnknown, fmt.Errorf("%w: invalid short playlist path", errUnsupportedYouTubeURL)
			}
			unescaped, unescapeErr := url.PathUnescape(shortID)
			if unescapeErr != nil || !isSafeYouTubeIdentifier(unescaped, 32) {
				return "", URLKindUnknown, fmt.Errorf("%w: invalid short playlist path", errUnsupportedYouTubeURL)
			}
		} else if parsed.Path != "/playlist" && parsed.Path != "/watch" {
			return "", URLKindUnknown, fmt.Errorf("%w: expected /watch or /playlist", errUnsupportedYouTubeURL)
		}
		return canonicalPlaylistURL(playlistID), URLKindPlaylist, nil
	}

	var videoID string
	switch host {
	case "youtube.com", "www.youtube.com", "music.youtube.com":
		if parsed.Path != "/watch" {
			return "", URLKindUnknown, fmt.Errorf("%w: expected /watch or a playlist", errUnsupportedYouTubeURL)
		}
		videoID = strings.TrimSpace(query.Get("v"))
	case "youtu.be":
		videoID = strings.Trim(parsed.EscapedPath(), "/")
		if strings.Contains(videoID, "/") {
			return "", URLKindUnknown, fmt.Errorf("%w: invalid short video path", errUnsupportedYouTubeURL)
		}
		unescaped, unescapeErr := url.PathUnescape(videoID)
		if unescapeErr != nil {
			return "", URLKindUnknown, fmt.Errorf("%w: invalid short video path", errUnsupportedYouTubeURL)
		}
		videoID = unescaped
	}

	if !isSafeYouTubeIdentifier(videoID, 32) {
		return "", URLKindUnknown, fmt.Errorf("%w: invalid video identifier", errUnsupportedYouTubeURL)
	}
	return canonicalVideoURL(videoID), URLKindVideo, nil
}

// IsPlaylist reports whether raw is a supported YouTube playlist URL.
func IsPlaylist(raw string) bool {
	_, kind, err := ClassifyYouTubeURL(raw)
	return err == nil && kind == URLKindPlaylist
}

// NormalizeSingleVideoURL extracts a locally resolvable YouTube video ID and
// canonicalizes it to the standard watch URL without network access.
func NormalizeSingleVideoURL(raw string) (canonicalURL string, videoID string, ok bool) {
	canonicalURL, kind, err := ClassifyYouTubeURL(raw)
	if err != nil || kind != URLKindVideo {
		return "", "", false
	}

	parsed, err := url.Parse(canonicalURL)
	if err != nil {
		return "", "", false
	}
	return canonicalURL, parsed.Query().Get("v"), true
}

// IsYouTubeURL reports whether raw is one of the supported HTTPS YouTube URL
// forms. It does not use substring matching, which previously allowed hosts
// such as 127.0.0.1/youtube.com to reach yt-dlp.
func IsYouTubeURL(raw string) bool {
	_, _, err := ClassifyYouTubeURL(raw)
	return err == nil
}

func isYouTubeHost(host string) bool {
	switch host {
	case "youtube.com", "www.youtube.com", "music.youtube.com", "youtu.be":
		return true
	default:
		return false
	}
}

func canonicalVideoURL(videoID string) string {
	return "https://www.youtube.com/watch?v=" + url.QueryEscape(videoID)
}

func canonicalPlaylistURL(playlistID string) string {
	return "https://www.youtube.com/playlist?list=" + url.QueryEscape(playlistID)
}

func isSafeYouTubeIdentifier(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// looksLikeURL prevents an unsupported explicit URL from becoming a YouTube
// search query. Ordinary search phrases with punctuation remain valid.
// IsURLLike reports whether raw is explicitly URL-shaped. It is used by the
// command layer to reject unsupported URLs instead of treating them as a
// YouTube search query.
func IsURLLike(raw string) bool {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "//") || strings.Contains(value, "://") {
		return true
	}
	if strings.HasPrefix(value, "file:") || strings.HasPrefix(value, "spotify:") {
		return true
	}
	for _, host := range []string{"youtube.com/", "www.youtube.com/", "music.youtube.com/", "youtu.be/", "www.spotify.com/", "open.spotify.com/"} {
		if strings.HasPrefix(value, host) {
			return true
		}
	}
	return false
}

// isPublicHTTPURL is a defence-in-depth check for yt-dlp-generated stream
// URLs. The extractor supplies these URLs, but accepting loopback or private
// literal addresses would still make the media pipeline an SSRF hop.
func isPublicHTTPURL(raw string) bool {
	return sourceurl.IsPublicHTTPURL(raw)
}
