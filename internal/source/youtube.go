// Package source implements authorized media acquisition for playback.
package source

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/audio"
	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/sourceurl"
)

// YouTubeOpener owns cache lookup and bounded acquisition for each attempt.
// Callers close/join returned streams before releasing dependencies. There are
// no opener-wide workers or hidden retries. Concurrent opens are supported.
type YouTubeOpener struct {
	limiter          *processlimit.Limiter
	store            *cache.Cache
	logger           *log.Logger
	executable       string
	startupBudget    time.Duration
	preparationDelay time.Duration
}

func NewYouTubeOpener(limiter *processlimit.Limiter, store *cache.Cache, logger *log.Logger) (*YouTubeOpener, error) {
	if limiter == nil || limiter.Capacity() < 1 || store == nil || logger == nil {
		return nil, errors.New("source dependencies required")
	}
	return &YouTubeOpener{limiter: limiter, store: store, logger: logger, executable: "yt-dlp", startupBudget: 30 * time.Second, preparationDelay: 2 * time.Second}, nil
}

func (o *YouTubeOpener) Open(parent context.Context, request playback.Request) (stream playback.Stream, result error) {
	url, err := sourceurl.ValidateCanonicalYouTubeVideoURL(request.MediaID)
	if err != nil {
		return nil, failure.Wrap("validate", failure.Source, err)
	}
	ctx, cancel := context.WithCancelCause(parent)
	s := &extractorStream{ctx: ctx, cancel: cancel, logger: o.logger, request: request, startupDone: make(chan struct{})}
	timeout := failure.Wrap("prepare", failure.Source, context.DeadlineExceeded)
	timeout.Reason = failure.StartupTimeout
	s.startup = time.AfterFunc(o.startupBudget, func() { cancel(timeout); close(s.startupDone) })
	defer func() {
		if result != nil {
			_ = s.Close()
		}
	}()
	key := cache.GenerateKey(url)
	if !request.Live {
		if lease, ok := o.store.Acquire(key); ok {
			s.lease = lease
			file, err := audio.NewFileOpener(lease.Path)
			if err != nil {
				return nil, err
			}
			s.decoder, err = file.Open(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := s.prime(); err != nil {
				return nil, err
			}
			return s, nil
		}
	}
	release, err := o.limiter.Acquire(ctx)
	if err != nil {
		return nil, s.classify("admission", err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		release()
		return nil, failure.Wrap("acquire", failure.Source, err)
	}
	s.reader = r
	// #nosec G204 -- fixed executable; canonical input follows --.
	s.cmd = exec.CommandContext(ctx, o.executable, extractionArgs(url)...)
	configureProcess(s.cmd)
	s.cmd.Stdout, s.cmd.Stderr = w, &s.stderr
	if err := s.cmd.Start(); err != nil {
		_ = w.Close()
		release()
		return nil, s.classify("acquire", err)
	}
	_ = w.Close()
	s.buffer = newByteBuffer(readAheadBytes)
	s.acquired = make(chan struct{})
	go s.acquire(release, o.store, key)
	if err := s.buffer.prepare(ctx, o.preparationDelay); err != nil {
		return nil, s.classify("prepare", err)
	}
	dr, dw, err := os.Pipe()
	if err != nil {
		return nil, failure.Wrap("decode_pipe", failure.Source, err)
	}
	s.feeder = dw
	s.decoder, err = audio.Decode(ctx, dr, request.Offset, request.Controls)
	_ = dr.Close()
	if err != nil {
		return nil, err
	}
	s.fed = make(chan struct{})
	go s.feed()
	if err := s.prime(); err != nil {
		return nil, err
	}
	return s, nil
}

type extractorStream struct {
	ctx                 context.Context
	cancel              context.CancelCauseFunc
	logger              *log.Logger
	request             playback.Request
	cmd                 *exec.Cmd
	reader, feeder      *os.File
	buffer              *byteBuffer
	stderr              failure.Diagnostic
	decoder             playback.Stream
	acquired, fed       chan struct{}
	acquireErr, feedErr error        // Read only after the corresponding join closes.
	write               *cache.Write // Producer writes; consumer commits/aborts after join.
	lease               *cache.Lease
	startup             *time.Timer
	startupDone         chan struct{}
	startupOnce         sync.Once
	closeOnce           sync.Once
	closeErr            error
	hasAudio            bool
	firstPacket         [playback.MaxPacketBytes]byte
	firstSize           int
}

func (s *extractorStream) classify(stage string, err error) error {
	if s.ctx.Err() != nil {
		return context.Cause(s.ctx)
	}
	var classified *failure.Error
	if errors.As(err, &classified) {
		return err
	}
	return failure.Wrap(stage, failure.Source, err)
}

func (s *extractorStream) stopStartup() {
	s.startupOnce.Do(func() {
		if !s.startup.Stop() {
			<-s.startupDone
		}
	})
}

func (s *extractorStream) cacheWarning(err error) {
	reason := "write_failed"
	if errors.Is(err, cache.ErrEntryTooLarge) {
		reason = "entry_budget"
	}
	s.logger.Warn("Optional cache write disabled", "event", "cache.fill", "request_id", s.request.ID,
		"attempt_id", s.request.AttemptID, "category", failure.Source, "reason", reason, "recovery", "stream_without_cache")
}

func (s *extractorStream) acquire(release func(), store *cache.Cache, key string) {
	defer close(s.acquired)
	defer func() { s.buffer.finish(s.acquireErr) }()
	if !s.request.Live {
		var err error
		s.write, err = store.TryBegin(key, 256<<20)
		if err != nil && !errors.Is(err, cache.ErrBusy) {
			s.cacheWarning(err)
		}
	}
	var block [acquisitionBlockBytes]byte
	var readErr error
	for {
		n, err := s.reader.Read(block[:])
		if n > 0 {
			if s.write != nil {
				if _, writeErr := s.write.Write(block[:n]); writeErr != nil {
					s.cacheWarning(errors.Join(writeErr, s.write.Abort()))
					s.write = nil
				}
			}
			if readErr = s.buffer.write(s.ctx, block[:n]); readErr != nil {
				break
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	_ = s.reader.Close()
	if readErr != nil && s.ctx.Err() == nil {
		s.cancel(failure.Wrap("acquire_read", failure.Source, readErr))
	}
	// A child can close stdout without exiting. Bound and join finalization.
	joined := make(chan struct{})
	timer := time.AfterFunc(2*time.Second, func() {
		s.cancel(failure.Wrap("acquire_finalize", failure.Source, context.DeadlineExceeded))
		close(joined)
	})
	processErr := s.cmd.Wait()
	if !timer.Stop() {
		<-joined
	}
	release()
	if s.ctx.Err() != nil {
		var exitErr *exec.ExitError
		if errors.Is(processErr, context.Canceled) || errors.Is(processErr, context.DeadlineExceeded) ||
			errors.As(processErr, &exitErr) && exitErr.ExitCode() == -1 {
			// The owned process group is killed on cancellation. Its signal exit
			// is teardown, not a new source failure. Nonzero normal exits remain
			// authoritative even when cancellation races Wait.
			processErr = nil
		}
	}
	if processErr != nil {
		s.acquireErr = s.stderr.Failure("acquire", failure.Source, processErr)
	} else if cause := failure.From(context.Cause(s.ctx)); cause != nil {
		// Preserve acquisition read/finalization failures that canceled the
		// child themselves, including the bounded finalization deadline.
		s.acquireErr = cause
	} else if readErr != nil && s.ctx.Err() == nil {
		s.acquireErr = failure.Wrap("acquire_read", failure.Source, readErr)
	}
}

func (s *extractorStream) feed() {
	defer close(s.fed)
	defer s.feeder.Close()
	var block [acquisitionBlockBytes]byte
	for {
		n, err := s.buffer.read(s.ctx, block[:])
		if n > 0 {
			if _, s.feedErr = s.feeder.Write(block[:n]); s.feedErr != nil {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.feedErr = err
			}
			return
		}
	}
}

func (s *extractorStream) Next(ctx context.Context, packet []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.firstSize > 0 {
		if len(packet) < s.firstSize {
			return 0, failure.Wrap("read", failure.InvalidFrame, io.ErrShortBuffer)
		}
		n := copy(packet, s.firstPacket[:s.firstSize])
		s.firstSize = 0
		return n, nil
	}
	return s.next(ctx, packet)
}

// Prime one packet before returning Open. A user's intentional pause after
// preparation must not be mistaken for a stalled first-audio deadline.
func (s *extractorStream) prime() error {
	n, err := s.next(s.ctx, s.firstPacket[:])
	if errors.Is(err, io.EOF) {
		return failure.Wrap("prepare", failure.ZeroFrames, err)
	}
	if err != nil {
		return err
	}
	if n <= 0 {
		return failure.Wrap("prepare", failure.ZeroFrames, errors.New("decoder returned no packet"))
	}
	s.firstSize = n
	return nil
}

func (s *extractorStream) next(ctx context.Context, packet []byte) (int, error) {
	n, err := s.decoder.Next(ctx, packet)
	if err == nil {
		s.stopStartup()
		if s.ctx.Err() != nil {
			return 0, context.Cause(s.ctx)
		}
		s.hasAudio = s.hasAudio || n > 0
		return n, nil
	}
	s.stopStartup()
	if s.ctx.Err() != nil {
		return 0, context.Cause(s.ctx)
	}
	if !errors.Is(err, io.EOF) {
		s.cancel(err)
	}
	if s.acquired != nil {
		// Early decoder exit must not strand acquisition on a full buffer.
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-s.acquired:
		case <-s.ctx.Done():
			<-s.acquired
		case <-timer.C:
			s.cancel(failure.Wrap("acquire_finalize", failure.Source, context.DeadlineExceeded))
			<-s.acquired
		}
		timer.Stop()
		if s.acquireErr != nil {
			var f *failure.Error
			if errors.As(s.acquireErr, &f) && f.ExitCode >= 0 {
				return 0, s.acquireErr
			}
			if errors.Is(err, io.EOF) {
				return 0, s.acquireErr
			}
		}
	}
	if errors.Is(err, io.EOF) && s.fed != nil {
		<-s.fed
		if s.feedErr != nil {
			return 0, failure.Wrap("decode_feed", failure.Codec, s.feedErr)
		}
	}
	if errors.Is(err, io.EOF) && s.write != nil && s.hasAudio {
		if commitErr := s.write.Commit(s.ctx); commitErr != nil {
			s.cacheWarning(commitErr)
		}
		s.write = nil
	}
	return n, err
}

func (s *extractorStream) Close() error {
	s.closeOnce.Do(func() {
		s.stopStartup()
		s.cancel(context.Canceled)
		if s.reader != nil {
			_ = s.reader.Close()
		}
		if s.feeder != nil {
			_ = s.feeder.Close()
		}
		if s.decoder != nil {
			s.closeErr = s.decoder.Close()
		}
		if s.acquired != nil {
			<-s.acquired
		}
		if s.fed != nil {
			<-s.fed
		}
		if s.write != nil {
			if err := s.write.Abort(); err != nil {
				s.cacheWarning(err)
			}
			s.write = nil
		}
		if s.lease != nil {
			s.lease.Release()
		}
		s.closeErr = errors.Join(s.closeErr, s.acquireErr)
	})
	return s.closeErr
}
