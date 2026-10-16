package bot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/botui"
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
	dismissedAttempt string
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return nil
	}
	// Check membership while holding the same brief lock as panel removal;
	// an older lookup must not recreate a panel after guild cleanup.
	if _, ok := r.sessions.Get(guild); !ok {
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
	if notice := playbackFeedback(s); notice != nil && m.Name != "stop" && m.Name != "disconnect" {
		if s.Current < 0 && m.Name != "queue" {
			embed.Title, embed.Description = notice.Name, notice.Value
		} else {
			embed.Fields = append(embed.Fields, notice)
		}
		embed.Color = botui.ColorWarning
	}
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
		if m.Name == "stop" || m.Name == "disconnect" {
			p.dismissedAttempt = s.Last.Request.AttemptID
		}
		if m.Name == "play" {
			p.dismissedAttempt = ""
		}
		if p.dismissedAttempt != "" && s.Last.Request.AttemptID == p.dismissedAttempt {
			s.Last.Failure = nil
			s.Recovery = ""
		}
	}
	if !m.Component {
		return r.renderCommand(ctx, ref, m, s, accepted, operationErr, p)
	}
	edit := panelEdit(m, s, accepted)
	if operationErr != nil {
		embed := commandFailure(operationErr)
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

// Called with p's gate held. Slash receipts stay private; the shared card is
// bot-owned, so updates do not retain short-lived interaction credentials.
func (r *Runtime) renderCommand(ctx context.Context, ref interactionRef, m MappedInteraction, s session.Snapshot, accepted int, operationErr error, p *playerPanel) error {
	card := commandReceipt(m, s, accepted)
	switch {
	case operationErr != nil:
		card = commandFailure(operationErr)
	case p != nil:
		editErr := r.showPanel(ctx, m, s, p)
		r.panelResult(m.Guild, p, time.Now(), editErr)
		if editErr != nil {
			card.Color = botui.ColorWarning
			card.Description += "\n\nThe command was applied, but the shared card couldn't update. Use `/now-playing` to refresh it."
		}
		if p.message != "" && validPathPart(m.Guild, 32) {
			card.Description += "\n[Open player](https://discord.com/channels/" + m.Guild + "/" + p.channel + "/" + p.message + ")"
		}
	case m.Name == "queue" || m.Name == "now-playing":
		card, _ = runtimeCard(m, s, accepted)
	}
	return r.rest.send(ctx, http.MethodPatch, "webhooks/"+ref.app+"/"+ref.token+"/messages/@original", &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{card}, Components: &[]discordgo.MessageComponent{}, AllowedMentions: noAllowedMentions()}, false)
}

// showPanel edits the existing message, or creates one for an explicit view
// request. A 404 permits exactly one replacement POST; other errors return.
func (r *Runtime) showPanel(ctx context.Context, m MappedInteraction, s session.Snapshot, p *playerPanel) error {
	viewRequest := m.Name == "play" || m.Name == "queue" || m.Name == "now-playing"
	view := p.view
	if viewRequest {
		view = panelView(m)
	}
	create := viewRequest && (p.message == "" || p.channel != m.Channel)
	edit := panelEdit(view, s, 0)
	if !create && p.message != "" {
		err := r.rest.send(ctx, http.MethodPatch, "channels/"+p.channel+"/messages/"+p.message, edit, true)
		var status *discordStatus
		if !viewRequest || !errors.As(err, &status) || status.code != http.StatusNotFound {
			if err == nil {
				p.view, p.digest = view, [32]byte{}
			}
			return err
		}
		p.message, create = "", true
	}
	if !create {
		return nil
	}
	if !validPathPart(m.Channel, 32) {
		return errors.New("player channel unavailable")
	}
	message, err := r.rest.request(ctx, http.MethodPost, "channels/"+m.Channel+"/messages", edit, true, true)
	if err == nil {
		p.channel, p.message, p.view, p.digest = m.Channel, message, view, [32]byte{}
	}
	return err
}

func (r *Runtime) updatePanel(parent context.Context, guild string, s session.Snapshot, now time.Time) {
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
	if p.dismissedAttempt != "" && s.Last.Request.AttemptID == p.dismissedAttempt {
		s.Last.Failure = nil
		s.Recovery = ""
	}
	edit := panelEdit(p.view, s, 0)
	data, err := json.Marshal(edit)
	if err != nil {
		return
	}
	digest := sha256.Sum256(data)
	if digest == p.digest {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	err = r.rest.send(ctx, http.MethodPatch, "channels/"+p.channel+"/messages/"+p.message, edit, true)
	cancel()
	if err == nil {
		p.digest = digest
	}
	r.panelResult(guild, p, now, err)
}

// Shared edit policy for observer updates and explicit commands. Caller owns
// the panel gate. A failed command-side edit must not restart automatic retries.
func (r *Runtime) panelResult(guild string, p *playerPanel, now time.Time, err error) {
	p.next = now.Add(3 * time.Second)
	if err == nil {
		p.failures = 0
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
		r.logger.Warn("Player update unavailable", "event", "discord.panel", "guild_id", guild, "failures", p.failures, "automatic_updates", p.message != "", "http_status", code, "retry_after_seconds", p.next.Sub(now).Seconds(), "error", err)
	}
}
