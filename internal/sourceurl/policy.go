// Package sourceurl contains source-address policy shared by metadata
// resolution and playback process launchers. Keeping this policy independent
// of the youtube and player packages prevents an import cycle while ensuring
// both boundaries enforce the same trust assumptions.
package sourceurl

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

var (
	// ErrUnsafeSourceURL means a URL is not safe to hand to an external media
	// process. Callers should return a generic user-facing error rather than
	// echoing the input, which can include signed query parameters.
	ErrUnsafeSourceURL = errors.New("unsafe media source URL")

	privateCarrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")
	privateIETFProtocol    = netip.MustParsePrefix("192.0.0.0/24")
	privateBenchmark       = netip.MustParsePrefix("198.18.0.0/15")
)

// ValidateCanonicalYouTubeVideoURL accepts only GoBard's canonical video URL
// form. The YouTube resolver converts allowed user input into this form before
// a Track is created; playback validates it again immediately before invoking
// yt-dlp so manually-created or stale tracks cannot make it extract arbitrary
// URLs.
func ValidateCanonicalYouTubeVideoURL(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed == nil || !strings.EqualFold(parsed.Scheme, "https") ||
		parsed.User != nil || parsed.Host != "www.youtube.com" || parsed.Port() != "" ||
		parsed.Path != "/watch" || parsed.Fragment != "" || parsed.ForceQuery {
		return "", ErrUnsafeSourceURL
	}

	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query) != 1 || len(query["v"]) != 1 {
		return "", ErrUnsafeSourceURL
	}
	videoID := query.Get("v")
	if !isSafeIdentifier(videoID, 32) {
		return "", ErrUnsafeSourceURL
	}

	canonical := "https://www.youtube.com/watch?v=" + url.QueryEscape(videoID)
	if raw != canonical {
		return "", fmt.Errorf("%w: expected canonical YouTube video URL", ErrUnsafeSourceURL)
	}
	return canonical, nil
}

// IsPublicHTTPURL rejects malformed, non-HTTP(S), local, and private literal
// stream addresses. Extractor-provided direct stream URLs are still checked at
// playback time because passing one directly to FFmpeg creates an SSRF hop.
func IsPublicHTTPURL(raw string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}

	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || allDigits(host) {
		return false
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return isPublicIP(address)
	}
	if isPotentialLegacyIPv4Literal(host) {
		return false
	}
	// A colon belongs only to an IPv6 literal here. If netip could not parse
	// it (for example an address with a zone), never let FFmpeg reinterpret it.
	if strings.Contains(host, ":") {
		return false
	}
	return true
}

func isSafeIdentifier(value string, maxLength int) bool {
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

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isPotentialLegacyIPv4Literal catches non-canonical numeric forms that
// netip intentionally rejects but some URL consumers still interpret as a
// loopback/private IPv4 address (for example 127.1 or 0x7f000001).
func isPotentialLegacyIPv4Literal(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) == 1 {
		return isLegacyIPv4Part(parts[0]) && strings.HasPrefix(strings.ToLower(parts[0]), "0x")
	}
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, part := range parts {
		if !isLegacyIPv4Part(part) {
			return false
		}
	}
	return true
}

func isLegacyIPv4Part(value string) bool {
	if value == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		if len(value) == 2 {
			return false
		}
		for _, r := range value[2:] {
			if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
				continue
			}
			return false
		}
		return true
	}
	return allDigits(value)
}

func isPublicIP(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsLoopback() || address.IsPrivate() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() ||
		address.IsUnspecified() || privateCarrierGradeNAT.Contains(address) || privateIETFProtocol.Contains(address) ||
		privateBenchmark.Contains(address) {
		return false
	}
	return true
}
