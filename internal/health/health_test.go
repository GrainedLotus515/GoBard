package health

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

type testChecker struct {
	liveErr  error
	readyErr error
}

func (c testChecker) Live() error                 { return c.liveErr }
func (c testChecker) Ready(context.Context) error { return c.readyErr }

func newTestClient(t *testing.T) *http.Client {
	t.Helper()
	client := &http.Client{
		Timeout:   time.Second,
		Transport: &http.Transport{},
	}
	// Register after server cleanup so the test-owned transport closes first.
	// The default transport can leave an unused connection open while Shutdown
	// waits for its first request, exceeding the test's shutdown deadline.
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func endpointStatus(t *testing.T, client *http.Client, url string) int {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Errorf("close response: %v", closeErr)
		}
	}()
	// Finish the response before issuing another request or shutting down.
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	return response.StatusCode
}

func TestEndpoints(t *testing.T) {
	server, err := Start("127.0.0.1:0", testChecker{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if shutdownErr := server.Shutdown(ctx); shutdownErr != nil {
			t.Errorf("health server shutdown: %v", shutdownErr)
		}
	})

	client := newTestClient(t)
	for _, path := range []string{"/live", "/ready"} {
		if status := endpointStatus(t, client, "http://"+server.Addr()+path); status != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, status, http.StatusOK)
		}
	}
}

func TestUnavailableEndpoint(t *testing.T) {
	checker := testChecker{readyErr: errors.New("discord unavailable")}
	server, err := Start("127.0.0.1:0", checker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if shutdownErr := server.Shutdown(ctx); shutdownErr != nil {
			t.Errorf("health server shutdown: %v", shutdownErr)
		}
	})

	client := newTestClient(t)
	if status := endpointStatus(t, client, "http://"+server.Addr()+"/ready"); status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}
