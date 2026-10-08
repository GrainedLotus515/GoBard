package session

import (
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/GrainedLotus515/gobard/internal/playback"
)

// Track contains bounded display metadata and a provider-specific acquisition
// key. It contains no SDK objects, signed URLs, headers or mutable futures.
type Track struct {
	Provider    string
	MediaID     string
	Title       string
	Artist      string
	URL         string
	Thumbnail   string
	RequestedBy string
	Duration    time.Duration
	Live        bool
}

// clone detaches bounded fields from potentially much larger caller strings.
// A short substring alone is not a bound on retained backing storage.
func (t Track) clone() Track {
	t.Provider = strings.Clone(t.Provider)
	t.MediaID = strings.Clone(t.MediaID)
	t.Title = strings.Clone(t.Title)
	t.Artist = strings.Clone(t.Artist)
	t.URL = strings.Clone(t.URL)
	t.Thumbnail = strings.Clone(t.Thumbnail)
	t.RequestedBy = strings.Clone(t.RequestedBy)
	return t
}

func (t Track) validate() error {
	if len(t.Provider) > 32 || len(t.MediaID) > 256 || len(t.Title) > 512 || len(t.Artist) > 256 || len(t.URL) > 256 || len(t.Thumbnail) > 1024 || len(t.RequestedBy) > 32 || t.Duration < 0 {
		return errors.New("track metadata exceeds bounds")
	}
	return nil
}

type Action string

const (
	Inspect    Action = "inspect"
	Pause      Action = "pause"
	Resume     Action = "resume"
	Skip       Action = "skip"
	Stop       Action = "stop"
	Disconnect Action = "disconnect"
	Connect    Action = "connect"
	Clear      Action = "clear"
	Shuffle    Action = "shuffle"
	Loop       Action = "loop"
	Remove     Action = "remove"
	Move       Action = "move"
	Seek       Action = "seek"
	Volume     Action = "volume"
	Ducking    Action = "ducking"
	DuckTarget Action = "duck_target"
	Speaking   Action = "speaking"
)

type Operation struct {
	Action   Action
	Position int
	To       int
	Offset   time.Duration
	Enabled  bool
	Speaker  string
	Output   playback.Output
}

// Snapshot is a value copy. Current is -1 when idle. Queue positions remain
// stable across completion until the queue drains, matching existing commands.
type Snapshot struct {
	Tracks     []Track
	Current    int
	Active     bool
	Connected  bool
	Paused     bool
	Loop       bool
	Volume     int
	Ducking    bool
	DuckTarget int
	Position   time.Duration
	Frames     uint64
	Last       playback.Result
}

type entry struct {
	track    Track
	ticket   *Ticket
	attempts uint64
	settled  bool
}

// queue is used exclusively by its coordinator; no locks or callbacks.
type queue struct {
	entries  []entry
	index    int
	capacity int
}

func (q *queue) current() *entry {
	if q.index < 0 || q.index >= len(q.entries) {
		return nil
	}
	return &q.entries[q.index]
}

func (q *queue) reset() { clear(q.entries); q.entries = q.entries[:0]; q.index = -1 }

func (q *queue) advance() {
	q.index++
	if q.index >= len(q.entries) {
		q.reset()
	}
}

// remove requires an owner-validated existing position.
func (q *queue) remove(index int) {
	copy(q.entries[index:], q.entries[index+1:])
	q.entries[len(q.entries)-1] = entry{}
	q.entries = q.entries[:len(q.entries)-1]
	if index < q.index {
		q.index--
	}
	if len(q.entries) == 0 {
		q.index = -1
	}
}

func (q *queue) move(from, to int) error {
	if from <= q.index || to <= q.index || from < 0 || to < 0 || from >= len(q.entries) || to >= len(q.entries) {
		return errors.New("only upcoming queue positions can be moved")
	}
	e := q.entries[from]
	if from < to {
		copy(q.entries[from:to], q.entries[from+1:to+1])
	} else {
		copy(q.entries[to+1:from+1], q.entries[to:from])
	}
	q.entries[to] = e
	return nil
}

func (q *queue) shuffle() error {
	start := max(q.index+1, 0)
	if len(q.entries)-start < 2 {
		return errors.New("not enough upcoming tracks to shuffle")
	}
	// #nosec G404 -- queue ordering is not a security primitive.
	rand.Shuffle(len(q.entries)-start, func(i, j int) { q.entries[start+i], q.entries[start+j] = q.entries[start+j], q.entries[start+i] })
	return nil
}
