package youtube

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/lotus/gobard/internal/player"
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
	FormatID   string `json:"format_id"`
	URL        string `json:"url"`
	Ext        string `json:"ext"`
	AudioCodec string `json:"acodec"`
	VideoCodec string `json:"vcodec"`
}

// Search searches for videos and returns track information
func (c *Client) Search(query string) ([]*player.Track, error) {
	// Use yt-dlp to search and get video info
	cmd := exec.Command(
		"yt-dlp",
		"--dump-json",
		"--no-playlist",
		"--default-search", "ytsearch1",
		query,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to search YouTube: %w", err)
	}

	var result SearchResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse search result: %w", err)
	}

	track := &player.Track{
		ID:        result.ID,
		Title:     result.Title,
		Artist:    result.Uploader,
		URL:       result.URL,
		Duration:  time.Duration(result.Duration) * time.Second,
		Source:    player.SourceYouTube,
		Thumbnail: result.Thumbnail,
		IsLive:    result.IsLive,
	}

	return []*player.Track{track}, nil
}

// GetVideoInfo gets information about a YouTube video
func (c *Client) GetVideoInfo(url string) (*player.Track, error) {
	cmd := exec.Command(
		"yt-dlp",
		"--dump-json",
		"--no-playlist",
		url,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get video info: %w", err)
	}

	var result SearchResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse video info: %w", err)
	}

	track := &player.Track{
		ID:        result.ID,
		Title:     result.Title,
		Artist:    result.Uploader,
		URL:       result.URL,
		Duration:  time.Duration(result.Duration) * time.Second,
		Source:    player.SourceYouTube,
		Thumbnail: result.Thumbnail,
		IsLive:    result.IsLive,
	}

	return track, nil
}

// GetPlaylistInfo gets information about a YouTube playlist
func (c *Client) GetPlaylistInfo(url string) ([]*player.Track, error) {
	cmd := exec.Command(
		"yt-dlp",
		"--dump-json",
		"--flat-playlist",
		url,
	)

	output, err := cmd.Output()
	if err != nil {
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

		track := &player.Track{
			ID:        result.ID,
			Title:     result.Title,
			Artist:    result.Uploader,
			URL:       result.URL,
			Duration:  time.Duration(result.Duration) * time.Second,
			Source:    player.SourceYouTube,
			Thumbnail: result.Thumbnail,
			IsLive:    result.IsLive,
		}

		tracks = append(tracks, track)
	}

	return tracks, nil
}

// Download downloads a video to the cache directory
func (c *Client) Download(url, outputPath string) error {
	// Download in webm format which DCA can encode
	// Don't extract/convert - keep original format for DCA to process
	cmd := exec.Command(
		"yt-dlp",
		"-f", "bestaudio[ext=webm]/bestaudio",
		"--no-post-overwrites",
		"-o", outputPath,
		url,
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download video: %w", err)
	}

	return nil
}

// GetStreamURL gets the direct stream URL for a video
func (c *Client) GetStreamURL(url string) (string, error) {
	cmd := exec.Command(
		"yt-dlp",
		"-f", "bestaudio",
		"-g", // Get URL
		url,
	)

	output, err := cmd.Output()
	if err != nil {
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
