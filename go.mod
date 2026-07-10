module github.com/GrainedLotus515/gobard

go 1.25.12

require (
	github.com/bwmarrin/discordgo v0.29.0
	github.com/charmbracelet/log v0.4.2
	github.com/disgoorg/disgo v0.19.2
	github.com/disgoorg/godave/golibdave v0.1.0
	github.com/disgoorg/snowflake/v2 v2.0.3
	github.com/hraban/opus v0.0.0-20230925203106-0188a62cb302
	github.com/joho/godotenv v1.5.1
	golang.org/x/time v0.14.0
)

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.2.3-0.20250311203215-f60798e515dc // indirect
	github.com/charmbracelet/lipgloss v1.1.0 // indirect
	github.com/charmbracelet/x/ansi v0.8.0 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.13-0.20250311204145-2c3ea96c31dd // indirect
	github.com/charmbracelet/x/term v0.2.1 // indirect
	github.com/disgoorg/godave v0.1.0 // indirect
	github.com/disgoorg/godave/libdave v0.1.0 // indirect
	github.com/disgoorg/json/v2 v2.0.0 // indirect
	github.com/disgoorg/omit v1.0.0 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/klauspost/compress v1.18.4 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sasha-s/go-csync v0.0.0-20240107134140-fcbab37b09ad // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/exp v0.0.0-20231006140011-7918f672742d // indirect
	golang.org/x/sys v0.41.0 // indirect
)

// Use ozraru's fork for gateway/session compatibility fixes.
// Actual voice transport is handled by internal/discordvoice via disgo+libdave
// because this fork still does not implement Discord's DAVE/E2EE voice protocol.
replace github.com/bwmarrin/discordgo => github.com/ozraru/discordgo v0.26.2-0.20250917201847-e6ee88434661
