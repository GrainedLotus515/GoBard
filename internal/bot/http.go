package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	data, err := json.Marshal(body)
	if err != nil {
		return failure.Wrap("discord_request", failure.Transport, err)
	}
	if len(data) > 256*1024 {
		return errors.New("discord request exceeds byte budget")
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://discord.com/api/v10/"+path, bytes.NewReader(data))
	if err != nil {
		return failure.Wrap("discord_request", failure.Transport, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.Header.Set("Authorization", "Bot "+d.token)
	}
	response, err := d.client.Do(req)
	if err != nil {
		return failure.Wrap("discord_request", failure.Transport, err)
	}
	defer response.Body.Close()
	// Discard at most 64 KiB; closing a larger body sacrifices connection reuse.
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return failure.Wrap("discord_response", failure.Transport, err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	category := failure.Transport
	switch response.StatusCode {
	case http.StatusTooManyRequests:
		category = failure.Throttled
	case http.StatusUnauthorized, http.StatusForbidden:
		category = failure.Authentication
	}
	return failure.Wrap("discord_response", category, fmt.Errorf("HTTP status %d", response.StatusCode))
}

// Path components are checked independently of the payload grammar. Tokens
// remain adapter-owned and are never included in diagnostic errors.
func validPathPart(value string, limit int) bool {
	if value == "" || len(value) > limit {
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
