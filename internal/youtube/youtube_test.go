package youtube

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type commandResponse struct {
	stdout   string
	stderr   string
	exitCode int
}

func TestYouTubeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_YOUTUBE_HELPER_PROCESS") != "1" {
		return
	}

	if _, err := fmt.Fprint(os.Stdout, os.Getenv("GO_YOUTUBE_HELPER_STDOUT")); err != nil {
		os.Exit(1)
	}
	if _, err := fmt.Fprint(os.Stderr, os.Getenv("GO_YOUTUBE_HELPER_STDERR")); err != nil {
		os.Exit(1)
	}

	exitCode, err := strconv.Atoi(os.Getenv("GO_YOUTUBE_HELPER_EXIT_CODE"))
	if err != nil {
		exitCode = 0
	}

	os.Exit(exitCode)
}

func TestNormalizeSearchKey(t *testing.T) {
	got := normalizeSearchKey("  Foo\tBAR   baz  ")
	if got != "foo bar baz" {
		t.Fatalf("normalizeSearchKey() = %q, want %q", got, "foo bar baz")
	}
}

func TestNormalizeSingleVideoURL(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantURL     string
		wantVideoID string
		wantOK      bool
	}{
		{
			name:        "music watch strips extra params",
			raw:         "https://music.youtube.com/watch?v=abc123XYZ89&si=token&t=42",
			wantURL:     "https://www.youtube.com/watch?v=abc123XYZ89",
			wantVideoID: "abc123XYZ89",
			wantOK:      true,
		},
		{
			name:   "watch with playlist param is rejected",
			raw:         "https://www.youtube.com/watch?v=abc123XYZ89&list=PL123&index=4",
			wantOK:      false,
		},
		{
			name:        "short host strips params",
			raw:         "https://youtu.be/abc123XYZ89?si=token",
			wantURL:     "https://www.youtube.com/watch?v=abc123XYZ89",
			wantVideoID: "abc123XYZ89",
			wantOK:      true,
		},
		{
			name:   "short host with playlist param is rejected",
			raw:    "https://youtu.be/abc123XYZ89?list=PL123",
			wantOK: false,
		},
		{
			name:   "playlist is rejected",
			raw:    "https://www.youtube.com/playlist?list=PL123",
			wantOK: false,
		},
		{
			name:   "unsupported shorts url is rejected",
			raw:    "https://www.youtube.com/shorts/abc123XYZ89",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotVideoID, gotOK := NormalizeSingleVideoURL(tt.raw)
			if gotOK != tt.wantOK {
				t.Fatalf("NormalizeSingleVideoURL() ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotURL != tt.wantURL {
				t.Fatalf("NormalizeSingleVideoURL() canonicalURL = %q, want %q", gotURL, tt.wantURL)
			}
			if gotVideoID != tt.wantVideoID {
				t.Fatalf("NormalizeSingleVideoURL() videoID = %q, want %q", gotVideoID, tt.wantVideoID)
			}
		})
	}
}

