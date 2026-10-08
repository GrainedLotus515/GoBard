// Package source implements authorized media acquisition for playback.
package source

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/GrainedLotus515/gobard/internal/audio"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/sourceurl"
)

// YouTubeOpener is the byte-acquisition adapter. It never resolves metadata,
// retries, switches providers, accepts cookies, or passes signed URLs to FFmpeg.
// Its process limit is supplied by composition and shared with metadata work.
type YouTubeOpener struct {
	limiter    *processlimit.Limiter
	executable string
}

func NewYouTubeOpener(limiter *processlimit.Limiter) (*YouTubeOpener, error) {
	if limiter == nil || limiter.Capacity() < 1 {
		return nil, errors.New("extractor process budget required")
	}
	return &YouTubeOpener{limiter: limiter, executable: "yt-dlp"}, nil
}

func (o *YouTubeOpener) Open(parent context.Context, request playback.Request) (playback.Stream, error) {
	url, err := sourceurl.ValidateCanonicalYouTubeVideoURL(request.MediaID)
	if err != nil {
		return nil, failure.Wrap("validate", failure.Source, err)
	}
	release, err := o.limiter.Acquire(parent)
	if err != nil {
		return nil, failure.Wrap("admission", failure.Source, err)
	}
	ctx, cancel := context.WithCancel(parent)
	r, w, err := os.Pipe()
	if err != nil {
		cancel()
		release()
		return nil, failure.Wrap("acquire", failure.Source, err)
	}
	// #nosec G204 -- executable is fixed by construction; URL is canonicalized above and follows --.
	cmd := exec.CommandContext(ctx, o.executable,
		"--ignore-config", "--no-playlist", "--no-progress", "--no-warnings",
		"--retries", "0", "--fragment-retries", "0", "--socket-timeout", "15",
		"--extractor-retries", "0", "--file-access-retries", "0",
		"-f", "bestaudio[ext=webm]/bestaudio", "-o", "-", "--", url)
	// The deployment target is Linux. Kill the entire extractor process group,
	// including any fragment helper, so inherited pipes cannot prevent cleanup.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	s := &extractorStream{cmd: cmd, cancel: cancel, release: release}
	cmd.Stdout = w
	cmd.Stderr = &s.stderr
	if err := cmd.Start(); err != nil {
		cancel()
		release()
		_ = r.Close()
		_ = w.Close()
		return nil, failure.Wrap("acquire", failure.Source, err)
	}
	_ = w.Close()
	decoder, err := audio.Decode(ctx, r, request.Offset, request.Controls)
	_ = r.Close()
	if err != nil {
		cancel()
		err = errors.Join(err, s.wait())
		release()
		return nil, err
	}
	s.decoder = decoder
	return s, nil
}

type extractorStream struct {
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	release   func()
	stderr    failure.Diagnostic
	decoder   playback.Stream
	waitOnce  sync.Once
	waitErr   error
	closeOnce sync.Once
	closeErr  error
}

func (s *extractorStream) Next(ctx context.Context, packet []byte) (int, error) {
	n, err := s.decoder.Next(ctx, packet)
	if err == nil {
		return n, nil
	}
	if !errors.Is(err, io.EOF) {
		s.cancel()
	}
	if sourceErr := s.wait(); sourceErr != nil {
		var classified *failure.Error
		// A killed extractor after codec failure is cleanup, not the original
		// cause. A real extractor exit (including auth after partial audio) wins.
		if errors.As(sourceErr, &classified) && classified.ExitCode >= 0 {
			return 0, sourceErr
		}
		if errors.Is(err, io.EOF) {
			return 0, sourceErr
		}
	}
	return n, err
}

func (s *extractorStream) wait() error {
	s.waitOnce.Do(func() {
		// EOF on stdout is not a promise that the process will exit. Bound
		// finalization and join the timer callback if it fired concurrently.
		cancelDone := make(chan struct{})
		timer := time.AfterFunc(2*time.Second, func() { s.cancel(); close(cancelDone) })
		if err := s.cmd.Wait(); err != nil {
			s.waitErr = s.stderr.Failure("acquire", failure.Source, err)
		}
		if !timer.Stop() {
			<-cancelDone
		}
	})
	return s.waitErr
}

func (s *extractorStream) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		decoderErr := s.decoder.Close()
		sourceErr := s.wait()
		s.release()
		s.closeErr = errors.Join(decoderErr, sourceErr)
	})
	return s.closeErr
}
