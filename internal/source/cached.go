package source

import (
	"context"
	"errors"
	"sync"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/audio"
	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/sourceurl"
)

type Downloader interface {
	Download(context.Context, string, string, int64) error
}

// Cached owns one optional warming worker with eight queued hints. Saturation
// drops cache hints, never playback. Close cancels and joins the worker; callers
// must first join attempts, which independently own their acquired cache leases.
type Cached struct {
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	warm       chan string
	store      *cache.Cache
	opener     playback.Opener
	downloader Downloader
	logger     *log.Logger
	maxBytes   int64
	closeOnce  sync.Once
}

func NewCached(parent context.Context, store *cache.Cache, opener playback.Opener, downloader Downloader, logger *log.Logger) (*Cached, error) {
	if parent == nil || store == nil || opener == nil || downloader == nil || logger == nil {
		return nil, errors.New("cache source dependencies required")
	}
	_, _, limit := store.GetStats()
	// #nosec G118 -- Cached.Close owns cancellation; parent cancellation also stops work.
	ctx, cancel := context.WithCancel(parent)
	c := &Cached{ctx: ctx, cancel: cancel, done: make(chan struct{}), warm: make(chan string, 8), store: store, opener: opener, downloader: downloader, logger: logger, maxBytes: min(limit, 256<<20)}
	go c.run()
	return c, nil
}

func (c *Cached) Open(ctx context.Context, request playback.Request) (playback.Stream, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, failure.Wrap("cache_closed", failure.Source, err)
	}
	url, err := sourceurl.ValidateCanonicalYouTubeVideoURL(request.MediaID)
	if err != nil {
		return nil, failure.Wrap("cache_validate", failure.Source, err)
	}
	if lease, ok := c.store.Acquire(cache.GenerateKey(url)); ok {
		opener, err := audio.NewFileOpener(lease.Path)
		if err != nil {
			lease.Release()
			return nil, err
		}
		stream, err := opener.Open(ctx, request)
		if err != nil {
			lease.Release()
			return nil, err
		}
		return &leasedStream{Stream: stream, lease: lease}, nil
	}
	stream, err := c.opener.Open(ctx, request)
	if err != nil {
		return nil, err
	}
	return &warmingStream{Stream: stream, owner: c, url: url}, nil
}

func (c *Cached) Close() { c.closeOnce.Do(c.cancel); <-c.done }

func (c *Cached) run() {
	defer close(c.done)
	for {
		select {
		case <-c.ctx.Done():
			return
		case url := <-c.warm:
			if c.ctx.Err() != nil {
				return
			}
			key := cache.GenerateKey(url)
			_, err := c.store.GetOrCreate(key, func(path string) error { return c.downloader.Download(c.ctx, url, path, c.maxBytes) })
			if err != nil && c.ctx.Err() == nil {
				category := failure.Source
				var classified *failure.Error
				if errors.As(err, &classified) {
					category = classified.Category
				}
				c.logger.Warn("Optional cache fill failed", "event", "cache.fill", "key", key, "category", category, "recovery", "stream_without_cache")
			}
		}
	}
}

type leasedStream struct {
	playback.Stream
	lease *cache.Lease
}

func (s *leasedStream) Close() error { defer s.lease.Release(); return s.Stream.Close() }

type warmingStream struct {
	playback.Stream
	owner   *Cached
	url     string
	offered bool
}

func (s *warmingStream) Next(ctx context.Context, packet []byte) (int, error) {
	n, err := s.Stream.Next(ctx, packet)
	if n > 0 && err == nil && !s.offered {
		s.offered = true
		select {
		case s.owner.warm <- s.url:
		default:
		}
	}
	return n, err
}
