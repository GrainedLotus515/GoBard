package player

import (
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
	ID          string
	Title       string
	Artist      string
	URL         string
	Duration    time.Duration
	Source      TrackSource
	Thumbnail   string
	RequestedBy string // Discord user ID
	IsLive      bool
	LocalPath   string // Path to cached file if available
	StreamURL   string // Pre-fetched direct stream URL for faster playback
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
