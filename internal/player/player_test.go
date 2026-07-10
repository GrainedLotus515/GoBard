package player

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
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

	session := newPlaybackSession()
	p.activePlayback = session
	p.lastPlayback = session
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
	case <-session.stop:
	default:
		t.Fatal("seek should stop the active playback session")
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

	session := newPlaybackSession()
	p.activePlayback = session
	p.lastPlayback = session
	p.Playing = true

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

func TestWaitForCompletionUsesCurrentPlaybackSession(t *testing.T) {
	p := NewManager().GetPlayer("guild-wait")

	stale := newPlaybackSession()
	current := newPlaybackSession()
	p.activePlayback = current
	p.lastPlayback = current
	stale.signalDone()

	done := make(chan struct{})
	go func() {
		p.WaitForCompletion()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("WaitForCompletion() returned before the active session completed")
	case <-time.After(50 * time.Millisecond):
	}

	current.signalDone()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitForCompletion() did not return after the active session completed")
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

func TestPlayTrackStopsBeforeSendingBufferedFrame(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newCustomEncoder = originalNewCustomEncoder
		sleepVoiceReady = originalSleepVoiceReady
	})

	p := NewManager().GetPlayer("guild-stop-buffered")
	vc := &stubVoiceConnection{}
	p.SetVoiceConnection(vc)

	encoder := &stubEncoder{
		frames: [][]byte{
			[]byte("frame-1"),
			[]byte("frame-2"),
		},
		onFrame: func(nextIndex int) {
			if nextIndex == 2 {
				p.Stop()
			}
		},
	}
	newCustomEncoder = func(string, int, int, time.Duration, *atomic.Int32) (EncoderInterface, error) {
		return encoder, nil
	}
	sleepVoiceReady = func() {}

	session := newPlaybackSession()
	p.activePlayback = session
	p.lastPlayback = session
	p.Playing = true
	p.playbackStartedAt = time.Now()

	track := &Track{
		Title:     "buffered-stop",
		LocalPath: "/tmp/buffered-stop.opus",
	}

	p.playTrack(session, track, 0, &p.volumeAtomic)

	if got := len(vc.frames); got != 6 {
		t.Fatalf("sent frames = %d, want 6 (1 data + 5 trailing silence)", got)
	}
	if !isSilenceFrame(vc.frames[5]) {
		t.Fatalf("frame 5 should be a trailing silence frame, got %v", vc.frames[5])
	}
	if !encoder.cleaned {
		t.Fatal("encoder Cleanup() was not called")
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
	newCustomEncoder = func(string, int, int, time.Duration, *atomic.Int32) (EncoderInterface, error) {
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

	session := newPlaybackSession()
	p.activePlayback = session
	p.lastPlayback = session
	p.Playing = true
	p.playbackStartedAt = time.Now()

	track := &Track{
		Title:     "paced",
		LocalPath: "/tmp/paced.opus",
	}

	p.playTrack(session, track, 0, &p.volumeAtomic)

	if got := len(vc.frames); got != 8 {
		t.Fatalf("sent frames = %d, want 8 (3 data + 5 trailing silence)", got)
	}
	if !isSilenceFrame(vc.frames[7]) {
		t.Fatalf("frame 7 should be a trailing silence frame, got %v", vc.frames[7])
	}
	if !encoder.cleaned {
		t.Fatal("encoder Cleanup() was not called")
	}

	sleepMu.Lock()
	gotSleeps := append([]time.Duration(nil), sleepDurations...)
	sleepMu.Unlock()

	wantSleeps := []time.Duration{
		opusFrameInterval, // first frame is paced from the actual send boundary
		opusFrameInterval - encoder.frameDelay,
		opusFrameInterval - encoder.frameDelay,
		opusFrameInterval, // trailing silence frame 1
		opusFrameInterval, // trailing silence frame 2
		opusFrameInterval, // trailing silence frame 3
		opusFrameInterval, // trailing silence frame 4
		opusFrameInterval, // trailing silence frame 5
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
	newCustomEncoder = func(string, int, int, time.Duration, *atomic.Int32) (EncoderInterface, error) {
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

	newCustomEncoder = func(string, int, int, time.Duration, *atomic.Int32) (EncoderInterface, error) {
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

	result := p.LastPlaybackResult()
	if result.Reason != PlaybackEndSourceFailure {
		t.Fatalf("LastPlaybackResult().Reason = %q, want %q", result.Reason, PlaybackEndSourceFailure)
	}
	if result.Err == nil {
		t.Fatalf("LastPlaybackResult().Err = %v, want encoder error", result.Err)
	}
	if result.Started {
		t.Fatal("source failure before the first frame should not report Started")
	}
}

func TestSkipReportsTypedResult(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	t.Cleanup(func() { newCustomEncoder = originalNewCustomEncoder })

	encoder := newBlockingEncoder()
	newCustomEncoder = func(string, int, int, time.Duration, *atomic.Int32) (EncoderInterface, error) {
		return encoder, nil
	}

	p := NewManager().GetPlayer("guild-skip-result")
	p.SetVoiceConnection(&stubVoiceConnection{})
	current := &Track{Title: "current", LocalPath: "/tmp/current.opus"}
	next := &Track{Title: "next", LocalPath: "/tmp/next.opus"}
	p.Queue.Add(current)
	p.Queue.Add(next)
	if err := p.Play(); err != nil {
		t.Fatalf("Play() error = %v", err)
	}
	select {
	case <-encoder.ready:
	case <-time.After(time.Second):
		t.Fatal("encoder did not start")
	}

	if got := p.Skip(); got != next {
		t.Fatalf("Skip() next = %p, want %p", got, next)
	}
	if !p.ConsumeSkipRequest() {
		t.Fatal("ConsumeSkipRequest() = false, want true")
	}
	if p.ConsumeSkipRequest() {
		t.Fatal("ConsumeSkipRequest() did not clear the request")
	}

	result := p.WaitForCompletionResult()
	if result.Reason != PlaybackEndSkipped {
		t.Fatalf("WaitForCompletionResult().Reason = %q, want %q", result.Reason, PlaybackEndSkipped)
	}
	if result.Err != nil {
		t.Fatalf("skip result error = %v, want nil", result.Err)
	}
}

func TestTransportSendFailureReportsTypedResult(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newCustomEncoder = originalNewCustomEncoder
		sleepVoiceReady = originalSleepVoiceReady
	})

	newCustomEncoder = func(string, int, int, time.Duration, *atomic.Int32) (EncoderInterface, error) {
		return &stubEncoder{frames: [][]byte{[]byte("frame")}}, nil
	}
	sleepVoiceReady = func() {}

	p := NewManager().GetPlayer("guild-transport-result")
	p.SetVoiceConnection(&stubVoiceConnection{sendErr: errors.New("voice send failed")})
	p.Queue.Add(&Track{Title: "transport", LocalPath: "/tmp/transport.opus"})
	if err := p.Play(); err != nil {
		t.Fatalf("Play() error = %v", err)
	}

	result := p.WaitForCompletionResult()
	if result.Reason != PlaybackEndTransportFailure {
		t.Fatalf("WaitForCompletionResult().Reason = %q, want %q", result.Reason, PlaybackEndTransportFailure)
	}
	if result.Err == nil || !result.Failed() {
		t.Fatalf("transport result = %#v, want a failure with error", result)
	}
	if p.IsVoiceConnected() {
		t.Fatal("voice connection remained marked connected after send failure")
	}
}

func TestVoiceDuckingRetainsDesiredVolume(t *testing.T) {
	p := NewManager().GetPlayer("guild-ducking")
	p.Playing = true
	p.ReduceOnVoice = true
	p.ReduceOnVoiceTarget = 35
	if err := p.SetVolume(80); err != nil {
		t.Fatalf("SetVolume() error = %v", err)
	}

	p.SpeakerStarted("speaker")
	if got := p.volumeAtomic.Load(); got != 35 {
		t.Fatalf("ducked volume = %d, want 35", got)
	}
	if p.Volume != 80 {
		t.Fatalf("desired volume = %d, want 80", p.Volume)
	}

	if err := p.SetVolume(60); err != nil {
		t.Fatalf("SetVolume() while ducked error = %v", err)
	}
	if got := p.volumeAtomic.Load(); got != 35 {
		t.Fatalf("ducked volume after SetVolume = %d, want 35", got)
	}
	p.SpeakerStopped("speaker")
	if got := p.volumeAtomic.Load(); got != 60 {
		t.Fatalf("restored volume = %d, want 60", got)
	}

	p.SpeakerStarted("speaker")
	p.SetVoiceReductionEnabled(false)
	if got := p.volumeAtomic.Load(); got != 60 {
		t.Fatalf("volume after disabling ducking = %d, want 60", got)
	}
}

func TestManagerPlaybackDefaultsApplyOnlyToNewPlayers(t *testing.T) {
	manager := NewManagerWithDefaults(PlaybackDefaults{
		Volume:              42,
		ReduceOnVoice:       true,
		ReduceOnVoiceTarget: 17,
	})
	first := manager.GetPlayer("first")
	if first.Volume != 42 || !first.ReduceOnVoice || first.ReduceOnVoiceTarget != 17 {
		t.Fatalf("first player defaults = (%d, %v, %d), want (42, true, 17)", first.Volume, first.ReduceOnVoice, first.ReduceOnVoiceTarget)
	}

	manager.SetPlaybackDefaults(PlaybackDefaults{Volume: 55, ReduceOnVoiceTarget: 25})
	if first.Volume != 42 {
		t.Fatalf("existing player volume = %d, want 42", first.Volume)
	}
	second := manager.GetPlayer("second")
	if second.Volume != 55 || second.ReduceOnVoice || second.ReduceOnVoiceTarget != 25 {
		t.Fatalf("second player defaults = (%d, %v, %d), want (55, false, 25)", second.Volume, second.ReduceOnVoice, second.ReduceOnVoiceTarget)
	}
}

func TestDisconnectPreservesCurrentQueueEntryAndResetsPosition(t *testing.T) {
	p := NewManager().GetPlayer("guild-disconnect-preserve")
	p.SetVoiceConnection(&stubVoiceConnection{})
	track := &Track{Title: "resume me", URL: "https://www.youtube.com/watch?v=abc123XYZ89"}
	p.Queue.Add(track)
	if selected := p.Queue.Next(); selected != track {
		t.Fatalf("Queue.Next() = %p, want current track %p", selected, track)
	}
	p.CurrentPosition = 42 * time.Second

	if err := p.Disconnect(); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if p.IsVoiceConnected() {
		t.Fatal("player remains voice-connected after Disconnect()")
	}
	if current := p.Queue.Current(); current != track {
		t.Fatalf("Queue.Current() = %p, want preserved track %p", current, track)
	}
	if got := p.Queue.Length(); got != 1 {
		t.Fatalf("Queue.Length() = %d, want 1", got)
	}
	if got := p.GetCurrentPosition(); got != 0 {
		t.Fatalf("GetCurrentPosition() = %v, want restart at 0", got)
	}
}

func TestPlayTrackFallsBackWhenPrefetchedStreamEndsBeforeFirstFrame(t *testing.T) {
	originalNewStreamingEncoder := newStreamingEncoder
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newStreamingEncoder = originalNewStreamingEncoder
		sleepVoiceReady = originalSleepVoiceReady
	})

	var callCount int
	firstEncoder := &stubEncoder{}
	secondEncoder := &stubEncoder{
		frames: [][]byte{
			[]byte("frame-1"),
		},
	}

	newStreamingEncoder = func(url, streamURL string, streamHeaders map[string]string, sampleRate, channels int, startOffset time.Duration, vol *atomic.Int32) (EncoderInterface, error) {
		callCount++
		switch callCount {
		case 1:
			if streamURL == "" {
				t.Fatal("first streaming attempt should use the prefetched stream URL")
			}
			if len(streamHeaders) == 0 {
				t.Fatal("first streaming attempt should include prefetched stream headers")
			}
			return firstEncoder, nil
		case 2:
			if streamURL != "" {
				t.Fatal("fallback streaming attempt should resolve live without a prefetched URL")
			}
			return secondEncoder, nil
		default:
			t.Fatalf("unexpected streaming encoder call %d", callCount)
			return nil, errors.New("unexpected streaming encoder call")
		}
	}
	sleepVoiceReady = func() {}

	p := NewManager().GetPlayer("guild-prefetch-fallback")
	vc := &stubVoiceConnection{}
	p.SetVoiceConnection(vc)

	session := newPlaybackSession()
	p.activePlayback = session
	p.lastPlayback = session
	p.Playing = true
	p.playbackStartedAt = time.Now()

	track := &Track{
		Title:    "fallback-track",
		URL:      "https://www.youtube.com/watch?v=test",
		Duration: time.Minute,
	}
	track.SetPrefetchedStream(
		"https://media.example/audio.webm",
		map[string]string{"User-Agent": "test-agent"},
		time.Now().Add(10*time.Minute),
	)

	p.playTrack(session, track, 0, &p.volumeAtomic)

	if callCount != 2 {
		t.Fatalf("streaming encoder calls = %d, want 2", callCount)
	}
	if got := len(vc.frames); got != 6 {
		t.Fatalf("sent frames = %d, want 6 (1 data + 5 trailing silence)", got)
	}
	if !isSilenceFrame(vc.frames[5]) {
		t.Fatalf("frame 5 should be a trailing silence frame, got %v", vc.frames[5])
	}
	if !firstEncoder.cleaned {
		t.Fatal("prefetched stream encoder was not cleaned up before fallback")
	}
	if track.StreamURL != "" {
		t.Fatal("prefetched stream metadata should be cleared after fallback")
	}
}

