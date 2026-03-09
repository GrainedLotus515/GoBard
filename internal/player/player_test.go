package player

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestSeekSignalsPlaybackRestart(t *testing.T) {
	p := NewManager().GetPlayer("guild-1")

	track := &Track{
		Title:    "test",
		Duration: 2 * time.Minute,
	}
	p.Queue.Add(track)
	if got := p.Queue.Next(); got == nil {
		t.Fatal("Queue.Next() returned nil, expected current track")
	}

	p.Playing = true

	if err := p.Seek(30 * time.Second); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}

	if got := p.GetCurrentPosition(); got != 30*time.Second {
		t.Fatalf("current position = %v, want %v", got, 30*time.Second)
	}
	if p.requestedStartOffset != 30*time.Second {
		t.Fatalf("requested start offset = %v, want %v", p.requestedStartOffset, 30*time.Second)
	}

	if !p.ConsumeSeekRequest() {
		t.Fatal("ConsumeSeekRequest() = false, want true")
	}
	if p.ConsumeSeekRequest() {
		t.Fatal("ConsumeSeekRequest() should clear pending request")
	}

	if p.Playing {
		t.Fatal("playing should be false after seek stop signal")
	}

	if got := p.Queue.Current(); got != track {
		t.Fatalf("current track changed after seek: got %v want %v", got, track)
	}

	select {
	case <-p.stopChan:
	default:
		t.Fatal("seek should signal stop channel")
	}
}

func TestStopClearsSeekStateAndPosition(t *testing.T) {
	p := NewManager().GetPlayer("guild-2")

	track := &Track{
		Title:    "test",
		Duration: time.Minute,
	}
	p.Queue.Add(track)
	if got := p.Queue.Next(); got == nil {
		t.Fatal("Queue.Next() returned nil, expected current track")
	}

	if err := p.Seek(15 * time.Second); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}

	p.Stop()

	if got := p.GetCurrentPosition(); got != 0 {
		t.Fatalf("current position after Stop() = %v, want 0", got)
	}
	if p.requestedStartOffset != 0 {
		t.Fatalf("requested start offset after Stop() = %v, want 0", p.requestedStartOffset)
	}

	if p.ConsumeSeekRequest() {
		t.Fatal("seek request should be cleared by Stop()")
	}
}

func TestGetCurrentPositionAdvancesWithPlaybackClock(t *testing.T) {
	p := NewManager().GetPlayer("guild-3")
	p.CurrentPosition = 5 * time.Second
	p.Playing = true
	p.playbackStartedAt = time.Now().Add(-1500 * time.Millisecond)

	got := p.GetCurrentPosition()
	if got < 6400*time.Millisecond || got > 7600*time.Millisecond {
		t.Fatalf("GetCurrentPosition() = %v, want approximately 6.5s", got)
	}
}

func TestPlayTrackPacesOpusFrames(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	originalNowOpusFrame := nowOpusFrame
	originalSleepOpusFrame := sleepOpusFrame
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newCustomEncoder = originalNewCustomEncoder
		nowOpusFrame = originalNowOpusFrame
		sleepOpusFrame = originalSleepOpusFrame
		sleepVoiceReady = originalSleepVoiceReady
	})

	fakeNow := time.Unix(0, 0)
	encoder := &stubEncoder{
		frames: [][]byte{
			[]byte("frame-1"),
			[]byte("frame-2"),
			[]byte("frame-3"),
		},
		frameDelay: 5 * time.Millisecond,
		advance: func(d time.Duration) {
			fakeNow = fakeNow.Add(d)
		},
	}
	newCustomEncoder = func(string, int, int, time.Duration) (EncoderInterface, error) {
		return encoder, nil
	}
	nowOpusFrame = func() time.Time {
		return fakeNow
	}

	var (
		sleepMu        sync.Mutex
		sleepDurations []time.Duration
	)
	sleepOpusFrame = func(d time.Duration) {
		fakeNow = fakeNow.Add(d)
		sleepMu.Lock()
		sleepDurations = append(sleepDurations, d)
		sleepMu.Unlock()
	}
	sleepVoiceReady = func() {}

	p := NewManager().GetPlayer("guild-pacing")
	vc := &stubVoiceConnection{}
	p.SetVoiceConnection(vc)

	track := &Track{
		Title:     "paced",
		LocalPath: "/tmp/paced.opus",
	}
	p.Queue.Add(track)

	if err := p.Play(); err != nil {
		t.Fatalf("Play() error = %v", err)
	}
	p.WaitForCompletion()

	if got := len(vc.frames); got != 3 {
		t.Fatalf("sent frames = %d, want 3", got)
	}
	if !encoder.cleaned {
		t.Fatal("encoder Cleanup() was not called")
	}

	sleepMu.Lock()
	gotSleeps := append([]time.Duration(nil), sleepDurations...)
	sleepMu.Unlock()

	wantSleeps := []time.Duration{
		opusFrameInterval - encoder.frameDelay,
		opusFrameInterval - encoder.frameDelay,
		opusFrameInterval - encoder.frameDelay,
	}
	if !reflect.DeepEqual(gotSleeps, wantSleeps) {
		t.Fatalf("sleep durations = %v, want %v", gotSleeps, wantSleeps)
	}
}

