package bot

import (
	"fmt"
	"testing"
	"time"

	"github.com/GrainedLotus515/gobard/internal/player"
	"github.com/bwmarrin/discordgo"
)

func TestResolveQueryUsesPlaceholderForDirectYouTubeURL(t *testing.T) {
	b := &Bot{}

	tracks, err := b.resolveQuery("https://music.youtube.com/watch?v=abc123XYZ89&si=token&t=42", "user-1")
	if err != nil {
		t.Fatalf("resolveQuery() error = %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("len(resolveQuery()) = %d, want 1", len(tracks))
	}

	track := tracks[0]
	if track.ID != "abc123XYZ89" {
		t.Fatalf("track.ID = %q, want %q", track.ID, "abc123XYZ89")
	}
	if track.Title != "Loading track..." {
		t.Fatalf("track.Title = %q, want %q", track.Title, "Loading track...")
	}
	if track.Artist != "YouTube" {
		t.Fatalf("track.Artist = %q, want %q", track.Artist, "YouTube")
	}
	if track.URL != "https://www.youtube.com/watch?v=abc123XYZ89" {
		t.Fatalf("track.URL = %q, want canonical watch URL", track.URL)
	}
	if track.Source != player.SourceYouTube {
		t.Fatalf("track.Source = %q, want %q", track.Source, player.SourceYouTube)
	}
	if track.RequestedBy != "user-1" {
		t.Fatalf("track.RequestedBy = %q, want %q", track.RequestedBy, "user-1")
	}
	if !track.MetadataPending {
		t.Fatal("track.MetadataPending = false, want true")
	}
}

func TestHydrateFastURLTrackAsyncReplacesQueuedTrackAndEditsPendingResponse(t *testing.T) {
	manager := player.NewManager()
	guildID := "guild-fastpath"
	p := manager.GetPlayer(guildID)

	current := &player.Track{
		Title: "current",
		URL:   "https://example.com/current",
	}
	placeholder := &player.Track{
		ID:              "abc123XYZ89",
		Title:           "Loading track...",
		Artist:          "YouTube",
		URL:             "https://www.youtube.com/watch?v=abc123XYZ89",
		Source:          player.SourceYouTube,
		RequestedBy:     "user-1",
		RequestTraceID:  "trace-queued",
		RequestedAt:     time.Now(),
		MetadataPending: true,
	}

	p.Queue.Add(current)
	p.Queue.Add(placeholder)
	p.Queue.Next()

	editCalls := make(chan *discordgo.WebhookEdit, 1)
	b := &Bot{
		PlayerManager: manager,
		nowFn:         time.Now,
	}
	b.getVideoInfoFn = func(url string) (*player.Track, error) {
		if url != placeholder.URL {
			return nil, fmt.Errorf("unexpected url %q", url)
		}
		return &player.Track{
			ID:                      placeholder.ID,
			Title:                   "Hydrated Song",
			Artist:                  "Hydrated Artist",
			Duration:                3 * time.Minute,
			Thumbnail:               "https://example.com/thumb.jpg",
			IsLive:                  false,
			StreamURL:               "https://media.example/audio.webm",
			StreamHeaders:           map[string]string{"User-Agent": "yt-dlp"},
			StreamExpiresAt:         time.Now().Add(10 * time.Minute),
			MetadataFetchedAt:       time.Now(),
			DirectStreamUnavailable: false,
		}, nil
	}
	b.interactionResponseEditFn = func(_ *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
		editCalls <- edit
		return &discordgo.Message{}, nil
	}

	b.registerPendingInteractionResponse(
		placeholder.RequestTraceID,
		&discordgo.Interaction{ID: "interaction-1"},
		guildID,
		"channel-1",
		placeholder,
		pendingInteractionModeQueuedSingle,
	)

	b.hydrateFastURLTrackAsync(placeholder.RequestTraceID, placeholder)

	var edit *discordgo.WebhookEdit
	select {
	case edit = <-editCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for interaction response edit")
	}

	next := p.Queue.Peek()
	if next == nil {
		t.Fatal("Peek() = nil, want hydrated track")
	}
	if next == placeholder {
		t.Fatal("Peek() returned placeholder pointer, want replacement")
	}
	if next.Title != "Hydrated Song" {
		t.Fatalf("Peek().Title = %q, want %q", next.Title, "Hydrated Song")
	}
	if next.MetadataPending {
		t.Fatal("Peek().MetadataPending = true, want false")
	}
	if next.StreamURL == "" {
		t.Fatal("Peek().StreamURL = empty, want hydrated stream URL")
	}

	if edit.Embeds == nil || len(*edit.Embeds) != 1 {
		t.Fatalf("edit embeds = %#v, want exactly one embed", edit.Embeds)
	}

	embed := (*edit.Embeds)[0]
	if embed.Title != "Hydrated Song" {
		t.Fatalf("embed.Title = %q, want %q", embed.Title, "Hydrated Song")
	}
	if embed.Description != "Hydrated Artist" {
		t.Fatalf("embed.Description = %q, want %q", embed.Description, "Hydrated Artist")
	}
	if len(embed.Fields) < 3 {
		t.Fatalf("len(embed.Fields) = %d, want at least 3", len(embed.Fields))
	}
	if embed.Fields[2].Name != "Queued" {
		t.Fatalf("context field name = %q, want %q", embed.Fields[2].Name, "Queued")
	}
	if embed.Fields[2].Value != "Queue position #2" {
		t.Fatalf("context field value = %q, want %q", embed.Fields[2].Value, "Queue position #2")
	}

	if _, ok := b.getPendingInteractionResponse(placeholder.RequestTraceID); ok {
		t.Fatal("pending interaction response still present after successful hydration update")
	}
}

func TestHydrateFastURLTrackAsyncKeepsPlaceholderWhenHydrationFails(t *testing.T) {
	manager := player.NewManager()
	guildID := "guild-fastpath-fail"
	p := manager.GetPlayer(guildID)

	placeholder := &player.Track{
		ID:              "abc123XYZ89",
		Title:           "Loading track...",
		Artist:          "YouTube",
		URL:             "https://www.youtube.com/watch?v=abc123XYZ89",
		Source:          player.SourceYouTube,
		RequestTraceID:  "trace-fail",
		RequestedAt:     time.Now(),
		MetadataPending: true,
	}
	p.Queue.Add(placeholder)

	hydrationDone := make(chan struct{})
	editCalled := make(chan struct{}, 1)

	b := &Bot{
		PlayerManager: manager,
		nowFn:         time.Now,
	}
	b.getVideoInfoFn = func(string) (*player.Track, error) {
		close(hydrationDone)
		return nil, fmt.Errorf("video unavailable")
	}
	b.interactionResponseEditFn = func(_ *discordgo.Interaction, _ *discordgo.WebhookEdit) (*discordgo.Message, error) {
		select {
		case editCalled <- struct{}{}:
		default:
		}
		return &discordgo.Message{}, nil
	}

	b.registerPendingInteractionResponse(
		placeholder.RequestTraceID,
		&discordgo.Interaction{ID: "interaction-2"},
		guildID,
		"channel-1",
		placeholder,
		pendingInteractionModeStartingPlayback,
	)

	b.hydrateFastURLTrackAsync(placeholder.RequestTraceID, placeholder)

	select {
	case <-hydrationDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hydration failure")
	}

	select {
	case <-editCalled:
		t.Fatal("interaction response edit was called on hydration failure")
	default:
	}

	if current := p.Queue.Current(); current != nil {
		t.Fatalf("Current() = %p, want nil because no track has started", current)
	}

	tracks, _ := p.Queue.Snapshot()
	if len(tracks) != 1 || tracks[0] != placeholder {
		t.Fatalf("queue snapshot = %#v, want placeholder track to remain", tracks)
	}

	if _, ok := b.getPendingInteractionResponse(placeholder.RequestTraceID); !ok {
		t.Fatal("pending interaction response removed after hydration failure, want it to remain")
	}
}
