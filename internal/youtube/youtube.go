package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/player"
)

// Client handles YouTube operations
type Client struct {
	apiKey string
}

// NewClient creates a new YouTube client
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
	}
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
	FormatID   string  `json:"format_id"`
	URL        string  `json:"url"`
	Ext        string  `json:"ext"`
	AudioCodec string  `json:"acodec"`
	VideoCodec string  `json:"vcodec"`
	ABR        float64 `json:"abr"` // Audio bitrate in kbps
}

// extractBestAudioURL finds the best audio-only URL from formats
func extractBestAudioURL(formats []Format) string {
	var bestURL string
	var bestBitrate float64

	for _, f := range formats {
		// Skip if no audio
		if f.AudioCodec == "none" || f.AudioCodec == "" {
			continue
		}
		// Prefer audio-only (no video)
		hasVideo := f.VideoCodec != "none" && f.VideoCodec != ""

		// Select highest bitrate audio-only
		if !hasVideo && f.ABR > bestBitrate && f.URL != "" {
			bestBitrate = f.ABR
			bestURL = f.URL
		}
	}

	// Fallback: if no audio-only found, take any format with audio
	if bestURL == "" {
		for _, f := range formats {
			if f.AudioCodec != "none" && f.AudioCodec != "" && f.URL != "" {
				return f.URL
			}
		}
	}

	return bestURL
}

// Search searches for videos and returns track information
func (c *Client) Search(query string) ([]*player.Track, error) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx,
		"yt-dlp",
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
		"--default-search", "ytsearch1",
		query,
	)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("search timed out after 30 seconds")
		}
		return nil, fmt.Errorf("failed to search YouTube: %w", err)
	}

	var result SearchResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse search result: %w", err)
	}

	streamURL := extractBestAudioURL(result.Formats)
	logger.Timing("YouTube search completed", "query", query, "duration_ms", time.Since(start).Milliseconds(), "has_stream_url", streamURL != "")

	track := &player.Track{
		ID:        result.ID,
		Title:     result.Title,
		Artist:    result.Uploader,
		URL:       result.URL,
		Duration:  time.Duration(result.Duration) * time.Second,
		Source:    player.SourceYouTube,
		Thumbnail: result.Thumbnail,
		IsLive:    result.IsLive,
		StreamURL: streamURL,
	}

	return []*player.Track{track}, nil
}

// GetVideoInfo gets information about a YouTube video
func (c *Client) GetVideoInfo(url string) (*player.Track, error) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx,
		"yt-dlp",
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
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

	streamURL := extractBestAudioURL(result.Formats)
	logger.Timing("Video info fetch completed", "url", url, "duration_ms", time.Since(start).Milliseconds(), "has_stream_url", streamURL != "")

	track := &player.Track{
		ID:        result.ID,
		Title:     result.Title,
		Artist:    result.Uploader,
		URL:       result.URL,
		Duration:  time.Duration(result.Duration) * time.Second,
		Source:    player.SourceYouTube,
		Thumbnail: result.Thumbnail,
		IsLive:    result.IsLive,
		StreamURL: streamURL,
	}

	return track, nil
}

// GetPlaylistInfo gets information about a YouTube playlist
func (c *Client) GetPlaylistInfo(url string) ([]*player.Track, error) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx,
		"yt-dlp",
		"--dump-json",
		"--flat-playlist",
		"--no-warnings",
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

		// Build video URL from ID if not provided
		videoURL := result.URL
		if videoURL == "" && result.ID != "" {
			videoURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", result.ID)
		}

		track := &player.Track{
			ID:        result.ID,
			Title:     result.Title,
			Artist:    result.Uploader,
			URL:       videoURL,
			Duration:  time.Duration(result.Duration) * time.Second,
			Source:    player.SourceYouTube,
			Thumbnail: result.Thumbnail,
			IsLive:    result.IsLive,
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

// prefetchStreamURLs fetches stream URLs for the first N tracks in parallel
func (c *Client) prefetchStreamURLs(tracks []*player.Track, count int) {
	if count > len(tracks) {
		count = len(tracks)
	}

	start := time.Now()
	var wg sync.WaitGroup
	var successCount int
	var mu sync.Mutex

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

			cmd := exec.CommandContext(ctx,
				"yt-dlp",
				"--dump-json",
				"--no-playlist",
				"--no-warnings",
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

			track.StreamURL = extractBestAudioURL(result.Formats)
			// Also update title if it was missing from flat playlist
			if track.Title == "" && result.Title != "" {
				track.Title = result.Title
			}
			if track.Artist == "" && result.Uploader != "" {
				track.Artist = result.Uploader
			}

			mu.Lock()
			successCount++
			mu.Unlock()
		}(tracks[i], i)
	}

	wg.Wait()
	logger.Timing("Playlist prefetch completed", "requested", count, "success", successCount, "duration_ms", time.Since(start).Milliseconds())
}

// Download downloads a video to the cache directory
func (c *Client) Download(url, outputPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx,
		"yt-dlp",
		"-f", "bestaudio[ext=webm]/bestaudio",
		"--no-post-overwrites",
		"--no-warnings",
		"-o", outputPath,
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

	cmd := exec.CommandContext(ctx,
		"yt-dlp",
		"-f", "bestaudio",
		"-g", // Get URL
		"--no-warnings",
		url,
	)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("stream URL fetch timed out after 30 seconds")
		}
		return "", fmt.Errorf("failed to get stream URL: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// IsPlaylist checks if a URL is a playlist
func IsPlaylist(url string) bool {
	return strings.Contains(url, "playlist") || strings.Contains(url, "list=")
}

// IsYouTubeURL checks if a URL is a YouTube URL
func IsYouTubeURL(url string) bool {
	return strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be")
}