func TestLatePlaybackCannotClearNewerSessionState(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newCustomEncoder = originalNewCustomEncoder
		sleepVoiceReady = originalSleepVoiceReady
	})

	encoder1 := newBlockingEncoder()
	newCustomEncoder = func(string, int, int, time.Duration, *atomic.Int32) (EncoderInterface, error) {
		return encoder1, nil
	}
	sleepVoiceReady = func() {}

	p := NewManager().GetPlayer("guild-late-finish")
	p.SetVoiceConnection(&stubVoiceConnection{})

	session1 := newPlaybackSession()
	p.activePlayback = session1
	p.lastPlayback = session1
	p.Playing = true
	p.playbackStartedAt = time.Now()

	done1 := make(chan struct{})
	go func() {
		p.playTrack(session1, &Track{Title: "first", LocalPath: "/tmp/first.opus"}, 0, &p.volumeAtomic)
		close(done1)
	}()

	select {
	case <-encoder1.ready:
	case <-time.After(time.Second):
		t.Fatal("first playback did not reach encoder read")
	}

	session2 := newPlaybackSession()
	encoder2 := &stubEncoder{}
	if !session2.bindEncoder(encoder2) {
		t.Fatal("failed to bind encoder to second session")
	}

	p.mu.Lock()
	p.activePlayback = session2
	p.lastPlayback = session2
	p.Playing = true
	p.playbackStartedAt = time.Now()
	p.mu.Unlock()

	encoder1.release()

	select {
	case <-done1:
	case <-time.After(time.Second):
		t.Fatal("first playback did not finish")
	}

	p.mu.RLock()
	active := p.activePlayback
	playing := p.Playing
	p.mu.RUnlock()

	if active != session2 {
		t.Fatal("late playback cleared the newer active session")
	}
	if !playing {
		t.Fatal("late playback cleared newer playback state")
	}
	if encoder2.cleaned {
		t.Fatal("late playback cleaned the newer session encoder")
	}
}

