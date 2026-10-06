package player

const opusFrameBufferSize = 4000

// EncodedFrame owns one reusable Opus buffer. Call Release exactly once after
// the voice transport has consumed Data. Frames created by test encoders may
// omit the pool; Release is then a no-op.
type EncodedFrame struct {
	Data []byte
	pool *encodedFramePool
}

// Release returns the frame's backing storage to its encoder without blocking
// a concurrent cleanup path.
func (f EncodedFrame) Release() {
	if f.pool == nil || cap(f.Data) < opusFrameBufferSize {
		return
	}
	select {
	case f.pool.free <- f.Data[:opusFrameBufferSize]:
	default:
	}
}

type encodedFramePool struct {
	free chan []byte
}

func newEncodedFramePool() *encodedFramePool {
	pool := &encodedFramePool{free: make(chan []byte, encodedFrameBufferCapacity+1)}
	for range cap(pool.free) {
		pool.free <- make([]byte, opusFrameBufferSize)
	}
	return pool
}

func (p *encodedFramePool) borrow(stop <-chan struct{}) ([]byte, bool) {
	select {
	case buffer := <-p.free:
		return buffer, true
	case <-stop:
		return nil, false
	}
}

func applyPCMVolume(samples []int16, rawVolume int32) {
	if rawVolume >= 100 {
		return
	}
	if rawVolume <= 0 {
		clear(samples)
		return
	}

	// Q15 scaling avoids a floating-point conversion for every sample. Keep
	// negative values truncating toward zero to match Go's numeric conversion.
	gain := rawVolume * 32768 / 100
	for i, sample := range samples {
		product := int32(sample) * gain
		if product < 0 {
			// #nosec G115 -- input and Q15 gain guarantee the result remains int16.
			samples[i] = int16(-((-product) >> 15))
		} else {
			// #nosec G115 -- input and Q15 gain guarantee the result remains int16.
			samples[i] = int16(product >> 15)
		}
	}
}
