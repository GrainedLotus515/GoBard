// Package health exposes a small loopback-only liveness and readiness server.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const probeTimeout = 2 * time.Second

// Checker reports the state needed by the process health endpoints.
type Checker interface {
	Live() error
	Ready(context.Context) error
}

// Dimensions are observations, not claims that idle providers or remote audio
// are currently working. ActiveDelivery is sampled transport acceptance.
type Dimensions struct {
	DiscordConnected bool   `json:"discord_connected"`
	CommandsReady    bool   `json:"commands_ready"`
	Source           string `json:"source"`
	Pipeline         string `json:"pipeline"`
	ActiveDelivery   int32  `json:"active_delivery"`
	BusySessions     int    `json:"busy_sessions"` // -1 means the observation failed.
}

type detailedChecker interface{ Dimensions() Dimensions }

// Server owns the HTTP server used only for local Docker health checks.
type Server struct {
	server *http.Server
	ln     net.Listener

	done chan struct{}

	mu       sync.Mutex
	serveErr error
}

// Start begins serving /live and /ready on addr. Call Shutdown to stop it.
func Start(addr string, checker Checker) (*Server, error) {
	if addr == "" {
		return nil, fmt.Errorf("health listen address is required")
	}
	if checker == nil {
		return nil, fmt.Errorf("health checker is required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, checker.Live(), checker)
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
		defer cancel()
		respond(w, checker.Ready(ctx), checker)
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for health checks: %w", err)
	}

	s := &Server{
		ln:   ln,
		done: make(chan struct{}),
		server: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: probeTimeout,
		},
	}

	go func() {
		err := s.server.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			s.serveErr = err
			s.mu.Unlock()
		}
		close(s.done)
	}()

	return s, nil
}

// Shutdown stops the health server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}

	shutdownErr := s.server.Shutdown(ctx)
	if shutdownErr != nil {
		// Graceful timeout does not transfer ownership back to the caller:
		// force-close sockets and join Serve before returning the failure.
		closeErr := s.server.Close()
		<-s.done
		return errors.Join(shutdownErr, closeErr)
	}

	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.serveErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Addr returns the address the health server is listening on.
func (s *Server) Addr() string {
	if s == nil || s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Probe checks an endpoint and returns an error unless it responds 2xx.
func Probe(ctx context.Context, url string) error {
	if url == "" {
		return fmt.Errorf("health URL is required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}
	response, err := (&http.Client{Timeout: probeTimeout}).Do(request)
	if err != nil {
		return fmt.Errorf("health request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}

func respond(w http.ResponseWriter, err error, checker Checker) {
	w.Header().Set("Content-Type", "application/json")
	body := struct {
		Status     string      `json:"status"`
		Error      string      `json:"error,omitempty"`
		Dimensions *Dimensions `json:"dimensions,omitempty"`
	}{Status: "ok"}
	if detailed, ok := checker.(detailedChecker); ok {
		dimensions := detailed.Dimensions()
		body.Dimensions = &dimensions
	}
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		body.Status, body.Error = "unavailable", err.Error()
	}
	if encodeErr := json.NewEncoder(w).Encode(body); encodeErr != nil {
		return
	}
}

// ErrNotReady is useful for checkers that need to distinguish transient startup.
var ErrNotReady = errors.New("not ready")
