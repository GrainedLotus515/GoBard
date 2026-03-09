package botui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

const (
	ColorInfo    = 0x2563EB
	ColorSuccess = 0x16A34A
	ColorWarning = 0xD97706
	ColorError   = 0xDC2626

	maxTrackTitleLen = 80
	maxArtistLen     = 60
	maxQueueEntryLen = 70

	queuePageSize = 8
)

type ComponentAction string

const (
	ActionPlaybackTogglePause ComponentAction = "music:playback:toggle_pause"
	ActionPlaybackSkip        ComponentAction = "music:playback:skip"
	ActionPlaybackToggleLoop  ComponentAction = "music:playback:toggle_loop"
	ActionPlaybackStop        ComponentAction = "music:playback:stop"
	ActionQueuePage           ComponentAction = "music:queue:page"
)

type ComponentMetadata struct {
	Page int
}

type PlaybackCardSpec struct {
	Title        string
	URL          string
	Artist       string
	ThumbnailURL string
	Length       string
	RequestedBy  string
	ContextLabel string
	ContextValue string
	Footer       string
	ShowControls bool
	Paused       bool
	LoopEnabled  bool
}

type QueueEntrySpec struct {
	Position int
	Title    string
	URL      string
	Artist   string
}

type QueueCardSpec struct {
	NowPlayingTitle    string
	NowPlayingURL      string
	NowPlayingArtist   string
	NowPlayingProgress string
	Upcoming           []QueueEntrySpec
	Page               int
	TotalPages         int
	TotalTracks        int
	LoopEnabled        bool
}

type StatusCardSpec struct {
	Title       string
	Description string
	Color       int
	Footer      string
}

func BuildPlaybackCard(spec PlaybackCardSpec) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	embed := &discordgo.MessageEmbed{
		Title:       truncate(spec.Title, maxTrackTitleLen),
		URL:         spec.URL,
		Description: truncate(spec.Artist, maxArtistLen),
		Color:       ColorInfo,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Length",
				Value:  fallback(spec.Length, "Unknown"),
				Inline: true,
			},
			{
				Name:   "Requested by",
				Value:  mentionUser(spec.RequestedBy),
				Inline: true,
			},
			{
				Name:   fallback(spec.ContextLabel, "Status"),
				Value:  fallback(spec.ContextValue, "Ready"),
				Inline: true,
			},
		},
	}

	if spec.ThumbnailURL != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: spec.ThumbnailURL}
	}
	if spec.Footer != "" {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: spec.Footer}
	}

	if !spec.ShowControls {
		return embed, nil
	}

	pauseLabel := "Pause"
	pauseStyle := discordgo.SecondaryButton
	if spec.Paused {
		pauseLabel = "Resume"
		pauseStyle = discordgo.PrimaryButton
	}

	loopStyle := discordgo.SecondaryButton
	if spec.LoopEnabled {
		loopStyle = discordgo.SuccessButton
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    pauseLabel,
					Style:    pauseStyle,
					CustomID: BuildCustomID(ActionPlaybackTogglePause, ComponentMetadata{}),
				},
				discordgo.Button{
					Label:    "Skip",
					Style:    discordgo.PrimaryButton,
					CustomID: BuildCustomID(ActionPlaybackSkip, ComponentMetadata{}),
				},
				discordgo.Button{
					Label:    "Loop",
					Style:    loopStyle,
					CustomID: BuildCustomID(ActionPlaybackToggleLoop, ComponentMetadata{}),
				},
				discordgo.Button{
					Label:    "Stop",
					Style:    discordgo.DangerButton,
					CustomID: BuildCustomID(ActionPlaybackStop, ComponentMetadata{}),
				},
			},
		},
	}

	return embed, components
}

