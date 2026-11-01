package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTransactionBoundsAndPublication(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, 16)
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.TryBegin("track.webm", 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.TryBegin("other.webm", 8); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("123456789")); !errors.Is(err, ErrEntryTooLarge) {
		t.Fatal(err)
	}
	if err := w.Commit(context.Background()); !errors.Is(err, ErrEntryTooLarge) {
		t.Fatal("failed write became committable", err)
	}
	if _, ok := c.Acquire("track.webm"); ok {
		t.Fatal("partial became visible")
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	w, err = c.TryBegin("track.webm", 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewCache(dir, 16)
	if err != nil {
		t.Fatal(err)
	}
	lease, ok := restarted.Acquire("track.webm")
	if !ok {
		t.Fatal("committed bytes did not survive restart")
	}
	defer lease.Release()
	data, err := os.ReadFile(lease.Path)
	if err != nil || string(data) != "12345678" {
		t.Fatal("published wrong bytes", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "other.webm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestCanceledAndEmptyTransactionsLeaveNoFiles(t *testing.T) {
	for _, empty := range []bool{false, true} {
		dir := t.TempDir()
		c, err := NewCache(dir, 64)
		if err != nil {
			t.Fatal(err)
		}
		w, err := c.TryBegin("track.webm", 32)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if !empty {
			if _, err := w.Write([]byte("data")); err != nil {
				t.Fatal(err)
			}
			cancel()
		}
		if err := w.Commit(ctx); err == nil {
			t.Fatal("invalid transaction committed")
		}
		cancel()
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("temporary files retained", err)
		}
		w, err = c.TryBegin("again.webm", 32)
		if err != nil {
			t.Fatal("writer slot leaked", err)
		}
		if err := w.Abort(); err != nil {
			t.Fatal(err)
		}
	}
}
