package sourceurl

import (
	"fmt"
	"net/url"
	"strings"
)

type URLKind uint8

const (
	URLKindUnknown URLKind = iota
	URLKindVideo
	URLKindPlaylist
)

// ClassifyYouTubeURL validates an externally supplied URL and returns a
// canonical video or playlist URL. It intentionally accepts a small, explicit
// grammar rather than passing arbitrary URLs through to yt-dlp.
func ClassifyYouTubeURL(raw string) (canonicalURL string, kind URLKind, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", URLKindUnknown, fmt.Errorf("%w: URL is empty", ErrUnsafeSourceURL)
	}

	parsed, parseErr := url.ParseRequestURI(trimmed)
	if parseErr != nil || parsed == nil {
		return "", URLKindUnknown, fmt.Errorf("%w: invalid URL", ErrUnsafeSourceURL)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", URLKindUnknown, fmt.Errorf("%w: only HTTPS URLs are accepted", ErrUnsafeSourceURL)
	}
	if parsed.User != nil {
		return "", URLKindUnknown, fmt.Errorf("%w: userinfo is not allowed", ErrUnsafeSourceURL)
	}
	if parsed.Host == "" || (parsed.Port() != "" && parsed.Port() != "443") {
		return "", URLKindUnknown, fmt.Errorf("%w: invalid host", ErrUnsafeSourceURL)
	}

	host := strings.ToLower(parsed.Hostname())
	if !isYouTubeHost(host) {
		return "", URLKindUnknown, fmt.Errorf("%w: host is not YouTube", ErrUnsafeSourceURL)
	}

	query := parsed.Query()
	playlistID := strings.TrimSpace(query.Get("list"))
	if playlistID != "" {
		if !isSafeIdentifier(playlistID, 128) {
			return "", URLKindUnknown, fmt.Errorf("%w: invalid playlist identifier", ErrUnsafeSourceURL)
		}
		if host == "youtu.be" {
			shortID := strings.Trim(parsed.EscapedPath(), "/")
			if strings.Contains(shortID, "/") {
				return "", URLKindUnknown, fmt.Errorf("%w: invalid short playlist path", ErrUnsafeSourceURL)
			}
			unescaped, unescapeErr := url.PathUnescape(shortID)
			if unescapeErr != nil || !isSafeIdentifier(unescaped, 32) {
				return "", URLKindUnknown, fmt.Errorf("%w: invalid short playlist path", ErrUnsafeSourceURL)
			}
		} else if parsed.Path != "/playlist" && parsed.Path != "/watch" {
			return "", URLKindUnknown, fmt.Errorf("%w: expected /watch or /playlist", ErrUnsafeSourceURL)
		}
		return canonicalPlaylistURL(playlistID), URLKindPlaylist, nil
	}

	var videoID string
	switch host {
	case "youtube.com", "www.youtube.com", "music.youtube.com":
		if parsed.Path != "/watch" {
			return "", URLKindUnknown, fmt.Errorf("%w: expected /watch or a playlist", ErrUnsafeSourceURL)
		}
		videoID = strings.TrimSpace(query.Get("v"))
	case "youtu.be":
		videoID = strings.Trim(parsed.EscapedPath(), "/")
		if strings.Contains(videoID, "/") {
			return "", URLKindUnknown, fmt.Errorf("%w: invalid short video path", ErrUnsafeSourceURL)
		}
		unescaped, unescapeErr := url.PathUnescape(videoID)
		if unescapeErr != nil {
			return "", URLKindUnknown, fmt.Errorf("%w: invalid short video path", ErrUnsafeSourceURL)
		}
		videoID = unescaped
	}

	if !isSafeIdentifier(videoID, 32) {
		return "", URLKindUnknown, fmt.Errorf("%w: invalid video identifier", ErrUnsafeSourceURL)
	}
	return canonicalVideoURL(videoID), URLKindVideo, nil
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
