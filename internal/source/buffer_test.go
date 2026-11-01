package source

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestReadAheadPreservesBytesAcrossWraparound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b := newByteBuffer(257)
	want := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz0123456789"), 10000)
	done := make(chan error, 1)
	go func() { err := b.write(ctx, want); b.finish(err); done <- err }()
	var actual bytes.Buffer
	block := make([]byte, 101)
	for {
		n, err := b.read(ctx, block)
		actual.Write(block[:n])
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Error(err)
			}
			break
		}
	}
	if err := <-done; err != nil || !bytes.Equal(want, actual.Bytes()) {
		t.Fatal("bytes lost or reordered", err)
	}
	if len(b.data) != 257 {
		t.Fatal("storage grew")
	}
}

func TestReadAheadCancellationAtEmptyAndFullBoundaries(t *testing.T) {
	for _, full := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		b := newByteBuffer(32)
		if full {
			if err := b.write(ctx, make([]byte, 32)); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan error, 1)
		go func() {
			if full {
				done <- b.write(ctx, []byte{1})
				return
			}
			_, err := b.read(ctx, make([]byte, 1))
			done <- err
		}()
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("buffer did not cancel")
		}
	}
}

func TestPreparationWaitsForBytesAndPreservesFailure(t *testing.T) {
	b := newByteBuffer(32)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.prepare(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cause := errors.New("source rejected")
	b.finish(cause)
	if err := b.prepare(context.Background(), time.Second); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	b = newByteBuffer(32)
	if err := b.write(context.Background(), []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := b.prepare(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 1)
	if n, err := b.read(context.Background(), packet); n != 1 || err != nil || packet[0] != 1 {
		t.Fatal(n, err)
	}
}