func TestPlayTrackWaitsForVoiceReadyAfterEachTrack(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newCustomEncoder = originalNewCustomEncoder
		sleepVoiceReady = originalSleepVoiceReady
	})

	var waitCalls atomic.Int32
	newCustomEncoder = func(string, int, int, time.Duration, *atomic.Int32) (EncoderInterface, error) {
		return &stubEncoder{
			frames: [][]byte{
				[]byte("frame-1"),
			},
		}, nil
	}
	sleepVoiceReady = func() {
		waitCalls.Add(1)
	}

	p := NewManager().GetPlayer("guild-voice-wait-after-each")
	p.SetVoiceConnection(&stubVoiceConnection{})

	runPlayback := func(title string) {
		session := newPlaybackSession()
		p.activePlayback = session
		p.lastPlayback = session
		p.Playing = true
		p.playbackStartedAt = time.Now()

		p.playTrack(session, &Track{
			Title:     title,
			LocalPath: "/tmp/" + title + ".opus",
		}, 0, &p.volumeAtomic)
	}

	runPlayback("first")
	if got := waitCalls.Load(); got != 1 {
		t.Fatalf("voice ready waits after first playback = %d, want 1", got)
	}

	runPlayback("second")
	if got := waitCalls.Load(); got != 1 {
		t.Fatalf("voice ready waits after second playback on same connection = %d, want 1", got)
	}

	p.ClearVoiceConnection()
	p.SetVoiceConnection(&stubVoiceConnection{})

	runPlayback("third")
	if got := waitCalls.Load(); got != 2 {
		t.Fatalf("voice ready waits after reconnect playback = %d, want 2", got)
	}
}

