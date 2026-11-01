package cache

import (
	"context"
	"errors"
	"io"
	"os"
)

// Write is an optional, bounded cache transaction. One acquisition worker owns
// Write/Commit/Abort serially; it must finish with Commit or Abort. The cache
// admits at most one transaction. No cache mutex is held during filesystem I/O.
type Write struct {
	cache           *Cache
	file            *os.File
	key, path       string
	remaining, size int64
	done            bool
	failed          error
}

// TryBegin never waits for another writer. Saturation is ErrBusy; callers may
// continue uncached. Only this transaction's newly created temporary file is
// removed on abort. Existing entries and unrelated files are never modified.
func (c *Cache) TryBegin(key string, maxBytes int64) (*Write, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		return nil, ErrEntryTooLarge
	}
	if c.writeFailed.Load() {
		return nil, errors.New("cache scratch cleanup previously failed")
	}
	select {
	case c.writer <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	path, err := c.tempPath(key)
	if err != nil {
		<-c.writer
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		<-c.writer
		return nil, err
	}
	return &Write{cache: c, file: f, key: key, path: path, remaining: min(maxBytes, c.maxSize)}, nil
}

func (w *Write) Write(p []byte) (int, error) {
	if w.done {
		return 0, os.ErrClosed
	}
	if w.failed != nil {
		return 0, w.failed
	}
	if int64(len(p)) > w.remaining {
		w.failed = ErrEntryTooLarge
		return 0, ErrEntryTooLarge
	}
	n, err := w.file.Write(p)
	w.remaining -= int64(n)
	w.size += int64(n)
	w.failed = err
	return n, err
}

// Commit publishes only complete caller-verified acquisition. Empty writes are
// rejected. It always releases the transaction, including on publication error.
func (w *Write) Commit(ctx context.Context) (result error) {
	if w.done {
		return os.ErrClosed
	}
	defer func() { result = errors.Join(result, w.Abort()) }()
	if w.failed != nil {
		return w.failed
	}
	if w.size == 0 {
		return io.ErrUnexpectedEOF
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := w.file.Close(); err != nil {
		w.file = nil
		return err
	}
	w.file = nil
	_, err := w.cache.commitTemp(ctx, w.key, w.path, w.size)
	return err
}

// Abort is idempotent. Failed scratch removal disables further transactions
// until restart, preventing abandoned temporary files from exceeding the bound.
func (w *Write) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	var closeErr error
	if w.file != nil {
		closeErr = w.file.Close()
		w.file = nil
	}
	err := os.Remove(w.path)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		w.cache.writeFailed.Store(true)
	}
	<-w.cache.writer
	return errors.Join(closeErr, err)
}
