// Package audio decodes trusted media to bounded PCM and encodes Discord Opus.
package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/hraban/opus"

	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

const samplesPerFrame = 960 * 2

// FileOpener is for operator-owned test media, never a Discord path/URL input.
// Every Open acquires its own descriptor. FFmpeg can read only the pipe protocol,
// preventing nested playlists from fetching URLs or opening other local files.
type FileOpener struct{ path string }

func NewFileOpener(path string) (*FileOpener, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, failure.Wrap("fixture", failure.Source, err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("fixture must be a regular file")
	}
	return &FileOpener{path: path}, nil
}

func (o *FileOpener) Open(ctx context.Context, request playback.Request) (playback.Stream, error) {
	f, err := os.Open(o.path)
	if err != nil {
		return nil, failure.Wrap("open", failure.Source, err)
	}
	stream, err := Decode(ctx, f, request.Offset)
	// exec duplicates the file descriptor for the child; the parent releases it.
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		_ = stream.Close()
		return nil, failure.Wrap("open", failure.Source, closeErr)
	}
	return stream, nil
}

// Decode starts FFmpeg with caller-owned input (an OS descriptor, not a copier
// goroutine). The caller closes its descriptor after this returns. Close on the
// returned stream always cancels and reaps the child. Offset is decoded from a
// nonseekable pipe; large offsets cost decoding time and remain cancellable.
func Decode(parent context.Context, input *os.File, offset time.Duration) (playback.Stream, error) {
	if input == nil || offset < 0 {
		return nil, errors.New("invalid decoder input or offset")
	}
	encoder, err := opus.NewEncoder(48000, 2, opus.AppAudio)
	if err != nil {
		return nil, failure.Wrap("encode", failure.Codec, err)
	}
	ctx, cancel := context.WithCancel(parent)
	r, w, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, failure.Wrap("decode", failure.Codec, err)
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "pipe", "-i", "pipe:0"}
	if offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(offset.Seconds(), 'f', 3, 64))
	}
	args = append(args, "-vn", "-f", "s16le", "-ar", "48000", "-ac", "2", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.WaitDelay = time.Second
	s := &pcmStream{cmd: cmd, cancel: cancel, stdout: r, encoder: encoder}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, w, &s.stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = r.Close()
		_ = w.Close()
		return nil, failure.Wrap("decode", failure.Codec, err)
	}
	_ = w.Close()
	return s, nil
}

type pcmStream struct {
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	stdout    *os.File
	stderr    failure.Diagnostic
	encoder   *opus.Encoder
	pcm       [samplesPerFrame * 2]byte
	samples   [samplesPerFrame]int16
	waitOnce  sync.Once
	waitErr   error
	closeOnce sync.Once
	closeErr  error
	ended     bool
}

func (s *pcmStream) Next(ctx context.Context, packet []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.ended {
		return 0, io.EOF
	}
	n, err := io.ReadFull(s.stdout, s.pcm[:])
	if err != nil {
		s.ended = true
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, failure.Wrap("decode", failure.Codec, err)
		}
		if err := s.wait(); err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		if n%4 != 0 {
			return 0, failure.Wrap("decode", failure.Codec, errors.New("partial stereo sample"))
		}
		clear(s.pcm[n:])
	}
	for i := range s.samples {
		// #nosec G115 -- reinterpret the signed 16-bit PCM representation.
		s.samples[i] = int16(binary.LittleEndian.Uint16(s.pcm[i*2:]))
	}
	n, err = s.encoder.Encode(s.samples[:], packet)
	if err != nil {
		return 0, failure.Wrap("encode", failure.Codec, err)
	}
	return n, nil
}

func (s *pcmStream) wait() error {
	s.waitOnce.Do(func() {
		if err := s.cmd.Wait(); err != nil {
			s.waitErr = s.stderr.Failure("decode", failure.Codec, err)
		}
	})
	return s.waitErr
}

func (s *pcmStream) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.stdout.Close()
		s.closeErr = s.wait()
	})
	return s.closeErr
}
