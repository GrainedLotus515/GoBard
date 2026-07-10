package player

import (
	"math/rand"
	"sync"
	"time"
)

// TrackSource represents where the track came from
type TrackSource string

const (
	SourceYouTube TrackSource = "youtube"
	SourceSpotify TrackSource = "spotify"
	SourceDirect  TrackSource = "direct"
)

// Track represents a single music track
type Track struct {
	ID              string
	Title           string
	Artist          string
	URL             string
	Duration        time.Duration
	Source          TrackSource
	Thumbnail       string
	RequestedBy     string // Discord user ID
	IsLive          bool
	LocalPath       string // Path to cached file if available
	StreamURL       string // Pre-fetched direct stream URL for faster playback
	StreamHeaders   map[string]string
	StreamExpiresAt time.Time
	RequestTraceID  string    // Correlates debug logs for startup tracing
	RequestedAt     time.Time // When the play command queued this track

	MetadataFetchedAt       time.Time // When stream metadata was last resolved
	DirectStreamUnavailable bool      // Whether the last stream metadata resolution found no direct stream URL
	MetadataPending         bool      // Whether title/artist/duration/thumbnail are placeholder values awaiting hydration
}

const prefetchedStreamSafetyMargin = 2 * time.Minute

// SetPrefetchedStream stores a direct stream URL and the metadata needed to reuse it safely.
func (t *Track) SetPrefetchedStream(url string, headers map[string]string, expiresAt time.Time) {
	t.StreamURL = url
	t.StreamHeaders = cloneStringMap(headers)
	t.StreamExpiresAt = expiresAt
	t.MetadataFetchedAt = time.Now()
	t.DirectStreamUnavailable = url == ""
}

// ClearPrefetchedStream removes any previously resolved direct stream metadata.
func (t *Track) ClearPrefetchedStream() {
	t.StreamURL = ""
	t.StreamHeaders = nil
	t.StreamExpiresAt = time.Time{}
}

// CanUsePrefetchedStream reports whether the stored direct stream URL is still fresh enough for playback.
func (t *Track) CanUsePrefetchedStream(now time.Time, startOffset time.Duration) bool {
	if t == nil || t.IsLive || t.StreamURL == "" {
		return false
	}

	if t.StreamExpiresAt.IsZero() {
		return true
	}

	remainingPlayback := t.Duration - startOffset
	if remainingPlayback < 0 {
		remainingPlayback = 0
	}

	return !now.Add(remainingPlayback + prefetchedStreamSafetyMargin).After(t.StreamExpiresAt)
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}

	return dst
}

// Queue represents a music queue for a guild
type Queue struct {
	Tracks       []*Track
	CurrentIndex int
	Loop         bool
	Shuffle      bool
	// currentRemoved means the active track was deleted while it was still
	// playing. CurrentIndex then points to its predecessor, allowing the next
	// advance to select the immediate successor without replaying or skipping
	// anything (including when loop mode is enabled).
	currentRemoved bool
	mu             sync.RWMutex
}

// NewQueue creates a new empty queue
func NewQueue() *Queue {
	return &Queue{
		Tracks:       make([]*Track, 0),
		CurrentIndex: -1,
		Loop:         false,
		Shuffle:      false,
	}
}

// Add adds a track to the queue
func (q *Queue) Add(track *Track) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.Tracks = append(q.Tracks, track)
}

// Next moves to the next track in the queue
func (q *Queue) Next() *Track {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.Tracks) == 0 {
		q.CurrentIndex = -1
		q.currentRemoved = false
		return nil
	}

	if q.currentRemoved {
		q.currentRemoved = false
		nextIndex := q.CurrentIndex + 1
		if nextIndex >= len(q.Tracks) {
			q.CurrentIndex = -1
			return nil
		}
		q.CurrentIndex = nextIndex
		return q.Tracks[q.CurrentIndex]
	}

	if q.Loop && q.CurrentIndex >= 0 && q.CurrentIndex < len(q.Tracks) {
		// Stay on current track if looping
		return q.Tracks[q.CurrentIndex]
	}

	q.CurrentIndex++
	if q.CurrentIndex >= len(q.Tracks) {
		// Reset index so new tracks can be picked up
		q.CurrentIndex = -1
		return nil
	}

	return q.Tracks[q.CurrentIndex]
}

