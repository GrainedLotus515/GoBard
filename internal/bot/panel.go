package bot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/session"
)

var errStalePlayer = errors.New("player view superseded")

// One panel per admitted guild. gate serializes edits and guards every field;
// observers skip a busy gate, handlers wait with their owned context. No token,
// payload history or background worker is retained here.
type playerPanel struct {
	gate             chan struct{}
	channel, message string
	view             MappedInteraction
	digest           [32]byte
	next             time.Time
	failures         int
}

func (p *playerPanel) acquire(ctx context.Context) error {
	select {
	case p.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *playerPanel) release() { <-p.gate }

func (r *Runtime) panel(guild string) *playerPanel {
	if _, ok := r.sessions.Get(guild); !ok {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return nil
	}
	if p := r.panels[guild]; p != nil {
		return p
	}
	if len(r.panels) >= max(1, r.cfg.MaxGuildSessions) {
		return nil
	}
	p := &playerPanel{gate: make(chan struct{}, 1)}
	r.panels[guild] = p
	return p
}

func (r *Runtime) checkPlayer(ctx context.Context, m MappedInteraction) error {
	if !m.Component || m.Operation.Action == session.Inspect {
		return nil
	}
	p := r.panel(m.Guild)
	if p == nil {
		return nil
	}
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	if p.message != "" && m.Message != p.message {
		return errStalePlayer
	}
	return nil
}

func panelView(m MappedInteraction) MappedInteraction {
	if m.Name == "queue" {
		return MappedInteraction{Name: "queue", Page: m.Page}
	}
	return MappedInteraction{Name: "now-playing"}
}

func panelEdit(m MappedInteraction, s session.Snapshot, accepted int) *discordgo.WebhookEdit {
	embed, components := runtimeCard(m, s, accepted)
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	return &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}, Components: &components, AllowedMentions: noAllowedMentions()}
}

func (r *Runtime) renderInteraction(ctx context.Context, ref interactionRef, m MappedInteraction, s session.Snapshot, accepted int, operationErr error) error {
	p := r.panel(m.Guild)
	if m.Name == "config" || operationErr != nil {
		p = nil
	}
	if p != nil {
		if err := p.acquire(ctx); err != nil {
			return err
		}
		defer p.release()
		// Another command may have advanced state while this response waited.
		if target, ok := r.sessions.Get(m.Guild); ok {
			if fresh, err := target.Apply(ctx, session.Operation{Action: session.Inspect}); err == nil {
				s = fresh
			}
		}
	}
	edit := panelEdit(m, s, accepted)
	if operationErr != nil {
		embed := &discordgo.MessageEmbed{Title: "Command failed", Description: commandMessage(operationErr), Color: 0xDC2626}
		edit.Embeds = &[]*discordgo.MessageEmbed{embed}
	}
	message, err := r.rest.request(ctx, http.MethodPatch, "webhooks/"+ref.app+"/"+ref.token+"/messages/@original", edit, false, p != nil)
	if err != nil || p == nil {
		return err
	}
	if !validPathPart(m.Channel, 32) {
		return nil
	}
	// An already-admitted control on a superseded view may finish, but must
	// never replace the newer tracked view. Its old response remains a snapshot.
	if m.Component && p.message != "" && p.message != message && m.Operation.Action != session.Inspect {
		return nil
	}
	p.channel, p.message, p.view = m.Channel, message, panelView(m)
	p.digest = [32]byte{}
	p.next, p.failures = time.Now().Add(3*time.Second), 0
	return nil
}

func (r *Runtime) updatePanel(guild string, s session.Snapshot, now time.Time) {
	p := r.panel(guild)
	if p == nil {
		return
	}
	select {
	case p.gate <- struct{}{}:
	default:
		return
	}
	defer p.release()
	if p.message == "" || now.Before(p.next) {
		return
	}
	s.Position = s.Position.Truncate(15 * time.Second)
	edit := panelEdit(p.view, s, 0)
	data, err := json.Marshal(edit)
	if err != nil {
		return
	}
	digest := sha256.Sum256(data)
	if digest == p.digest {
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 8*time.Second)
	err = r.rest.send(ctx, http.MethodPatch, "channels/"+p.channel+"/messages/"+p.message, edit, true)
	cancel()
	p.next = now.Add(3 * time.Second)
	if err == nil {
		p.digest, p.failures = digest, 0
		return
	}
	p.failures++
	p.next = now.Add(time.Duration(p.failures) * 30 * time.Second)
	var status *discordStatus
	if errors.As(err, &status) {
		if status.code == http.StatusTooManyRequests {
			p.next = now.Add(max(status.retryAfter, 30*time.Second))
			if status.retryAfter >= time.Hour {
				p.message = ""
			}
		}
		if status.code == http.StatusNotFound || status.code == http.StatusForbidden {
			p.message = ""
		}
	}
	if p.failures >= 3 {
		p.message = ""
	}
	if r.ctx.Err() == nil {
		code := 0
		if status != nil {
			code = status.code
		}
		r.logger.Warn("Player update unavailable", "event", "discord.panel", "failures", p.failures, "automatic_updates", p.message != "", "http_status", code, "retry_after_seconds", p.next.Sub(now).Seconds(), "error", err)
	}
}
