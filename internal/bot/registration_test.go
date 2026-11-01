package bot

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type registrationTimerEvent struct {
	delay   time.Duration
	fire    chan time.Time
	stopped chan struct{}
}

func registrationFixture(t *testing.T) (*Runtime, chan registrationTimerEvent) {
	t.Helper()
	r := runtimeFixture(t)
	r.cfg.RegisterGlobally = true
	r.connected.Store(true)
	r.discord.State.Lock()
	r.discord.State.User = &discordgo.User{ID: "application"}
	r.discord.State.Unlock()
	timers := make(chan registrationTimerEvent, 8)
	r.registrationTimer = func(delay time.Duration) (<-chan time.Time, func()) {
		event := registrationTimerEvent{delay: delay, fire: make(chan time.Time, 1), stopped: make(chan struct{})}
		timers <- event
		return event.fire, func() { close(event.stopped) }
	}
	r.work.Add(1)
	go func() { defer r.work.Done(); r.registrationLoop() }()
	return r, timers
}

func waitRegistrationPass(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("registration pass did not complete HTTP request")
	}
}

func waitRegistrationReady(t *testing.T, r *Runtime) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !r.registered.Load() {
		if time.Now().After(deadline) {
			t.Fatal("registration did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRegistrationRateLimitSchedulesRecovery(t *testing.T) {
	r, timers := registrationFixture(t)
	var calls atomic.Int32
	called := make(chan struct{}, 4)
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		called <- struct{}{}
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"400"}}, Body: io.NopCloser(strings.NewReader("private response"))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	r.signalRegistration()
	waitRegistrationPass(t, called)
	event := <-timers
	if event.delay != 400*time.Second {
		t.Fatalf("retry delay = %v, want provider delay", event.delay)
	}
	if r.registered.Load() {
		t.Fatal("rate-limited registration became ready")
	}
	for range 4 {
		r.signalRegistration()
	}
	event.fire <- time.Now()
	waitRegistrationPass(t, called)
	waitRegistrationReady(t, r)
	if got := calls.Load(); got != 2 {
		t.Fatalf("registration calls = %d, want two", got)
	}
	r.Close()
}

func TestRegistrationPersistentFailureRetriesAndCloseStopsTimer(t *testing.T) {
	r, timers := registrationFixture(t)
	called := make(chan struct{}, 8)
	r.rest.client.Transport = httpRoundTrip(func(req *http.Request) (*http.Response, error) {
		called <- struct{}{}
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	r.signalRegistration()
	waitRegistrationPass(t, called)
	first := <-timers
	if first.delay != registrationInitialBackoff {
		t.Fatalf("first backoff = %v", first.delay)
	}
	first.fire <- time.Now()
	waitRegistrationPass(t, called)
	second := <-timers
	if second.delay != 2*registrationInitialBackoff {
		t.Fatalf("second backoff = %v", second.delay)
	}
	second.fire <- time.Now()
	waitRegistrationPass(t, called)
	third := <-timers
	if third.delay != 4*registrationInitialBackoff {
		t.Fatalf("third backoff = %v", third.delay)
	}
	if r.registered.Load() {
		t.Fatal("persistent failure became ready")
	}
	r.Close()
	select {
	case <-third.stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not stop registration retry")
	}
}

func TestRegistrationBackoffIsBounded(t *testing.T) {
	for pass, expected := range map[int]time.Duration{1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second, 100: registrationMaximumBackoff} {
		if got := registrationBackoff(pass); got != expected {
			t.Fatalf("pass %d backoff = %v, want %v", pass, got, expected)
		}
	}
}

func TestResumeRuntimeSignalsRegistration(t *testing.T) {
	r := runtimeFixture(t)
	r.resumeRuntime()
	if !r.connected.Load() {
		t.Fatal("resumed Gateway did not restore connectivity")
	}
	select {
	case <-r.register:
	default:
		t.Fatal("resumed Gateway did not trigger registration reconciliation")
	}
}
