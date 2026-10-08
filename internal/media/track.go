package media

import (
	"errors"
	"strings"
	"time"
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
func (t Track) Clone() Track {
	t.Provider = strings.Clone(t.Provider)
	t.MediaID = strings.Clone(t.MediaID)
	t.Title = strings.Clone(t.Title)
	t.Artist = strings.Clone(t.Artist)
	t.URL = strings.Clone(t.URL)
	t.Thumbnail = strings.Clone(t.Thumbnail)
	t.RequestedBy = strings.Clone(t.RequestedBy)
	return t
}

func (t Track) Validate() error {
	if len(t.Provider) > 32 || len(t.MediaID) > 256 || len(t.Title) > 512 || len(t.Artist) > 256 || len(t.URL) > 256 || len(t.Thumbnail) > 1024 || len(t.RequestedBy) > 32 || t.Duration < 0 {
		return errors.New("track metadata exceeds bounds")
	}
	return nil
}
