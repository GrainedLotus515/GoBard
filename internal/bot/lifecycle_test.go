package bot

import (
	"testing"
	"time"
)

func TestStopCancelsAndJoinsAsyncWork(t *testing.T) {
	b := &Bot{}
	if !b.beginAsyncWork() {
		t.Fatal("beginAsyncWork() = false, want true")
	}
	ctx := b.asyncContext()
	done := make(chan struct{})
	go func() {
		defer b.asyncWorkWG.Done()
		<-ctx.Done()
		close(done)
	}()

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop() returned before asynchronous work exited")
	}
	if b.beginAsyncWork() {
		t.Fatal("beginAsyncWork() = true after Stop(), want false")
	}
}
