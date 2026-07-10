package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/botui"
	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/GrainedLotus515/gobard/internal/player"
)

type playbackState struct {
	Track        *player.Track
	Tracks       []*player.Track
	CurrentIndex int
	TotalTracks  int
	Position     time.Duration
	Playing      bool
	Paused       bool
	LoopEnabled  bool
	LoopRunning  bool
}

func (b *Bot) getPlaybackState(guildID string) playbackState {
	p := b.PlayerManager.GetPlayer(guildID)
	tracks, currentIndex := p.Queue.Snapshot()
	playing, paused, position := p.GetPlaybackState()

	loopRunning := p.IsLoopRunning()
	if currentIndex < 0 && len(tracks) > 0 && loopRunning {
		currentIndex = 0
	}

	var current *player.Track
	if currentIndex >= 0 && currentIndex < len(tracks) {
		current = tracks[currentIndex]
	}

	return playbackState{
		Track:        current,
		Tracks:       tracks,
		CurrentIndex: currentIndex,
		TotalTracks:  len(tracks),
		Position:     position,
		Playing:      playing,
		Paused:       paused,
		LoopEnabled:  p.Queue.IsLoopEnabled(),
		LoopRunning:  loopRunning,
	}
}

func (b *Bot) buildPlaybackCard(
	track *player.Track,
	totalTracks int,
	paused bool,
	loopEnabled bool,
	contextLabel string,
	contextValue string,
	showControls bool,
) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	if track == nil {
		return nil, nil
	}

	spec := botui.PlaybackCardSpec{
		Title:        track.Title,
		URL:          track.URL,
		Artist:       track.Artist,
		ThumbnailURL: track.Thumbnail,
		Length:       formatTrackLength(track),
		RequestedBy:  track.RequestedBy,
		ContextLabel: contextLabel,
		ContextValue: contextValue,
		Footer:       playbackFooter(track, totalTracks, loopEnabled),
		ShowControls: showControls,
		Paused:       paused,
		LoopEnabled:  loopEnabled,
	}

	return botui.BuildPlaybackCard(spec)
}

func (b *Bot) buildQueueCard(state playbackState, requestedPage int) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	if state.TotalTracks == 0 {
		return botui.BuildStatusCard(botui.StatusCardSpec{
			Title:       "Queue Is Empty",
			Description: "Add a track with `/play` to get things moving.",
			Color:       botui.ColorInfo,
		}), nil
	}

	startIndex := 0
	if state.Track != nil {
		startIndex = state.CurrentIndex + 1
	}
	if startIndex < 0 {
		startIndex = 0
	}
	if startIndex > len(state.Tracks) {
		startIndex = len(state.Tracks)
	}

	upcoming := state.Tracks[startIndex:]
	pageSize := botui.QueuePageSize()
	totalPages := 1
	if len(upcoming) > 0 {
		totalPages = (len(upcoming) + pageSize - 1) / pageSize
	}

	page := requestedPage
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	pageStart := (page - 1) * pageSize
	pageEnd := pageStart + pageSize
	if pageEnd > len(upcoming) {
		pageEnd = len(upcoming)
	}

	pageEntries := make([]botui.QueueEntrySpec, 0, pageEnd-pageStart)
	for idx := pageStart; idx < pageEnd; idx++ {
		track := upcoming[idx]
		pageEntries = append(pageEntries, botui.QueueEntrySpec{
			Position: startIndex + idx + 1,
			Title:    track.Title,
			URL:      track.URL,
			Artist:   track.Artist,
		})
	}

	spec := botui.QueueCardSpec{
		Page:        page,
		TotalPages:  totalPages,
		TotalTracks: state.TotalTracks,
		LoopEnabled: state.LoopEnabled,
		Upcoming:    pageEntries,
	}

	if state.Track != nil {
		spec.NowPlayingTitle = state.Track.Title
		spec.NowPlayingURL = state.Track.URL
		spec.NowPlayingArtist = state.Track.Artist
		spec.NowPlayingProgress = progressText(state.Track, state.Position)
	}

	return botui.BuildQueueCard(spec)
}

func idleStatusCard(description string) *discordgo.MessageEmbed {
	return botui.BuildStatusCard(botui.StatusCardSpec{
		Title:       "Playback Is Idle",
		Description: description,
		Color:       botui.ColorInfo,
	})
}

