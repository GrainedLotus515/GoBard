package discordvoice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"

	"github.com/charmbracelet/log"
)

// sdkLog drops arbitrary SDK attributes/message payloads, which can contain
// voice tokens or signed endpoints. Application owners log returned failures;
// unsolicited SDK warnings retain a safe fingerprint for diagnosis.
type sdkLog struct{ logger *log.Logger }

func (h sdkLog) Enabled(context.Context, slog.Level) bool { return true }
func (h sdkLog) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.LevelWarn {
		return nil
	}
	hash := sha256.Sum256([]byte(r.Message))
	h.logger.Warn("Discord voice SDK diagnostic", "event", "discord.sdk", "level", r.Level.String(), "fingerprint", hex.EncodeToString(hash[:8]))
	return nil
}
func (h sdkLog) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h sdkLog) WithGroup(string) slog.Handler      { return h }
