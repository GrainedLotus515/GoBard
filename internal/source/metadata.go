package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/media"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/sourceurl"
)

const maxMetadataBytes = 8 << 20

var errMetadataLimit = errors.New("metadata response exceeds byte budget")

// Resolver provides bounded YouTube metadata, independently of acquisition.
// Calls are synchronous; the application owns bounded command workers.
type Resolver struct {
	limiter    *processlimit.Limiter
	maxTracks  int
	executable string
}

func NewResolver(limiter *processlimit.Limiter, maxTracks int) (*Resolver, error) {
	if limiter == nil || limiter.Capacity() < 1 || maxTracks < 1 || maxTracks > 500 {
		return nil, errors.New("invalid metadata budgets")
	}
	return &Resolver{limiter: limiter, maxTracks: maxTracks, executable: "yt-dlp"}, nil
}

func (r *Resolver) Resolve(parent context.Context, query string) ([]media.Track, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 512 || !utf8.ValidString(query) {
		return nil, errors.New("query must contain 1–512 bytes of UTF-8 text")
	}
	argument := "ytsearch1:" + query
	if sourceurl.IsURLLike(query) {
		canonical, _, err := sourceurl.ClassifyYouTubeURL(query)
		if err != nil {
			return nil, err
		}
		argument = canonical
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	release, err := r.limiter.Acquire(ctx)
	if err != nil {
		return nil, failure.Wrap("metadata_admission", failure.Source, err)
	}
	defer release()
	// Every explicit URL is canonicalized above; the other grammar is a bounded
	// ytsearch1 expression. No query is interpolated into a shell command.
	// #nosec G204 -- executable is constructor-owned; validated input follows --.
	cmd := exec.CommandContext(ctx, r.executable, "--ignore-config", "--skip-download", "--dump-single-json",
		"--flat-playlist", "--no-warnings", "--retries", "0", "--extractor-retries", "0",
		"--socket-timeout", "15", "--playlist-end", strconv.Itoa(r.maxTracks), "--", argument)
	configureProcess(cmd)
	var stderr failure.Diagnostic
	stdout := limitedMetadata{cancel: cancel}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stdout.exceeded {
			return nil, failure.Wrap("metadata", failure.Source, errMetadataLimit)
		}
		return nil, stderr.Failure("metadata", failure.Source, errors.Join(ctx.Err(), err))
	}
	var metadata metadataEntry
	if err := json.Unmarshal(stdout.buffer.Bytes(), &metadata); err != nil {
		return nil, failure.Wrap("metadata_decode", failure.Source, err)
	}
	entries, err := metadata.entries(r.maxTracks)
	if err != nil {
		return nil, failure.Wrap("metadata", failure.Source, err)
	}
	tracks := make([]media.Track, 0, len(entries))
	for _, item := range entries {
		if item == nil {
			continue
		}
		track, err := item.track()
		if err != nil {
			return nil, failure.Wrap("metadata_validate", failure.Source, err)
		}
		tracks = append(tracks, track)
	}
	if len(tracks) == 0 {
		return nil, failure.Wrap("metadata", failure.Unavailable, errors.New("no playable tracks found"))
	}
	return tracks, nil
}

type limitedMetadata struct {
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	exceeded bool
}

func (b *limitedMetadata) Write(p []byte) (int, error) {
	if len(p) > maxMetadataBytes-b.buffer.Len() {
		b.exceeded = true
		b.cancel()
		return 0, errMetadataLimit
	}
	return b.buffer.Write(p)
}

type metadataEntry struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Artist    string          `json:"uploader"`
	Duration  float64         `json:"duration"`
	Live      bool            `json:"is_live"`
	Thumbnail string          `json:"thumbnail"`
	Entries   json.RawMessage `json:"entries"`
}

// Decode rows incrementally so an 8 MiB array of tiny objects cannot allocate
// millions of Go structs before the track-count check. Nested playlists are
// not an acquisition capability and are rejected without recursive decoding.
func (m *metadataEntry) entries(limit int) ([]*metadataEntry, error) {
	if len(m.Entries) == 0 || bytes.Equal(m.Entries, []byte("null")) {
		return []*metadataEntry{m}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(m.Entries))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('[') {
		return nil, errors.New("invalid playlist entries")
	}
	entries := make([]*metadataEntry, 0, min(limit, 16))
	for decoder.More() {
		if len(entries) >= limit {
			return nil, errors.New("playlist exceeds track budget")
		}
		var entry *metadataEntry
		if err := decoder.Decode(&entry); err != nil {
			return nil, err
		}
		if entry != nil && len(entry.Entries) != 0 && !bytes.Equal(entry.Entries, []byte("null")) {
			return nil, errors.New("nested playlists unavailable")
		}
		entries = append(entries, entry)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (m *metadataEntry) track() (media.Track, error) {
	canonical := "https://www.youtube.com/watch?v=" + url.QueryEscape(m.ID)
	if _, err := sourceurl.ValidateCanonicalYouTubeVideoURL(canonical); err != nil {
		return media.Track{}, err
	}
	t := media.Track{Provider: "youtube", MediaID: canonical, TrackID: m.ID, URL: canonical, Title: boundedText(m.Title, 512), Artist: boundedText(m.Artist, 256), Live: m.Live}
	if t.Title == "" {
		t.Title = m.ID
	}
	if m.Duration > 0 && m.Duration <= 7*24*3600 {
		t.Duration = time.Duration(m.Duration * float64(time.Second))
	}
	if len(m.Thumbnail) <= 1024 && strings.HasPrefix(m.Thumbnail, "https://") && sourceurl.IsPublicHTTPURL(m.Thumbnail) {
		t.Thumbnail = m.Thumbnail
	}
	return t, t.Validate()
}

func boundedText(s string, limit int) string {
	if len(s) > limit {
		s = s[:limit]
	}
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.Clone(s)
}
