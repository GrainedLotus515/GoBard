# GoBard 🎧

A self-hosted Discord music bot written in Go with simplicity and reliability in mind. GoBard is a feature-complete recreation of [Muse](https://github.com/museofficial/muse) with full feature parity.

## Features

✨ **Core Features:**
- 🎵 Play music from YouTube, Spotify, and direct URLs
- 📺 Support for livestreams
- ⏩ Seek within songs and videos
- 💾 Local caching for improved performance (configurable limit)
- 🔀 Queue management (shuffle, move, remove)
- 🔂 Loop individual tracks
- 🎚️ Volume control with normalization
- 🔇 Automatic volume reduction when users speak

🎶 **Music Sources:**
- YouTube videos and playlists
- Spotify tracks, playlists, albums, and artists (auto-converts to YouTube)
- Direct audio URLs

🎮 **No Democracy:**
- No vote-to-skip - direct control for a better experience
- Perfect for small to medium-sized servers

## Commands

All commands use Discord's slash command system:

### Playback
- `/play <query>` - Play a song, playlist, or URL
- `/pause` - Pause playback
- `/resume` - Resume playback
- `/skip` - Skip to the next song
- `/stop` - Stop playback and clear queue
- `/disconnect` - Disconnect from voice channel

### Queue Management
- `/queue` - Show current queue
- `/now-playing` - Show currently playing song
- `/clear` - Clear queue (except current song)
- `/shuffle` - Shuffle the queue
- `/move <from> <to>` - Move a song in the queue
- `/remove <position>` - Remove a song from the queue
- `/loop` - Toggle looping of current song

### Playback Control
- `/volume <level>` - Set volume (0-100)
- `/seek <position>` - Seek to position (e.g., "1:30" or "90s")
- `/fseek <seconds>` - Fast seek forward by seconds

### Configuration
- `/config set-reduce-vol-when-voice <enabled>` - Enable/disable volume reduction
- `/config set-reduce-vol-when-voice-target <volume>` - Set target volume for reduction
- `/config show` - Show current configuration

## Requirements

### System Requirements
- Go 1.21 or later
- FFmpeg 4.1 or later
- yt-dlp (for YouTube support)

### Installation

#### Install FFmpeg

**Linux (Debian/Ubuntu):**
```bash
sudo apt update
sudo apt install ffmpeg
```

**macOS:**
```bash
brew install ffmpeg
```

**Arch Linux:**
```bash
sudo pacman -S ffmpeg
```

#### Install yt-dlp

```bash
# Using pip
pip install yt-dlp

# Or using your package manager
# Debian/Ubuntu
sudo apt install yt-dlp

# macOS
brew install yt-dlp

# Arch Linux
sudo pacman -S yt-dlp
```

### API Keys Required

1. **Discord Bot Token** (required)
   - Go to [Discord Developer Portal](https://discord.com/developers/applications)
   - Create a new application
   - Go to "Bot" section and create a bot
   - Copy the token
   - Enable "Message Content Intent" and "Server Members Intent"

2. **YouTube API Key** (optional, recommended)
   - Go to [Google Cloud Console](https://console.cloud.google.com/)
   - Create a new project
   - Enable YouTube Data API v3
   - Create credentials (API key)

3. **Spotify Credentials** (optional)
   - Go to [Spotify Developer Dashboard](https://developer.spotify.com/dashboard)
   - Create an app
   - Copy Client ID and Client Secret

## Setup

1. **Clone the repository:**
```bash
git clone https://git.grainedlotus.com/GrainedLotus515/GoBard.git
cd GoBard
```

2. **Install dependencies:**
```bash
go mod download
```

3. **Configure environment variables:**
```bash
cp .env.example .env
nano .env  # Edit with your tokens
```

4. **Build and run:**
```bash
go build -o gobard ./cmd/gobard
./gobard
```

Or run directly:
```bash
go run ./cmd/gobard
```

## Docker Deployment

### Using Docker

1. **Create a `.env` file** with your configuration (see `.env.example`)

2. **Build and run:**
```bash
docker build -t gobard .
docker run -d --name gobard --env-file .env gobard
```

### Using Docker Compose

1. **Create a `.env` file** with your configuration

2. **Run:**
```bash
docker-compose up -d
```

## Configuration

All configuration is done through environment variables:

### Required
- `DISCORD_TOKEN` - Your Discord bot token

### Optional

**API Keys:**
- `YOUTUBE_API_KEY` - YouTube Data API key (improves search)
- `SPOTIFY_CLIENT_ID` - Spotify client ID
- `SPOTIFY_CLIENT_SECRET` - Spotify client secret

**Cache:**
- `CACHE_DIR` - Directory for cached files (default: `./cache`)
- `CACHE_LIMIT` - Maximum cache size (default: `2GB`)
  - Examples: `512MB`, `10GB`, `1024MB`

**Bot Appearance:**
- `BOT_STATUS` - Bot status: `online`, `idle`, `dnd` (default: `online`)
- `BOT_ACTIVITY_TYPE` - Activity type: `PLAYING`, `LISTENING`, `WATCHING`, `STREAMING` (default: `LISTENING`)
- `BOT_ACTIVITY` - Activity text (default: `music`)
- `BOT_ACTIVITY_URL` - Activity URL (required for `STREAMING` type)

**Command Registration:**
- `REGISTER_COMMANDS_ON_BOT` - Register commands globally instead of per-guild (default: `false`)
  - Set to `true` for bots in 10+ guilds
  - Note: Global command updates can take up to 1 hour to propagate

**Behavior:**
- `WAIT_AFTER_QUEUE_EMPTIES` - Seconds to wait before leaving voice channel when queue is empty (default: `30`)

**Features:**
- `ENABLE_SPONSORBLOCK` - Skip non-music segments in YouTube videos (default: `false`)
- `SPONSORBLOCK_TIMEOUT` - SponsorBlock API timeout in seconds (default: `5`)

**Playback:**
- `DEFAULT_VOLUME` - Default playback volume 0-100 (default: `100`)
- `REDUCE_VOL_WHEN_VOICE` - Reduce volume when users speak (default: `false`)
- `REDUCE_VOL_WHEN_VOICE_TARGET` - Target volume when reducing (default: `70`)

## Project Structure

```
gobard/
├── cmd/
│   └── gobard/          # Main application entry point
│       └── main.go
├── internal/
│   ├── bot/             # Discord bot logic
│   │   ├── bot.go       # Bot initialization
│   │   ├── commands.go  # Command registration
│   │   └── handlers.go  # Command handlers
│   ├── cache/           # Local file caching
│   │   └── cache.go
│   ├── config/          # Configuration management
│   │   └── config.go
│   ├── player/          # Music player and queue
│   │   ├── player.go
│   │   └── track.go
│   ├── spotify/         # Spotify integration
│   │   └── spotify.go
│   └── youtube/         # YouTube integration
│       └── youtube.go
├── .env.example         # Example environment file
├── .gitignore
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```

## Development

### Building

```bash
go build -o gobard ./cmd/gobard
```

### Running Tests

```bash
go test ./...
```

### Adding New Commands

1. Add command definition in `internal/bot/commands.go` (`registerCommands` function)
2. Add handler case in `internal/bot/commands.go` (`interactionCreate` function)
3. Implement handler in `internal/bot/handlers.go`

## Troubleshooting

### Bot doesn't join voice channel
- Ensure the bot has "Connect" and "Speak" permissions in the voice channel
- Verify you're in a voice channel when using `/play`

### Music not playing
- Check that FFmpeg is installed: `ffmpeg -version`
- Check that yt-dlp is installed: `yt-dlp --version`
- Verify cache directory is writable

### YouTube videos not found
- Add a YouTube API key for better results
- Some videos may be region-locked or age-restricted

### Spotify integration not working
- Ensure `SPOTIFY_CLIENT_ID` and `SPOTIFY_CLIENT_SECRET` are set
- Verify credentials are correct in Spotify Developer Dashboard

### Commands not showing up
- Wait a few minutes for Discord to sync commands
- If using `REGISTER_COMMANDS_ON_BOT=true`, it can take up to 1 hour
- Try kicking and re-inviting the bot

## Comparison with Muse

GoBard provides feature parity with Muse while being written in Go:

| Feature | Muse | GoBard |
|---------|------|--------|
| Language | TypeScript | Go |
| YouTube Support | ✅ | ✅ |
| Spotify Support | ✅ | ✅ |
| Playlist Support | ✅ | ✅ |
| Seeking | ✅ | ✅ |
| Local Caching | ✅ | ✅ |
| Queue Management | ✅ | ✅ |
| Volume Control | ✅ | ✅ |
| Voice Ducking | ✅ | ✅ |
| SponsorBlock | ✅ | ✅ |
| Slash Commands | ✅ | ✅ |
| Multi-Guild | ✅ | ✅ |
| Docker Support | ✅ | ✅ |

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Inspired by [Muse](https://github.com/museofficial/muse) by [@codetheweb](https://github.com/codetheweb)
- Built with [DiscordGo](https://github.com/bwmarrin/discordgo)
- Uses [yt-dlp](https://github.com/yt-dlp/yt-dlp) for YouTube support
- Uses [Spotify Web API Go](https://github.com/zmb3/spotify) for Spotify integration

## Support

If you encounter any issues or have questions:
- Open an issue on [Gitea](https://git.grainedlotus.com/GrainedLotus515/GoBard/issues)
- Check existing issues for solutions

---

Made with ❤️ and Go
