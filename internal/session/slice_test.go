package session

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hraban/opus"

	"github.com/GrainedLotus515/gobard/internal/audio"
	"github.com/GrainedLotus515/gobard/internal/playback"
)

type voice struct {
	decoder *opus.Decoder
	frames  int
	samples [1920]int16
	entered chan struct{}
	once    sync.Once
}

func (v *voice) Send(ctx context.Context, packet []byte) error {
	if v.entered != nil {
		v.once.Do(func() { close(v.entered) })
		<-ctx.Done()
		return ctx.Err()
	}
	n, err := v.decoder.Decode(packet, v.samples[:])
	if err != nil {
		return err
	}
	if n != 960 {
		return errors.New("wrong frame duration")
	}
	v.frames++
	return nil
}

func fixture(t testing.TB, frames int) string {
	t.Helper()
	if frames < 0 || frames > 1000 {
		t.Fatal("fixture frame budget exceeded")
	}
	// Real stereo PCM WAV, 20 ms/frame, recognizable nonzero square wave.
	pcm := make([]byte, frames*960*4)
	for i := 0; i < len(pcm); i += 2 {
		binary.LittleEndian.PutUint16(pcm[i:], uint16((i/100%2)*8000))
	}
	b := make([]byte, 44+len(pcm))
	copy(b, "RIFF")
	// #nosec G115 -- at most 1000 frames, below the WAV uint32 size limit.
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 2)
	binary.LittleEndian.PutUint32(b[24:], 48000)
	binary.LittleEndian.PutUint32(b[28:], 192000)
	binary.LittleEndian.PutUint16(b[32:], 4)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	// #nosec G115 -- at most 1000 frames, below the WAV uint32 size limit.
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)))
	copy(b[44:], pcm)
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func BenchmarkRealMedia(b *testing.B) {
	opener, err := audio.NewFileOpener(fixture(b, 1000))
	if err != nil {
		b.Fatal(err)
	}
	engine, err := playback.New(opener, log.New(io.Discard))
	if err != nil {
		b.Fatal(err)
	}
	decoder, err := opus.NewDecoder(48000, 2)
	if err != nil {
		b.Fatal(err)
	}
	v := &voice{decoder: decoder}
	b.ReportAllocs()
	b.SetBytes(1000 * 3840)
	for b.Loop() {
		result := engine.Run(context.Background(), playback.Request{}, v)
		if result.Outcome != playback.Completed || result.Frames != 1000 {
			b.Fatal(result)
		}
	}
}

func newSlice(t *testing.T, frames, capacity int, output playback.Output) *Session {
	t.Helper()
	o, err := audio.NewFileOpener(fixture(t, frames))
	if err != nil {
		t.Fatal(err)
	}
	e, err := playback.New(o, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), "fixture", e, output, capacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestRealMediaThroughSessionAndOpus(t *testing.T) {
	decoder, err := opus.NewDecoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	v := &voice{decoder: decoder}
	s := newSlice(t, 10, 2, v)
	ticket, err := s.Enqueue(context.Background(), playback.Request{Provider: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := ticket.Wait(ctx)
	if err != nil || r.Outcome != playback.Completed || r.Frames != 10 || v.frames != 10 {
		t.Fatalf("%+v frames=%d err=%v", r, v.frames, err)
	}
	var energy int64
	for _, sample := range v.samples {
		energy += int64(sample) * int64(sample)
	}
	if energy == 0 {
		t.Fatal("decoded output was silent")
	}
	s.Close()
	s.Close()
}

func TestRealMediaBackpressureAndQueueBudget(t *testing.T) {
	v := &voice{entered: make(chan struct{})}
	s := newSlice(t, 100, 2, v)
	first, err := s.Enqueue(context.Background(), playback.Request{})
	if err != nil {
		t.Fatal(err)
	}
	<-v.entered
	second, err := s.Enqueue(context.Background(), playback.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(context.Background(), playback.Request{}); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	if err := s.Control(context.Background(), playback.Stopped); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, ticket := range []*Ticket{first, second} {
		r, err := ticket.Wait(ctx)
		if err != nil || r.Outcome != playback.Stopped || r.Frames != 0 {
			t.Fatalf("%+v %v", r, err)
		}
	}
	s.Close()
	if _, err := s.Enqueue(context.Background(), playback.Request{}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestRealMediaZeroFrames(t *testing.T) {
	s := newSlice(t, 0, 1, &voice{})
	ticket, err := s.Enqueue(context.Background(), playback.Request{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := ticket.Wait(ctx)
	if err != nil || r.Outcome != playback.EncodeFailure || r.Frames != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}
