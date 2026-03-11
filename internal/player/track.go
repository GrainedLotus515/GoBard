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
}

const prefetchedStreamSafetyMargin = 2 * time.Minute

// SetPrefetchedStream stores a direct stream URL and the metadata needed to reuse it safely.
func (t *Track) SetPrefetchedStream(url string, headers map[string]string, expiresAt time.Time) {
	t.StreamURL = url
	t.StreamHeaders = cloneStringMap(headers)
	t.StreamExpiresAt = expiresAt
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
	mu           sync.RWMutex
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
		return nil
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

// Current returns the current track
func (q *Queue) Current() *Track {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if q.CurrentIndex < 0 || q.CurrentIndex >= len(q.Tracks) {
		return nil
	}
	return q.Tracks[q.CurrentIndex]
}

// Clear removes all tracks from the queue except the current one
func (q *Queue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.CurrentIndex >= 0 && q.CurrentIndex < len(q.Tracks) {
		current := q.Tracks[q.CurrentIndex]
		q.Tracks = []*Track{current}
		q.CurrentIndex = 0
	} else {
		q.Tracks = make([]*Track, 0)
		q.CurrentIndex = -1
	}
}

// ClearAll removes all tracks from the queue including the current one
func (q *Queue) ClearAll() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.Tracks = make([]*Track, 0)
	q.CurrentIndex = -1
}

// Remove removes a track at the specified index
func (q *Queue) Remove(index int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if index < 0 || index >= len(q.Tracks) {
		return false
	}

	q.Tracks = append(q.Tracks[:index], q.Tracks[index+1:]...)

	// Adjust current index if necessary
	if q.CurrentIndex >= index {
		q.CurrentIndex--
	}

	return true
}

// Move moves a track from one position to another
func (q *Queue) Move(from, to int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if from < 0 || from >= len(q.Tracks) || to < 0 || to >= len(q.Tracks) {
		return false
	}

	track := q.Tracks[from]
	q.Tracks = append(q.Tracks[:from], q.Tracks[from+1:]...)

	// Insert at new position
	q.Tracks = append(q.Tracks[:to], append([]*Track{track}, q.Tracks[to:]...)...)

	// Adjust current index
	if q.CurrentIndex == from {
		q.CurrentIndex = to
	} else if from < q.CurrentIndex && to >= q.CurrentIndex {
		q.CurrentIndex--
	} else if from > q.CurrentIndex && to <= q.CurrentIndex {
		q.CurrentIndex++
	}

	return true
}

// Snapshot returns a copy of queue tracks and the current index.
func (q *Queue) Snapshot() ([]*Track, int) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	tracks := make([]*Track, len(q.Tracks))
	copy(tracks, q.Tracks)
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
