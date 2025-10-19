# GoBard Quick Start Guide

Get GoBard up and running in minutes!

## Prerequisites

Install the required dependencies:

```bash
# Install FFmpeg
# Ubuntu/Debian
sudo apt update && sudo apt install ffmpeg

# macOS
brew install ffmpeg

# Arch Linux
sudo pacman -S ffmpeg

# Install yt-dlp
pip3 install yt-dlp
# or
sudo apt install yt-dlp  # Ubuntu/Debian
brew install yt-dlp      # macOS
sudo pacman -S yt-dlp    # Arch Linux
```

## Getting a Discord Bot Token

1. Go to https://discord.com/developers/applications
2. Click "New Application" and give it a name
3. Go to the "Bot" tab and click "Add Bot"
4. Under "Token", click "Copy" to copy your bot token
5. Under "Privileged Gateway Intents", enable:
   - ✅ Presence Intent
   - ✅ Server Members Intent
   - ✅ Message Content Intent
6. Go to "OAuth2" → "URL Generator"
7. Select scopes:
   - ✅ bot
   - ✅ applications.commands
8. Select bot permissions:
   - ✅ Read Messages/View Channels
   - ✅ Send Messages
   - ✅ Embed Links
   - ✅ Connect
   - ✅ Speak
   - ✅ Use Voice Activity
9. Copy the generated URL and open it in your browser to invite the bot

## Setup in 3 Steps

### 1. Clone and Install

```bash
git clone https://github.com/lotus/gobard.git
cd gobard
go mod download
```

### 2. Configure

```bash
cp .env.example .env
nano .env  # or use your favorite editor
```

Minimal `.env` configuration:
```bash
DISCORD_TOKEN=your_bot_token_here
```

Optional but recommended:
```bash
YOUTUBE_API_KEY=your_youtube_api_key
SPOTIFY_CLIENT_ID=your_spotify_client_id
SPOTIFY_CLIENT_SECRET=your_spotify_client_secret
```

### 3. Run

```bash
# Option 1: Run directly
go run ./cmd/gobard

# Option 2: Build and run
make build
./gobard

# Option 3: Docker
docker-compose up -d
```

## First Commands

Once the bot is online and in your server:

1. **Join a voice channel** in your Discord server
2. **Play your first song:**
   ```
   /play never gonna give you up
   ```
3. **Try other commands:**
   ```
   /queue          - See what's queued
   /skip           - Skip current song
   /volume 50      - Set volume to 50%
   /pause          - Pause playback
   /resume         - Resume playback
   ```

## Testing Your Setup

### Test YouTube
```
/play https://www.youtube.com/watch?v=dQw4w9WgXcQ
```

### Test Spotify (if configured)
```
/play https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT
```

### Test Search
```
/play bohemian rhapsody
```

## Common Issues

### Bot doesn't join voice channel
- Make sure you're in a voice channel first
- Check bot has "Connect" and "Speak" permissions

### No sound
- Verify FFmpeg is installed: `ffmpeg -version`
- Verify yt-dlp is installed: `yt-dlp --version`
- Check bot has "Speak" permission

### Commands don't appear
- Wait a few minutes for Discord to sync
- Try kicking and re-inviting the bot
- Check bot has "Use Application Commands" permission

### YouTube videos fail
- Add a YouTube API key to `.env`
- Some videos are region-locked or age-restricted

## Using Docker

### Quick Start with Docker

```bash
# 1. Create .env file
cp .env.example .env
nano .env

# 2. Run with Docker Compose
docker-compose up -d

# 3. View logs
docker-compose logs -f

# 4. Stop
docker-compose down
```

### Manual Docker Run

```bash
# Build
docker build -t gobard .

# Run
docker run -d \
  --name gobard \
  --env-file .env \
  -v $(pwd)/cache:/app/cache \
  gobard
```

## Advanced Configuration

See [README.md](README.md#configuration) for all available configuration options including:
- Cache size limits
- Bot status customization
- Volume reduction when speaking
- SponsorBlock integration
- And more!

## Getting Help

- 📖 Read the full [README.md](README.md)
- 🐛 Report issues on GitHub
- 💬 Check existing issues for solutions

## Next Steps

- Configure Spotify integration for playlist support
- Adjust cache size based on your storage
- Customize bot status and activity
- Set up volume reduction for voice chat
- Enable SponsorBlock to skip intros/outros

---

Enjoy your music! 🎵
