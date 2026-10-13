package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GrainedLotus515/gobard/internal/failure"
)

// discordHTTP implements only the REST operations consumed by GoBard. The
// temporary SDK's rate-limit sleeps cannot be canceled. No response bodies,
// credentials or webhook URLs escape this boundary; 429 is explicitly returned
// to the operation owner, never retried by this layer. Redirects are forbidden.
type discordHTTP struct {
	client *http.Client
	token  string
}

func newDiscordHTTP(token string) *discordHTTP {
	return &discordHTTP{token: token, client: &http.Client{
		Timeout:       8 * time.Second,
		Transport:     &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 40, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("discord redirects prohibited") },
	}}
}

func (d *discordHTTP) send(ctx context.Context, method, path string, body any, authenticated bool) error {
	_, err := d.request(ctx, method, path, body, authenticated, false)
	return err
}

type discordStatus struct {
	code       int
	retryAfter time.Duration
}

func (s *discordStatus) Error() string { return fmt.Sprintf("HTTP status %d", s.code) }

// request optionally extracts only the message ID from a bounded response.
// All other fields, including untrusted content, are discarded.
func (d *discordHTTP) request(ctx context.Context, method, path string, body any, authenticated, message bool) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", failure.Wrap("discord_request", failure.Transport, err)
	}
	if len(data) > 256*1024 {
		return "", errors.New("discord request exceeds byte budget")
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://discord.com/api/v10/"+path, bytes.NewReader(data))
	if err != nil {
		return "", failure.Wrap("discord_request", failure.Transport, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/GrainedLotus515/gobard, 2)")
	if authenticated {
		req.Header.Set("Authorization", "Bot "+d.token)
	}
	response, err := d.client.Do(req)
	if err != nil {
		return "", failure.Wrap("discord_request", failure.Transport, err)
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil {
		return "", failure.Wrap("discord_response", failure.Transport, err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if !message {
			return "", nil
		}
		var identity struct {
			ID string `json:"id"`
		}
		if len(data) > 64*1024 || json.Unmarshal(data, &identity) != nil || !validPathPart(identity.ID, 32) {
			return "", failure.Wrap("discord_message", failure.Transport, errors.New("invalid message identity response"))
		}
		return identity.ID, nil
	}
	category := failure.Transport
	switch response.StatusCode {
	case http.StatusTooManyRequests:
		category = failure.Throttled
	case http.StatusUnauthorized, http.StatusForbidden:
		category = failure.Authentication
	}
	retry := 30 * time.Second
	if seconds, parseErr := strconv.ParseFloat(response.Header.Get("Retry-After"), 64); parseErr == nil && seconds > 0 {
		// An hour or more suspends automatic updates instead of shortening
		// the server's delay or overflowing a duration conversion.
		retry = max(retry, time.Duration(min(seconds, 3600)*float64(time.Second)))
	}
	return "", failure.Wrap("discord_response", category, &discordStatus{code: response.StatusCode, retryAfter: retry})
}

// Path components are checked independently of the payload grammar. Tokens
// remain adapter-owned and are never included in diagnostic errors.
func validPathPart(value string, limit int) bool {
	if value == "" || value == "." || value == ".." || len(value) > limit {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			return false
		default:
			return true
		}
	}) == -1
}