func BuildQueueCard(spec QueueCardSpec) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	embed := &discordgo.MessageEmbed{
		Title: "Queue",
		Color: ColorInfo,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "Now Playing",
				Value: formatNowPlayingValue(
					spec.NowPlayingTitle,
					spec.NowPlayingURL,
					spec.NowPlayingArtist,
					spec.NowPlayingProgress,
				),
			},
			{
				Name:  "Up Next",
				Value: formatUpcoming(spec.Upcoming),
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: queueFooter(spec),
		},
	}

	var components []discordgo.MessageComponent
	if spec.TotalPages > 1 {
		prevPage := spec.Page - 1
		if prevPage < 1 {
			prevPage = 1
		}
		nextPage := spec.Page + 1
		if nextPage > spec.TotalPages {
			nextPage = spec.TotalPages
		}

		components = []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Prev",
						Style:    discordgo.SecondaryButton,
						CustomID: BuildCustomID(ActionQueuePage, ComponentMetadata{Page: prevPage}),
						Disabled: spec.Page <= 1,
					},
					discordgo.Button{
						Label:    "Next",
						Style:    discordgo.SecondaryButton,
						CustomID: BuildCustomID(ActionQueuePage, ComponentMetadata{Page: nextPage}),
						Disabled: spec.Page >= spec.TotalPages,
					},
				},
			},
		}
	}

	return embed, components
}

func BuildStatusCard(spec StatusCardSpec) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Title:       spec.Title,
		Description: spec.Description,
		Color:       spec.Color,
	}
	if embed.Color == 0 {
		embed.Color = ColorInfo
	}
	if spec.Footer != "" {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: spec.Footer}
	}
	return embed
}

func BuildCustomID(action ComponentAction, metadata ComponentMetadata) string {
	if action == ActionQueuePage {
		return fmt.Sprintf("%s:%d", action, metadata.Page)
	}
	return string(action)
}

func ParseCustomID(id string) (ComponentAction, ComponentMetadata, error) {
	switch id {
	case string(ActionPlaybackTogglePause):
		return ActionPlaybackTogglePause, ComponentMetadata{}, nil
	case string(ActionPlaybackSkip):
		return ActionPlaybackSkip, ComponentMetadata{}, nil
	case string(ActionPlaybackToggleLoop):
		return ActionPlaybackToggleLoop, ComponentMetadata{}, nil
	case string(ActionPlaybackStop):
		return ActionPlaybackStop, ComponentMetadata{}, nil
	}

	if !strings.HasPrefix(id, string(ActionQueuePage)+":") {
		return "", ComponentMetadata{}, fmt.Errorf("unsupported custom id %q", id)
	}

	pageText := strings.TrimPrefix(id, string(ActionQueuePage)+":")
	page, err := strconv.Atoi(pageText)
	if err != nil {
		return "", ComponentMetadata{}, fmt.Errorf("invalid queue page %q", pageText)
	}
	if page < 1 {
		page = 1
	}

	return ActionQueuePage, ComponentMetadata{Page: page}, nil
}

func QueuePageSize() int {
	return queuePageSize
}

func formatNowPlayingValue(title, url, artist, progress string) string {
	if title == "" {
		return "Nothing is currently playing."
	}

	line := linkedTitle(title, url, maxQueueEntryLen)
	if artist != "" {
		line += "\n" + truncate(artist, maxArtistLen)
	}
	if progress != "" {
		line += "\n" + progress
	}
	return line
}

func formatUpcoming(entries []QueueEntrySpec) string {
	if len(entries) == 0 {
		return "No upcoming tracks."
	}

	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		title := linkedTitle(entry.Title, entry.URL, maxQueueEntryLen)
		line := fmt.Sprintf("%d. %s", entry.Position, title)
		if entry.Artist != "" {
			line += " - " + truncate(entry.Artist, maxArtistLen)
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func queueFooter(spec QueueCardSpec) string {
	page := spec.Page
	if page < 1 {
		page = 1
	}
	totalPages := spec.TotalPages
	if totalPages < 1 {
		totalPages = 1
	}

	text := fmt.Sprintf("Page %d/%d • %d tracks total", page, totalPages, spec.TotalTracks)
	if spec.LoopEnabled {
		text += " • Loop on"
	}
	return text
}

func mentionUser(userID string) string {
	if userID == "" {
		return "Unknown"
	}
	return "<@" + userID + ">"
}

func linkedTitle(title, url string, limit int) string {
	if title == "" {
		title = "Unknown track"
	}
	title = truncate(title, limit)
	if url == "" {
		return title
	}
	return fmt.Sprintf("[%s](%s)", title, url)
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func truncate(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}

	runes := []rune(text)
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}
