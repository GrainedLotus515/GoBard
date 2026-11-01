package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/command"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/media"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/session"
)

func TestReviewWrappedMetadataFailureKeepsActionableGuidance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category failure.Category
		want     string
	}{
		{"authentication", failure.Authentication, "provider refused access"},
		{"rate limit", failure.Throttled, "limiting requests"},
		{"unavailable", failure.Unavailable, "unavailable from its provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := failure.Wrap("metadata", tc.category, errors.New("private provider diagnostic"))
			// command.Player.Play adds this context to resolver failures.
			err := failure.Wrap("metadata", failure.Source, cause)
			card := commandFailure(err)
			if !strings.Contains(card.Description, tc.want) {
				t.Fatalf("lost classified metadata guidance: %q, want %q", card.Description, tc.want)
			}
		})
	}
}

type failedResolver struct{ err error }

func (r failedResolver) Resolve(context.Context, string) ([]media.Track, error) { return nil, r.err }

type allowedAccess struct{}

func (allowedAccess) Access(context.Context, string, string) (session.Access, error) {
	return session.Access{Channel: "voice"}, nil
}

func TestPlayMetadataFailureResponseAndRecord(t *testing.T) {
	for _, category := range []failure.Category{failure.Authentication, failure.Throttled, failure.Unavailable} {
		t.Run(string(category), func(t *testing.T) {
			var records bytes.Buffer
			logger := log.NewWithOptions(&records, log.Options{Formatter: log.JSONFormatter})
			engine, err := playback.New(interactionSource{}, log.New(io.Discard))
			if err != nil {
				t.Fatal(err)
			}
			target, err := session.New(context.Background(), "metadata", engine, mappingOutput{}, 4)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if _, err := target.Apply(context.Background(), session.Operation{Action: session.Connect, Channel: "voice", Output: mappingOutput{}}); err != nil {
				t.Fatal(err)
			}
			secret := errors.New("https://signed.invalid/?token=SECRET Cookie: SECRET @everyone")
			cause := failure.Wrap("extract", category, secret)
			player, err := command.NewPlayer(context.Background(), failedResolver{fmt.Errorf("context: %w", failure.Wrap("resolve", failure.Source, cause))}, allowedAccess{}, logger, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer player.Close()
			a, err := player.Play(context.Background(), target, "guild", "user", "hostile title SECRET")
			if !errors.Is(err, secret) {
				t.Fatalf("cause lost: %v", err)
			}
			card := commandFailure(err)
			if card.Description != failureMessage(cause) {
				t.Fatalf("wrong response: %s", card.Description)
			}
			var record map[string]any
			dec := json.NewDecoder(&records)
			if err := dec.Decode(&record); err != nil {
				t.Fatal(err)
			}
			if record["category"] != string(category) || record["command_id"] != a.CommandID || record["event"] != "command.terminal" {
				t.Fatalf("record: %+v", record)
			}
			if err := dec.Decode(&record); err != io.EOF {
				t.Fatal("duplicate terminal log")
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded)+card.Description, "SECRET") {
				t.Fatal("secret leaked")
			}
		})
	}
}
