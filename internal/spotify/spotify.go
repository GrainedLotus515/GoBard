package spotify

import "strings"

// IsSpotifyURL checks if a URL is a Spotify URL
func IsSpotifyURL(url string) bool {
	return strings.Contains(url, "spotify.com") || strings.HasPrefix(url, "spotify:")
}