func TestPlaybackSignalsCloseStartedBeforeDone(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newCustomEncoder = originalNewCustomEncoder
		sleepVoiceReady = originalSleepVoiceReady
	})

	secondFrameBlocked := make(chan struct{})
	releaseSecondFrame := make(chan struct{})
	newCustomEncoder = func(string, int, int, time.Duration) (EncoderInterface, error) {
		return &stubEncoder{
			frames: [][]byte{
				[]byte("frame-1"),
				[]byte("frame-2"),
				[]byte("frame-3"),
			},
		}, nil
	}
	sleepVoiceReady = func() {}

	p := NewManager().GetPlayer("guild-signals")
	vc := &stubVoiceConnection{
		sendHook: func(_ []byte, sendCount int) {
			if sendCount == 2 {
				close(secondFrameBlocked)
				<-releaseSecondFrame
			}
		},
	}
	p.SetVoiceConnection(vc)
	p.Queue.Add(&Track{Title: "signal-track", LocalPath: "/tmp/signal.opus"})

	if err := p.Play(); err != nil {
		t.Fatalf("Play() error = %v", err)
	}

	started, done := p.PlaybackSignals()

	select {
	case <-secondFrameBlocked:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second frame was not blocked in time")
	}

	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("started signal did not close after first frame")
	}

	select {
	case <-done:
		t.Fatal("done signal closed before playback completed")
	default:
	}

	close(releaseSecondFrame)
	p.WaitForCompletion()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("done signal did not close after playback completed")
	}
}

func TestPlaybackSignalsCloseDoneOnEncoderFailureWithoutStarted(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newCustomEncoder = originalNewCustomEncoder
		sleepVoiceReady = originalSleepVoiceReady
	})

	newCustomEncoder = func(string, int, int, time.Duration) (EncoderInterface, error) {
		return nil, errors.New("boom")
	}
	sleepVoiceReady = func() {}

	p := NewManager().GetPlayer("guild-encoder-fail")
	p.SetVoiceConnection(&stubVoiceConnection{})
	p.Queue.Add(&Track{Title: "broken-track", LocalPath: "/tmp/broken.opus"})

	if err := p.Play(); err != nil {
		t.Fatalf("Play() error = %v", err)
	}

	started, done := p.PlaybackSignals()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("done signal did not close after encoder creation failure")
	}

	select {
	case <-started:
		t.Fatal("started signal closed even though playback never began")
	default:
	}
}

func TestWaitForCompletionUnblocksWhenDoneChannelCloses(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newCustomEncoder = originalNewCustomEncoder
		sleepVoiceReady = originalSleepVoiceReady
	})

	newCustomEncoder = func(string, int, int, time.Duration) (EncoderInterface, error) {
		return &stubEncoder{
			frames: [][]byte{
				[]byte("frame-1"),
			},
		}, nil
	}
	sleepVoiceReady = func() {}

	p := NewManager().GetPlayer("guild-wait")
	p.SetVoiceConnection(&stubVoiceConnection{})
	p.Queue.Add(&Track{Title: "wait-track", LocalPath: "/tmp/wait.opus"})

	if err := p.Play(); err != nil {
		t.Fatalf("Play() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		p.WaitForCompletion()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitForCompletion() did not return after playback finished")
	}
}

type stubEncoder struct {
	frames     [][]byte
	index      int
	cleaned    bool
	frameDelay time.Duration
	advance    func(time.Duration)
	cleanupMu  sync.Mutex
}

func (e *stubEncoder) OpusFrame() ([]byte, error) {
	if e.index >= len(e.frames) {
		return nil, io.EOF
	}
	if e.advance != nil && e.frameDelay > 0 {
		e.advance(e.frameDelay)
	}
	frame := e.frames[e.index]
	e.index++
	return frame, nil
}

func (e *stubEncoder) Cleanup() error {
	e.cleanupMu.Lock()
	defer e.cleanupMu.Unlock()
	e.cleaned = true
	return nil
}

type stubVoiceConnection struct {
	frames      [][]byte
	speaking    []bool
	disconnects int
	sendHook    func(frame []byte, sendCount int)
	mu          sync.Mutex
}

func (c *stubVoiceConnection) SetSpeaking(_ context.Context, speaking bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.speaking = append(c.speaking, speaking)
	return nil
}

func (c *stubVoiceConnection) SendOpusFrame(frame []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames = append(c.frames, append([]byte(nil), frame...))
	if c.sendHook != nil {
		c.sendHook(frame, len(c.frames))
	}
	return nil
}

func (c *stubVoiceConnection) Disconnect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnects++
	return nil
}
