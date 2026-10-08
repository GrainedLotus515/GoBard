package source

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/sourceurl"
)

func TestMetadataPlaylistAndURLBoundary(t *testing.T) {
	o := fakeExtractor(t, `printf '%s' '{"entries":[{"id":"first","title":"First","uploader":"Artist","duration":65},{"id":"second","title":"Second","is_live":true}]}'`)
	r, err := NewResolver(o.limiter, 2)
	if err != nil {
		t.Fatal(err)
	}
	r.executable = o.executable
	tracks, err := r.Resolve(context.Background(), "https://youtube.com/playlist?list=fixture")
	if err != nil || len(tracks) != 2 {
		t.Fatal(tracks, err)
	}
	if tracks[0].MediaID != "https://www.youtube.com/watch?v=first" || tracks[0].Duration != 65*time.Second || !tracks[1].Live {
		t.Fatal(tracks)
	}
	for _, query := range []string{"http://youtube.com/watch?v=x", "https://youtube.com.attacker.invalid/watch?v=x", "file:///etc/passwd", "https://www.youtube.com@localhost/watch?v=x"} {
		if _, err := r.Resolve(context.Background(), query); !errors.Is(err, sourceurl.ErrUnsafeSourceURL) {
			t.Fatalf("%s: %v", query, err)
		}
	}
	r.maxTracks = 1
	if _, err := r.Resolve(context.Background(), "music search"); err == nil {
		t.Fatal("over-budget playlist accepted")
	}
}

func TestMetadataRejectsUnsafeReturnedIDAndSafeDiagnostics(t *testing.T) {
	for _, script := range []string{
		`printf '%s' '{"id":"../private?token=secret","title":"Bad"}'`,
		`printf '%s' 'secret invalid JSON'`,
		`printf '%s' 'Sign in to confirm secret' >&2; exit 1`,
	} {
		o := fakeExtractor(t, script)
		r, err := NewResolver(o.limiter, 10)
		if err != nil {
			t.Fatal(err)
		}
		r.executable = o.executable
		_, err = r.Resolve(context.Background(), "search text")
		var classified *failure.Error
		if !errors.As(err, &classified) || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
}

func TestMetadataByteBudgetCannotBeBypassedByCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &limitedMetadata{cancel: cancel}
	reader := strings.NewReader(strings.Repeat("x", maxMetadataBytes+1))
	if _, err := io.Copy(w, reader); !errors.Is(err, errMetadataLimit) {
		t.Fatal(err)
	}
	if w.buffer.Len() > maxMetadataBytes || ctx.Err() == nil || !w.exceeded {
		t.Fatal("budget or cancellation not enforced")
	}
	if _, ok := any(w).(io.ReaderFrom); ok {
		t.Fatal("ReaderFrom could bypass the bounded Write implementation")
	}
}

func TestMetadataCancellationReleasesAdmission(t *testing.T) {
	o := fakeExtractor(t, "sleep 60 &\nwait\n")
	r, err := NewResolver(o.limiter, 1)
	if err != nil {
		t.Fatal(err)
	}
	r.executable = o.executable
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Resolve(ctx, "query"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	check, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	release, err := o.limiter.Acquire(check)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestBoundedMetadataText(t *testing.T) {
	text := boundedText(strings.Repeat("音", 1000), 512)
	if len(text) > 512 || !utf8.ValidString(text) {
		t.Fatal("invalid bounded metadata")
	}
}