// TryAdvance atomically checks and advances to the next track, returning it.
// Returns nil if at end of queue or if looping is active (caller should check
// IsLoopEnabled first). It also handles a current-track removal as a
// one-time bypass of loop mode, selecting the immediate successor. When nil
// is returned, played tracks are cleared atomically so tracks added
// concurrently by another goroutine are not wiped.
func (q *Queue) TryAdvance() *Track {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.currentRemoved {
		q.currentRemoved = false
		nextIdx := q.CurrentIndex + 1
		if nextIdx >= len(q.Tracks) {
			q.CurrentIndex = -1
			q.Tracks = make([]*Track, 0)
			return nil
		}
		q.CurrentIndex = nextIdx
		return q.Tracks[q.CurrentIndex]
	}

	if q.Loop && q.CurrentIndex >= 0 && q.CurrentIndex < len(q.Tracks) {
		return q.Tracks[q.CurrentIndex]
	}

	nextIdx := q.CurrentIndex + 1
	if nextIdx >= len(q.Tracks) {
		// No more tracks.  Clear played tracks atomically so that tracks
		// added after this point (by a concurrent Add call) survive.
		q.CurrentIndex = -1
		q.Tracks = make([]*Track, 0)
		return nil
	}

	q.CurrentIndex = nextIdx
	return q.Tracks[q.CurrentIndex]
}

// TryAdvanceBypassingLoop advances to the immediate successor even when
// one-track loop is enabled. It is used for explicit skip/removal and for a
// permanently failed source; those user-visible transitions must not replay
// the current track.
func (q *Queue) TryAdvanceBypassingLoop() *Track {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.currentRemoved {
		q.currentRemoved = false
		nextIdx := q.CurrentIndex + 1
		if nextIdx >= len(q.Tracks) {
			q.CurrentIndex = -1
			q.Tracks = make([]*Track, 0)
			return nil
		}
		q.CurrentIndex = nextIdx
		return q.Tracks[q.CurrentIndex]
	}

	nextIdx := q.CurrentIndex + 1
	if nextIdx >= len(q.Tracks) {
		q.CurrentIndex = -1
		q.Tracks = make([]*Track, 0)
		return nil
	}
	q.CurrentIndex = nextIdx
	return q.Tracks[q.CurrentIndex]
}

// RewindCurrent is retained for compatibility with older callers. The active
// queue entry is never advanced on a transport failure, so replaying it after
// reconnect must leave the current index untouched. The old decrement logic
// replayed the previous track for every track after the first.
func (q *Queue) RewindCurrent() {
	// Intentionally a no-op.
}

// Current returns the current track
func (q *Queue) Current() *Track {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if q.CurrentIndex < 0 || q.CurrentIndex >= len(q.Tracks) {
		return nil
	}
	if q.currentRemoved {
		return nil
	}
	return q.Tracks[q.CurrentIndex]
}

// HasPendingCurrentRemoval reports whether the active track was removed and
// the next transition must select its immediate successor. It is primarily
// useful to playback controllers deciding how to handle a stopped session.
func (q *Queue) HasPendingCurrentRemoval() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.currentRemoved
}

// Clear removes all tracks from the queue except the current one
func (q *Queue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.currentRemoved && q.CurrentIndex >= 0 && q.CurrentIndex < len(q.Tracks) {
		current := q.Tracks[q.CurrentIndex]
		q.Tracks = []*Track{current}
		q.CurrentIndex = 0
	} else {
		q.Tracks = make([]*Track, 0)
		q.CurrentIndex = -1
	}
	q.currentRemoved = false
}

// ClearAll removes all tracks from the queue including the current one
func (q *Queue) ClearAll() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.Tracks = make([]*Track, 0)
	q.CurrentIndex = -1
	q.currentRemoved = false
}

// Remove removes a track at the specified index and reports whether the
// removed track was the currently-playing one.
func (q *Queue) Remove(index int) (success bool, wasCurrent bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if index < 0 || index >= len(q.Tracks) {
		return false, false
	}

	wasCurrent = !q.currentRemoved && q.CurrentIndex == index
	q.Tracks = append(q.Tracks[:index], q.Tracks[index+1:]...)

	if wasCurrent {
		// Keep the predecessor index so the next call to Next/TryAdvance picks
		// the track that slid into the removed slot. currentRemoved makes
		// Current return nil and bypasses loop mode exactly once.
		q.CurrentIndex = index - 1
		q.currentRemoved = true
	} else if q.CurrentIndex > index {
		q.CurrentIndex--
	}

	return true, wasCurrent
}

