package player

import "testing"

func TestEncodedFramePoolReusesSixBuffersWithoutPerFrameAllocation(t *testing.T) {
	pool := newEncodedFramePool()
	stop := make(chan struct{})
	allocations := testing.AllocsPerRun(1000, func() {
		buffer, ok := pool.borrow(stop)
		if !ok {
			t.Fatal("frame pool stopped unexpectedly")
		}
		EncodedFrame{Data: buffer[:100], pool: pool}.Release()
	})
	if allocations != 0 {
		t.Fatalf("frame borrow/release allocations = %f, want 0", allocations)
	}
}

func TestApplyPCMVolume(t *testing.T) {
	samples := []int16{-32768, -1000, 0, 1000, 32767}
	applyPCMVolume(samples, 50)
	want := []int16{-16384, -500, 0, 500, 16383}
	for i := range samples {
		if samples[i] != want[i] {
			t.Fatalf("samples[%d] = %d, want %d", i, samples[i], want[i])
		}
	}
}

func BenchmarkEncodedFramePool(b *testing.B) {
	pool := newEncodedFramePool()
	stop := make(chan struct{})
	b.ReportAllocs()
	for b.Loop() {
		buffer, _ := pool.borrow(stop)
		EncodedFrame{Data: buffer[:320], pool: pool}.Release()
	}
}

func BenchmarkApplyPCMVolumeStereo20ms(b *testing.B) {
	samples := make([]int16, 960*2)
	for i := range samples {
		samples[i] = int16(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		applyPCMVolume(samples, 70)
	}
}
