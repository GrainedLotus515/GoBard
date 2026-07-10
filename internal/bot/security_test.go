package bot

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/GrainedLotus515/gobard/internal/player"
)

func TestRequireGuildMemberInteractionRejectsDMAndMissingMember(t *testing.T) {
	tests := []struct {
		name string
		i    *discordgo.InteractionCreate
		want bool
	}{
		{"nil", nil, false},
		{"dm", &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{User: &discordgo.User{ID: "user"}}}, false},
		{"missing user", &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "guild", Member: &discordgo.Member{}}}, false},
		{"guild member", &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "guild", Member: &discordgo.Member{User: &discordgo.User{ID: "user"}}}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireGuildMemberInteraction(tt.i)
			if (err == nil) != tt.want {
				t.Fatalf("requireGuildMemberInteraction() error = %v, want success %v", err, tt.want)
			}
		})
	}
}

func TestConfigMutationRequiresManageGuild(t *testing.T) {
	b := &Bot{PlayerManager: player.NewManager()}
	withoutPermission := commandInteraction("config", "guild", "user", 0, "set-reduce-vol-when-voice")
	if err := b.authorizeApplicationCommand(withoutPermission, "config"); err == nil {
		t.Fatal("config mutation authorization error = nil, want Manage Guild rejection")
	}

	withPermission := commandInteraction("config", "guild", "user", discordgo.PermissionManageGuild, "set-reduce-vol-when-voice")
	if err := b.authorizeApplicationCommand(withPermission, "config"); err != nil {
		t.Fatalf("config mutation with Manage Guild error = %v", err)
	}

	show := commandInteraction("config", "guild", "user", 0, "show")
	if err := b.authorizeApplicationCommand(show, "config"); err != nil {
		t.Fatalf("config show authorization error = %v", err)
	}
}

func TestResolveQueryRejectsArbitraryURLsBeforeYouTubeClientUse(t *testing.T) {
	b := &Bot{}
	inputs := []string{
		"http://127.0.0.1/youtube.com/watch?v=abc123XYZ89",
		"https://youtube.com.evil.test/watch?v=abc123XYZ89",
		"file:///etc/passwd",
	}
	for _, input := range inputs {
		if _, err := b.resolveQuery(input, "user"); err == nil || !strings.Contains(err.Error(), "only HTTPS YouTube") {
			t.Fatalf("resolveQuery(%q) error = %v, want unsupported URL rejection", input, err)
		}
	}
}

func TestIntentionalDisconnectMarkerIsOneShot(t *testing.T) {
	b := &Bot{}
	b.markIntentionalDisconnect("guild")
	if !b.consumeIntentionalDisconnect("guild") {
		t.Fatal("consumeIntentionalDisconnect() = false, want true")
	}
	if b.consumeIntentionalDisconnect("guild") {
		t.Fatal("consumeIntentionalDisconnect() = true after consumption, want false")
	}
	b.markIntentionalDisconnect("guild")
	b.clearIntentionalDisconnect("guild")
	if b.consumeIntentionalDisconnect("guild") {
		t.Fatal("clearIntentionalDisconnect() did not clear marker")
	}
}

func commandInteraction(command, guildID, userID string, permissions int64, subcommand string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: guildID,
		Member: &discordgo.Member{
			User:        &discordgo.User{ID: userID},
			Permissions: permissions,
		},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: command,
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: subcommand, Type: discordgo.ApplicationCommandOptionSubCommand},
			},
		},
	}}
}
