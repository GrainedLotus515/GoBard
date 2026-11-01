package playback

import "time"

// Timing is owned by one attempt. It retains no samples or packet data. The
// first accepted frame measures startup separately. Subsequent accepted frames
// measure Next (source plus codec) and Send (pacing plus transport), excluding
// explicit pause gates. Failed reads/writes and EOF cleanup are not samples.
// These durations diagnose local stalls, not remote reception or audibility.
type Timing struct {
	FirstFrame time.Duration
	MaxRead    time.Duration
	MaxSend    time.Duration
	SlowReads  uint64
	SlowSends  uint64
}

func (t *Timing) record(read, send time.Duration) {
	t.MaxRead = max(t.MaxRead, read)
	t.MaxSend = max(t.MaxSend, send)
	if read > 3*FrameDuration {
		t.SlowReads++
	}
	if send > 3*FrameDuration {
		t.SlowSends++
	}
}