func TestStartLoopIfIdleIsAtomic(t *testing.T) {
	p := NewManager().GetPlayer("guild-loop")

	var (
		successes atomic.Int32
		wg        sync.WaitGroup
		start     = make(chan struct{})
	)

	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if p.StartLoopIfIdle() {
				successes.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful loop starts = %d, want 1", got)
	}
	if !p.IsLoopRunning() {
		t.Fatal("loop should be marked as running")
	}
}

func TestWaitForCompletionUnblocksWhenDoneChannelCloses(t *testing.T) {
	originalNewCustomEncoder := newCustomEncoder
	originalSleepVoiceReady := sleepVoiceReady
	t.Cleanup(func() {
		newCustomEncoder = originalNewCustomEncoder
		sleepVoiceReady = originalSleepVoiceReady
	})

	newCustomEncoder = func(string, int, int, time.Duration, *atomic.Int32) (EncoderInterface, error) {
		return &stubEncoder{
			frames: [][]byte{
				[]byte("frame-1"),
			},
		}, nil
	}
	sleepVoiceReady = func() {}

	p := NewManager().GetPlayer("guild-wait-unblock")
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
	onFrame    func(nextIndex int)
	cleanupMu  sync.Mutex
}

func (e *stubEncoder) OpusFrame() ([]byte, error) {
	if e.index >= len(e.frames) {
		return nil, io.EOF
	}
	if e.onFrame != nil {
		e.onFrame(e.index + 1)
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

type blockingEncoder struct {
	ready       chan struct{}
	releaseCh   chan struct{}
	readyOnce   sync.Once
	releaseOnce sync.Once
	cleanupMu   sync.Mutex
	cleaned     bool
}

func newBlockingEncoder() *blockingEncoder {
	return &blockingEncoder{
		ready:     make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
}

func (e *blockingEncoder) OpusFrame() ([]byte, error) {
	e.readyOnce.Do(func() {
		close(e.ready)
	})
	<-e.releaseCh
	return nil, io.EOF
}

func (e *blockingEncoder) Cleanup() error {
	e.cleanupMu.Lock()
	e.cleaned = true
	e.cleanupMu.Unlock()
	e.release()
	return nil
}

func (e *blockingEncoder) release() {
	e.releaseOnce.Do(func() {
		close(e.releaseCh)
	})
}

type stubVoiceConnection struct {
	frames      [][]byte
	speaking    []bool
	disconnects int
	sendHook    func(frame []byte, sendCount int)
	sendErr     error
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
	return c.sendErr
}

func (c *stubVoiceConnection) Disconnect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnects++
	return nil
}

func isSilenceFrame(frame []byte) bool {
	return len(frame) == len(silenceFrame) && frame[0] == silenceFrame[0] && frame[1] == silenceFrame[1] && frame[2] == silenceFrame[2]
}
