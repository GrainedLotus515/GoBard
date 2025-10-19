package spotify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lotus/gobard/internal/player"
	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2/clientcredentials"
)

// Client handles Spotify operations
type Client struct {
	client *spotify.Client
	ctx    context.Context
}

// NewClient creates a new Spotify client
func NewClient(clientID, clientSecret string) (*Client, error) {
	ctx := context.Background()

	config := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     spotifyauth.TokenURL,
	}

	token, err := config.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Spotify token: %w", err)
	}

	httpClient := spotifyauth.New().Client(ctx, token)
	client := spotify.New(httpClient)

	return &Client{
		client: client,
		ctx:    ctx,
	}, nil
}

// GetTrackInfo gets information about a Spotify track
func (c *Client) GetTrackInfo(trackID string) (*player.Track, error) {
	track, err := c.client.GetTrack(c.ctx, spotify.ID(trackID))
	if err != nil {
		return nil, fmt.Errorf("failed to get track info: %w", err)
	}

	artists := make([]string, len(track.Artists))
	for i, artist := range track.Artists {
		artists[i] = artist.Name
	}

	return &player.Track{
		ID:       track.ID.String(),
		Title:    track.Name,
		Artist:   strings.Join(artists, ", "),
		Duration: time.Duration(track.Duration) * time.Millisecond,
		Source:   player.SourceSpotify,
		URL:      track.ExternalURLs["spotify"],
	}, nil
}

// GetPlaylistTracks gets all tracks from a Spotify playlist
func (c *Client) GetPlaylistTracks(playlistID string) ([]*player.Track, error) {
	tracks := make([]*player.Track, 0)

	offset := 0
	limit := 100

	for {
		page, err := c.client.GetPlaylistItems(
			c.ctx,
			spotify.ID(playlistID),
			spotify.Limit(limit),
			spotify.Offset(offset),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to get playlist tracks: %w", err)
		}

		for _, item := range page.Items {
			if item.Track.Track == nil {
				continue
			}

			track := item.Track.Track
			artists := make([]string, len(track.Artists))
			for i, artist := range track.Artists {
				artists[i] = artist.Name
			}

			tracks = append(tracks, &player.Track{
				ID:       track.ID.String(),
				Title:    track.Name,
				Artist:   strings.Join(artists, ", "),
				Duration: time.Duration(track.Duration) * time.Millisecond,
				Source:   player.SourceSpotify,
				URL:      track.ExternalURLs["spotify"],
			})
		}

		if len(page.Items) < limit {
			break
		}

		offset += limit
	}

	return tracks, nil
}

// GetAlbumTracks gets all tracks from a Spotify album
func (c *Client) GetAlbumTracks(albumID string) ([]*player.Track, error) {
	album, err := c.client.GetAlbum(c.ctx, spotify.ID(albumID))
	if err != nil {
		return nil, fmt.Errorf("failed to get album: %w", err)
	}

	tracks := make([]*player.Track, 0)

	for _, track := range album.Tracks.Tracks {
		artists := make([]string, len(track.Artists))
		for i, artist := range track.Artists {
			artists[i] = artist.Name
		}

		tracks = append(tracks, &player.Track{
			ID:       track.ID.String(),
			Title:    track.Name,
			Artist:   strings.Join(artists, ", "),
			Duration: time.Duration(track.Duration) * time.Millisecond,
			Source:   player.SourceSpotify,
			URL:      track.ExternalURLs["spotify"],
		})
	}

	return tracks, nil
}

// GetArtistTopTracks gets an artist's top tracks
func (c *Client) GetArtistTopTracks(artistID string) ([]*player.Track, error) {
	topTracks, err := c.client.GetArtistsTopTracks(c.ctx, spotify.ID(artistID), "US")
	if err != nil {
		return nil, fmt.Errorf("failed to get artist top tracks: %w", err)
	}

	tracks := make([]*player.Track, 0)

	for _, track := range topTracks {
		artists := make([]string, len(track.Artists))
		for i, artist := range track.Artists {
			artists[i] = artist.Name
		}

		tracks = append(tracks, &player.Track{
			ID:       track.ID.String(),
			Title:    track.Name,
			Artist:   strings.Join(artists, ", "),
			Duration: time.Duration(track.Duration) * time.Millisecond,
			Source:   player.SourceSpotify,
			URL:      track.ExternalURLs["spotify"],
		})
	}

	return tracks, nil
}

// SearchTrack searches for a track on Spotify
func (c *Client) SearchTrack(query string) (*player.Track, error) {
	result, err := c.client.Search(c.ctx, query, spotify.SearchTypeTrack, spotify.Limit(1))
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	if result.Tracks == nil || len(result.Tracks.Tracks) == 0 {
		return nil, fmt.Errorf("no tracks found")
	}

	track := &result.Tracks.Tracks[0]
	artists := make([]string, len(track.Artists))
	for i, artist := range track.Artists {
		artists[i] = artist.Name
	}

	return &player.Track{
		ID:       track.ID.String(),
		Title:    track.Name,
		Artist:   strings.Join(artists, ", "),
		Duration: time.Duration(track.Duration) * time.Millisecond,
		Source:   player.SourceSpotify,
		URL:      track.ExternalURLs["spotify"],
	}, nil
}

// ParseSpotifyURL parses a Spotify URL and returns the type and ID
func ParseSpotifyURL(url string) (string, string, error) {
	// Format: https://open.spotify.com/{type}/{id}
	parts := strings.Split(url, "/")
	if len(parts) < 5 {
		return "", "", fmt.Errorf("invalid Spotify URL")
	}

	spotifyType := parts[3] // track, playlist, album, artist
	id := parts[4]

	// Remove query parameters if present
	if idx := strings.Index(id, "?"); idx != -1 {
		id = id[:idx]
	}

	return spotifyType, id, nil
}

// IsSpotifyURL checks if a URL is a Spotify URL
func IsSpotifyURL(url string) bool {
	return strings.Contains(url, "spotify.com") || strings.HasPrefix(url, "spotify:")
}