func playbackProgressContext(track *player.Track, position time.Duration, paused bool) (string, string) {
	value := progressText(track, position)
	if paused {
		value = "Paused • " + value
	}
	return "Progress", value
}

func (b *Bot) respondStatus(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	title string,
	description string,
	color int,
) {
	embed := botui.BuildStatusCard(botui.StatusCardSpec{
		Title:       title,
		Description: description,
		Color:       color,
	})
	b.respondEmbedComponents(s, i, embed, nil)
}

func (b *Bot) respondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	b.respondEmbedComponents(s, i, embed, nil)
}

func (b *Bot) deferInteractionResponse(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if s == nil || i == nil || i.Interaction == nil {
		return fmt.Errorf("missing session or interaction")
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			AllowedMentions: noAllowedMentions(),
		},
	})
	if err != nil {
		logger.Error("Failed to defer interaction response", "err", err)
	}
	return err
}

func (b *Bot) respondEmbedComponents(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	embed *discordgo.MessageEmbed,
	components []discordgo.MessageComponent,
) {
	if s == nil || i == nil || i.Interaction == nil {
		logger.Error("Unable to respond to interaction", "reason", "missing session or interaction")
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:          []*discordgo.MessageEmbed{embed},
			Components:      components,
			AllowedMentions: noAllowedMentions(),
		},
	}); err != nil {
		logger.Error("Failed to respond to interaction", "err", err)
	}
}

func (b *Bot) editDeferredEmbedComponents(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	embed *discordgo.MessageEmbed,
	components []discordgo.MessageComponent,
) {
	embeds := []*discordgo.MessageEmbed{embed}
	if s == nil || i == nil || i.Interaction == nil {
		logger.Error("Unable to edit deferred interaction", "reason", "missing session or interaction")
		return
	}
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:         ptrString(""),
		Embeds:          &embeds,
		Components:      &components,
		AllowedMentions: noAllowedMentions(),
	}); err != nil {
		logger.Error("Failed to edit deferred interaction", "err", err)
	}
}

func (b *Bot) updateComponentMessage(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	embed *discordgo.MessageEmbed,
	components []discordgo.MessageComponent,
) {
	if s == nil || i == nil || i.Interaction == nil {
		logger.Error("Unable to update component message", "reason", "missing session or interaction")
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:         "",
			Embeds:          []*discordgo.MessageEmbed{embed},
			Components:      components,
			AllowedMentions: noAllowedMentions(),
		},
	}); err != nil {
		logger.Error("Failed to update component message", "err", err)
	}
}

func (b *Bot) respondComponentError(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	title string,
	description string,
) {
	if s == nil || i == nil || i.Interaction == nil {
		logger.Error("Unable to respond to component error", "reason", "missing session or interaction")
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{
				botui.BuildStatusCard(botui.StatusCardSpec{
					Title:       title,
					Description: description,
					Color:       botui.ColorError,
				}),
			},
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: noAllowedMentions(),
		},
	}); err != nil {
		logger.Error("Failed to respond to component error", "err", err)
	}
}

func noAllowedMentions() *discordgo.MessageAllowedMentions {
	return &discordgo.MessageAllowedMentions{}
}

func playbackFooter(track *player.Track, totalTracks int, loopEnabled bool) string {
	parts := []string{formatSource(track.Source), fmt.Sprintf("%d tracks in queue", totalTracks)}
	if loopEnabled {
		parts = append(parts, "Loop on")
	}
	return strings.Join(parts, " • ")
}

func formatTrackLength(track *player.Track) string {
	if track == nil {
		return "Unknown"
	}
	if track.IsLive {
		return "LIVE"
	}
	if track.Duration <= 0 {
		return "Unknown"
	}
	return formatDuration(track.Duration)
}

func progressText(track *player.Track, position time.Duration) string {
	if track == nil {
		return "00:00"
	}

	current := getDisplayPosition(track, position)
	if track.IsLive {
		return fmt.Sprintf("%s elapsed", formatDuration(current))
	}

	return fmt.Sprintf("%s / %s", formatDuration(current), formatTrackLength(track))
}

func formatSource(source player.TrackSource) string {
	switch source {
	case player.SourceYouTube:
		return "YouTube"
	case player.SourceSpotify:
		return "Spotify"
	case player.SourceDirect:
		return "Direct"
	default:
		return "Unknown"
	}
}