func TestSearchUsesPositiveCacheAndReturnsStableClones(t *testing.T) {
	client := NewClient("")
	now := time.Unix(1_700_000_000, 0).UTC()
	client.now = func() time.Time { return now }

	calls := stubSearchCommands(t, []commandResponse{{
		stdout: `{"id":"song-1","title":"Cache Song","duration":123,"thumbnail":"https://example.com/thumb.jpg","uploader":"Cache Artist","webpage_url":"https://www.youtube.com/watch?v=song-1","is_live":false,"formats":[{"format_id":"251","url":"https://media.example/audio.webm?expire=4102444800","ext":"webm","acodec":"opus","vcodec":"none","abr":128,"http_headers":{"User-Agent":"yt-dlp"}}]}`,
	}})

	first, err := client.Search("  Cache   Song  ")
	if err != nil {
		t.Fatalf("first Search() error = %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first Search() tracks = %d, want 1", len(first))
	}
	if first[0].StreamURL == "" {
		t.Fatal("first Search() should return prefetched stream metadata on cache miss")
	}

	first[0].Title = "mutated"
	first[0].RequestedBy = "user-1"
	first[0].StreamURL = "https://mutated.example/audio.webm"

	second, err := client.Search("cache song")
	if err != nil {
		t.Fatalf("second Search() error = %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("second Search() tracks = %d, want 1", len(second))
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("command calls = %d, want 1", got)
	}
	if second[0].Title != "Cache Song" {
		t.Fatalf("cached Search() title = %q, want %q", second[0].Title, "Cache Song")
	}
	if second[0].RequestedBy != "" {
		t.Fatalf("cached Search() RequestedBy = %q, want empty", second[0].RequestedBy)
	}
	if second[0].StreamURL != "" {
		t.Fatalf("cached Search() StreamURL = %q, want empty", second[0].StreamURL)
	}
	if !second[0].MetadataFetchedAt.IsZero() {
		t.Fatalf("cached Search() MetadataFetchedAt = %v, want zero", second[0].MetadataFetchedAt)
	}
}

func TestSearchEvictsStalePositiveCacheEntries(t *testing.T) {
	client := NewClient("")
	now := time.Unix(1_700_000_000, 0).UTC()
	client.now = func() time.Time { return now }

	calls := stubSearchCommands(t, []commandResponse{
		{
			stdout: `{"id":"song-stale-1","title":"First Title","duration":100,"thumbnail":"https://example.com/1.jpg","uploader":"Artist 1","webpage_url":"https://www.youtube.com/watch?v=song-stale-1","is_live":false,"formats":[]}`,
		},
		{
			stdout: `{"id":"song-stale-2","title":"Second Title","duration":101,"thumbnail":"https://example.com/2.jpg","uploader":"Artist 2","webpage_url":"https://www.youtube.com/watch?v=song-stale-2","is_live":false,"formats":[]}`,
		},
	})

	first, err := client.Search("stale me")
	if err != nil {
		t.Fatalf("first Search() error = %v", err)
	}
	if len(first) != 1 || first[0].Title != "First Title" {
		t.Fatalf("first Search() = %#v, want title %q", first, "First Title")
	}

	now = now.Add(searchCacheTTL + time.Minute)

	second, err := client.Search(" stale   me ")
	if err != nil {
		t.Fatalf("second Search() error = %v", err)
	}
	if len(second) != 1 || second[0].Title != "Second Title" {
		t.Fatalf("second Search() = %#v, want title %q", second, "Second Title")
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Fatalf("command calls = %d, want 2", got)
	}
}

func TestSearchUsesNegativeCacheForEmptyResults(t *testing.T) {
	client := NewClient("")
	now := time.Unix(1_700_000_000, 0).UTC()
	client.now = func() time.Time { return now }

	calls := stubSearchCommands(t, []commandResponse{{
		stderr:   "ERROR: No matches found",
		exitCode: 1,
	}})

	first, err := client.Search("missing result")
	if err != nil {
		t.Fatalf("first Search() error = %v, want nil", err)
	}
	if len(first) != 0 {
		t.Fatalf("first Search() tracks = %d, want 0", len(first))
	}

	second, err := client.Search("  Missing   Result ")
	if err != nil {
		t.Fatalf("second Search() error = %v, want nil", err)
	}
	if len(second) != 0 {
		t.Fatalf("second Search() tracks = %d, want 0", len(second))
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("command calls = %d, want 1", got)
	}
}

func TestSearchUsesNegativeCacheForQueryErrors(t *testing.T) {
	client := NewClient("")
	now := time.Unix(1_700_000_000, 0).UTC()
	client.now = func() time.Time { return now }

	calls := stubSearchCommands(t, []commandResponse{{
		stderr:   "ERROR: Unsupported URL: not a valid URL",
		exitCode: 1,
	}})

	if _, err := client.Search("://bad url"); err == nil {
		t.Fatal("first Search() error = nil, want non-nil")
	}
	if _, err := client.Search(" ://BAD   URL "); err == nil {
		t.Fatal("second Search() error = nil, want non-nil")
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("command calls = %d, want 1", got)
	}
}

func stubSearchCommands(t *testing.T, responses []commandResponse) *int32 {
	t.Helper()

	if len(responses) == 0 {
		t.Fatal("stubSearchCommands() requires at least one response")
	}

	original := execCommandContext
	var calls int32

	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		index := int(atomic.AddInt32(&calls, 1)) - 1
		if index >= len(responses) {
			index = len(responses) - 1
		}
		response := responses[index]

		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestYouTubeHelperProcess", "--")
		cmd.Env = append(
			os.Environ(),
			"GO_WANT_YOUTUBE_HELPER_PROCESS=1",
			"GO_YOUTUBE_HELPER_STDOUT="+response.stdout,
			"GO_YOUTUBE_HELPER_STDERR="+response.stderr,
			"GO_YOUTUBE_HELPER_EXIT_CODE="+strconv.Itoa(response.exitCode),
		)
		return cmd
	}

	t.Cleanup(func() {
		execCommandContext = original
	})

	return &calls
}
