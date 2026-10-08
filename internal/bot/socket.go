package bot

import (
	"context"
	"net"
	"sync"
	"time"
)

// gatewaySocket bounds the SDK's context-free Gateway writes. Root cancellation
// closes the socket; Close also joins the cancellation callback. Never invoke
// wrapper.Close from that callback, which would attempt to join itself.
type gatewaySocket struct {
	net.Conn
	stop func() bool
	done chan struct{}
	once sync.Once
	err  error
}

func newGatewaySocket(ctx context.Context, conn net.Conn) *gatewaySocket {
	s := &gatewaySocket{Conn: conn, done: make(chan struct{})}
	s.stop = context.AfterFunc(ctx, func() { _ = conn.Close(); close(s.done) })
	return s
}

func (s *gatewaySocket) Write(p []byte) (int, error) {
	if err := s.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	return s.Conn.Write(p)
}

func (s *gatewaySocket) Close() error {
	s.once.Do(func() {
		s.err = s.Conn.Close()
		if !s.stop() {
			<-s.done
		}
	})
	return s.err
}
