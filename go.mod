module github.com/GrainedLotus515/gobard

go 1.27.1

require (
	github.com/bwmarrin/discordgo v0.29.0
	github.com/charmbracelet/log v1.0.0
	github.com/disgoorg/disgo v0.19.6
	github.com/disgoorg/godave/golibdave v0.3.0
	github.com/disgoorg/snowflake/v2 v2.0.3
	github.com/hraban/opus v0.0.0-20260708213942-bde8e4304501
	github.com/joho/godotenv v1.5.1
)

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/lipgloss v1.1.0 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.15 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/disgoorg/godave v0.3.0 // indirect
	github.com/disgoorg/godave/libdave v0.3.0 // indirect
	github.com/disgoorg/json/v2 v2.0.0 // indirect
	github.com/disgoorg/omit v1.0.0 // indirect
	github.com/go-logfmt/logfmt v0.6.1 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-runewidth v0.0.30 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sasha-s/go-csync v0.0.0-20240107134140-fcbab37b09ad // indirect
	github.com/xo/terminfo v1.2.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/exp v0.0.0-20261005173118-76772065c9b0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

// Use ozraru's fork for gateway/session compatibility fixes.
// Actual voice transport is handled by internal/discordvoice via disgo+libdave
// because this fork still does not implement Discord's DAVE/E2EE voice protocol.
replace github.com/bwmarrin/discordgo => github.com/ozraru/discordgo v0.26.2-0.20251101193404-5cce117199f7
