package botui

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestBuildPlaybackCardIncludesControlsAndMetadata(t *testing.T) {
	embed, components := BuildPlaybackCard(PlaybackCardSpec{
		Title:        "A Song",
		URL:          "https://example.com/song",
		Artist:       "An Artist",
		ThumbnailURL: "https://example.com/thumb.jpg",
		Length:       "03:14",
		RequestedBy:  "1234",
		ContextLabel: "Status",
		ContextValue: "Starting playback",
		Footer:       "YouTube • 4 tracks in queue",
		ShowControls: true,
	})

	if embed.Title != "A Song" {
		t.Fatalf("embed.Title = %q, want %q", embed.Title, "A Song")
	}
	if embed.URL != "https://example.com/song" {
		t.Fatalf("embed.URL = %q", embed.URL)
	}
	if embed.Description != "An Artist" {
		t.Fatalf("embed.Description = %q", embed.Description)
	}
	if embed.Thumbnail == nil || embed.Thumbnail.URL != "https://example.com/thumb.jpg" {
		t.Fatalf("embed.Thumbnail = %#v", embed.Thumbnail)
	}
	if len(embed.Fields) != 3 {
		t.Fatalf("len(embed.Fields) = %d, want 3", len(embed.Fields))
	}
	if embed.Fields[1].Value != "<@1234>" {
		t.Fatalf("requested by field = %q", embed.Fields[1].Value)
	}
	if len(components) != 1 {
		t.Fatalf("len(components) = %d, want 1", len(components))
	}

	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("components[0] type = %T, want discordgo.ActionsRow", components[0])
	}
	if len(row.Components) != 4 {
		t.Fatalf("len(row.Components) = %d, want 4", len(row.Components))
	}

	pauseButton, ok := row.Components[0].(discordgo.Button)
	if !ok {
		t.Fatalf("row.Components[0] type = %T, want discordgo.Button", row.Components[0])
	}
	if pauseButton.Label != "Pause" {
		t.Fatalf("pauseButton.Label = %q, want Pause", pauseButton.Label)
	}
	if pauseButton.CustomID != string(ActionPlaybackTogglePause) {
		t.Fatalf("pauseButton.CustomID = %q", pauseButton.CustomID)
	}
}

func TestBuildQueueCardPaginatesAndDisablesButtons(t *testing.T) {
	embed, components := BuildQueueCard(QueueCardSpec{
		NowPlayingTitle:    "Current Track",
		NowPlayingURL:      "https://example.com/current",
		NowPlayingArtist:   "Current Artist",
		NowPlayingProgress: "00:42 / 03:00",
		Upcoming: []QueueEntrySpec{
			{Position: 2, Title: "Second Track", URL: "https://example.com/2", Artist: "Two"},
		},
		Page:        1,
		TotalPages:  2,
		TotalTracks: 9,
		LoopEnabled: true,
	})

	if embed.Title != "Queue" {
		t.Fatalf("embed.Title = %q, want Queue", embed.Title)
	}
	if got := embed.Fields[0].Value; got == "" || got == "Nothing is currently playing." {
		t.Fatalf("now playing field = %q", got)
	}
	if got := embed.Footer.Text; got != "Page 1/2 • 9 tracks total • Loop on" {
		t.Fatalf("embed.Footer.Text = %q", got)
	}

	if len(components) != 1 {
		t.Fatalf("len(components) = %d, want 1", len(components))
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("components[0] type = %T, want discordgo.ActionsRow", components[0])
	}
	prev, ok := row.Components[0].(discordgo.Button)
	if !ok {
		t.Fatalf("row.Components[0] type = %T, want discordgo.Button", row.Components[0])
	}
	next, ok := row.Components[1].(discordgo.Button)
	if !ok {
		t.Fatalf("row.Components[1] type = %T, want discordgo.Button", row.Components[1])
	}
	if !prev.Disabled {
		t.Fatalf("prev.Disabled = false, want true")
	}
	if next.Disabled {
		t.Fatalf("next.Disabled = true, want false")
	}
	if next.CustomID != "music:queue:page:2" {
		t.Fatalf("next.CustomID = %q", next.CustomID)
	}
}

func TestBuildStatusCardUsesDefaultColor(t *testing.T) {
	embed := BuildStatusCard(StatusCardSpec{
		Title:       "Paused",
		Description: "Playback is paused.",
	})

	if embed.Color != ColorInfo {
		t.Fatalf("embed.Color = %#x, want %#x", embed.Color, ColorInfo)
	}
}

func TestParseCustomIDRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want ComponentAction
		page int
	}{
		{name: "pause", id: BuildCustomID(ActionPlaybackTogglePause, ComponentMetadata{}), want: ActionPlaybackTogglePause},
		{name: "skip", id: BuildCustomID(ActionPlaybackSkip, ComponentMetadata{}), want: ActionPlaybackSkip},
		{name: "loop", id: BuildCustomID(ActionPlaybackToggleLoop, ComponentMetadata{}), want: ActionPlaybackToggleLoop},
		{name: "stop", id: BuildCustomID(ActionPlaybackStop, ComponentMetadata{}), want: ActionPlaybackStop},
		{name: "queue", id: BuildCustomID(ActionQueuePage, ComponentMetadata{Page: 3}), want: ActionQueuePage, page: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAction, gotMetadata, err := ParseCustomID(tt.id)
			if err != nil {
				t.Fatalf("ParseCustomID(%q) error = %v", tt.id, err)
			}
			if gotAction != tt.want {
				t.Fatalf("ParseCustomID(%q) action = %q, want %q", tt.id, gotAction, tt.want)
			}
			if gotMetadata.Page != tt.page {
				t.Fatalf("ParseCustomID(%q) page = %d, want %d", tt.id, gotMetadata.Page, tt.page)
			}
		})
	}
}

func TestQueuePageSize(t *testing.T) {
	if got := QueuePageSize(); got != 8 {
		t.Fatalf("QueuePageSize() = %d, want 8", got)
	}
}
