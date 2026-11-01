// Package netguard bounds context-free WebSocket I/O in the temporary SDKs.
package netguard

import (
	"context"
	"net"
	"sync"
	"time"
)

// Socket owns conn and a lifecycle cancellation callback. Close interrupts I/O
// and joins that callback. It cannot join workers private to a consuming SDK.
type Socket struct {
	net.Conn
	stop func() bool
	done chan struct{}
	once sync.Once
	err  error
}

// Wrap takes ownership of conn. Both ctx and conn must be non-nil; conn must
// allow concurrent Close and I/O, as required by net.Conn.
func Wrap(ctx context.Context, conn net.Conn) *Socket {
	s := &Socket{Conn: conn, done: make(chan struct{})}
	s.stop = context.AfterFunc(ctx, func() { _ = conn.Close(); close(s.done) })
	return s
}

func (s *Socket) Write(p []byte) (int, error) {
	if err := s.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	return s.Conn.Write(p)
}

func (s *Socket) Close() error {
	s.once.Do(func() {
		s.err = s.Conn.Close()
		if !s.stop() {
			<-s.done
		}
	})
	return s.err
}
