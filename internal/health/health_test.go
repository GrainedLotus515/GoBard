package health

import (
	"context"
	"errors"
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

	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/live", "/ready"} {
		response, err := client.Get("http://" + server.Addr() + path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, response.StatusCode, http.StatusOK)
		}
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Errorf("close %s response: %v", path, closeErr)
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

	response, err := (&http.Client{Timeout: time.Second}).Get("http://" + server.Addr() + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Errorf("close readiness response: %v", closeErr)
		}
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
}
