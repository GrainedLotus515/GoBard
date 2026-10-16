package media

import (
	"strings"
	"testing"
)

func TestTrackDiagnosticIdentifierBoundary(t *testing.T) {
	for _, id := range []string{"", "8fM1xnLTApg", "public-identifier_123"} {
		track := Track{TrackID: id}
		if err := track.Validate(); err != nil || track.Clone().TrackID != id {
			t.Fatal("public identifier rejected or lost", err)
		}
	}
	for _, id := range []string{"https://signed.invalid/?token=secret", "/private/media", "@everyone", "line\nbreak", "音", strings.Repeat("x", 33)} {
		if err := (Track{TrackID: id}).Validate(); err == nil {
			t.Fatal("unsafe diagnostic identifier admitted")
		}
	}
}