// ReplaceTrack swaps a queued/current track by pointer identity.
func (q *Queue) ReplaceTrack(target *Track, replacement *Track) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if target == nil || replacement == nil {
		return false
	}

	for idx, track := range q.Tracks {
		if track == target {
			q.Tracks[idx] = replacement
			return true
		}
	}

	return false
}

// RemoveTrack removes a queued/current track by pointer identity and reports
// whether the removed track was the currently-playing one.
func (q *Queue) RemoveTrack(target *Track) (success bool, wasCurrent bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if target == nil {
		return false, false
	}

	for idx, track := range q.Tracks {
		if track != target {
			continue
		}

		wasCurrent = !q.currentRemoved && q.CurrentIndex == idx
		q.Tracks = append(q.Tracks[:idx], q.Tracks[idx+1:]...)
		if wasCurrent {
			q.CurrentIndex = idx - 1
			q.currentRemoved = true
		} else if q.CurrentIndex > idx {
			q.CurrentIndex--
		}
		return true, wasCurrent
	}

	return false, false
}

// FindTrack locates a queued/current track by pointer identity.
func (q *Queue) FindTrack(target *Track) (index int, isCurrent bool, ok bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if target == nil {
		return -1, false, false
	}

	for idx, track := range q.Tracks {
		if track == target {
			return idx, !q.currentRemoved && idx == q.CurrentIndex, true
		}
	}

	return -1, false, false
}

// Move moves a track from one position to another
func (q *Queue) Move(from, to int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if from < 0 || from >= len(q.Tracks) || to < 0 || to >= len(q.Tracks) {
		return false
	}

	// The current slot and its boundary are controller-owned while playback is
	// active. Restrict moves to upcoming tracks so a move cannot silently
	// change the identity of the playing track or corrupt the cursor.
	if q.currentRemoved || from <= q.CurrentIndex || to <= q.CurrentIndex {
		return false
	}
	if from == to {
		return true
	}

	track := q.Tracks[from]
	if from < to {
		copy(q.Tracks[from:to], q.Tracks[from+1:to+1])
	} else {
		copy(q.Tracks[to+1:from+1], q.Tracks[to:from])
	}
	q.Tracks[to] = track

	return true
}

// Snapshot returns a copy of queue tracks and the current index.
func (q *Queue) Snapshot() ([]*Track, int) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	tracks := make([]*Track, len(q.Tracks))
	copy(tracks, q.Tracks)
	if q.currentRemoved {
		return tracks, -1
	}
	return tracks, q.CurrentIndex
}

// ShuffleUpcoming shuffles tracks after the current one.
// If no track is currently selected, it shuffles the whole queue.
func (q *Queue) ShuffleUpcoming() bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.Tracks) <= 1 {
		return false
	}

	if q.CurrentIndex >= 0 {
		if q.CurrentIndex >= len(q.Tracks)-1 {
			return false
		}

		start := q.CurrentIndex + 1
		rand.Shuffle(len(q.Tracks[start:]), func(i, j int) {
			q.Tracks[start+i], q.Tracks[start+j] = q.Tracks[start+j], q.Tracks[start+i]
		})
		return true
	}

	rand.Shuffle(len(q.Tracks), func(i, j int) {
		q.Tracks[i], q.Tracks[j] = q.Tracks[j], q.Tracks[i]
	})
	return true
}

// IsLoopEnabled returns whether loop mode is enabled.
func (q *Queue) IsLoopEnabled() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.Loop
}

// ToggleLoop toggles loop mode and returns the new value.
func (q *Queue) ToggleLoop() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.Loop = !q.Loop
	return q.Loop
}

// IsEmpty returns true if the queue is empty
func (q *Queue) IsEmpty() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.Tracks) == 0
}

// Length returns the number of tracks in the queue
func (q *Queue) Length() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.Tracks)
}

// Peek returns the next track without advancing the queue
func (q *Queue) Peek() *Track {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if len(q.Tracks) == 0 {
		return nil
	}

	nextIndex := q.CurrentIndex + 1
	if nextIndex >= len(q.Tracks) {
		return nil
	}

	return q.Tracks[nextIndex]
}
