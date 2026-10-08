package source

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/sourceurl"
)

// Download writes at most maxBytes to a new caller-owned temporary file. The
// caller owns removal/publication. Failure never publishes a partial cache hit.
func (o *YouTubeOpener) Download(parent context.Context, rawURL, path string, maxBytes int64) (result error) {
	url, err := sourceurl.ValidateCanonicalYouTubeVideoURL(rawURL)
	if err != nil {
		return failure.Wrap("cache_validate", failure.Source, err)
	}
	if maxBytes < 1 {
		return errors.New("positive download byte budget required")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	release, err := o.limiter.AcquireClass(ctx, processlimit.Background)
	if err != nil {
		return failure.Wrap("cache_admission", failure.Source, err)
	}
	defer release()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return failure.Wrap("cache_open", failure.Source, err)
	}
	defer func() {
		if err := f.Close(); result == nil && err != nil {
			result = failure.Wrap("cache_close", failure.Source, err)
		}
	}()
	writer := limitedFile{file: f, remaining: maxBytes, cancel: cancel}
	// #nosec G204 -- constructor-owned executable; canonical input follows --.
	cmd := exec.CommandContext(ctx, o.executable, extractionArgs(url)...)
	configureProcess(cmd)
	var stderr failure.Diagnostic
	cmd.Stdout = &writer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if writer.exceeded {
			return cache.ErrEntryTooLarge
		}
		return stderr.Failure("cache_acquire", failure.Source, errors.Join(ctx.Err(), err))
	}
	if writer.remaining == maxBytes {
		return failure.Wrap("cache_acquire", failure.ZeroFrames, errors.New("extractor returned no media bytes"))
	}
	if err := f.Sync(); err != nil {
		return failure.Wrap("cache_sync", failure.Source, err)
	}
	return nil
}

type limitedFile struct {
	file      *os.File
	remaining int64
	cancel    context.CancelFunc
	exceeded  bool
}

func (w *limitedFile) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		w.exceeded = true
		w.cancel()
		return 0, cache.ErrEntryTooLarge
	}
	n, err := w.file.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func extractionArgs(url string) []string {
	return []string{"--ignore-config", "--no-playlist", "--no-progress", "--no-warnings",
		"--retries", "0", "--fragment-retries", "0", "--extractor-retries", "0", "--file-access-retries", "0", "--socket-timeout", "15",
		"-f", "bestaudio[ext=webm]/bestaudio", "-o", "-", "--", url}
}
