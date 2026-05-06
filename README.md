# GoBard 🎧

[![Build Status](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions/workflows/go-test.yml/badge.svg)](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions)
[![Docker Build](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions/workflows/docker-build.yml/badge.svg)](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> A self‑hosted Discord music bot that just works. Play YouTube, Spotify, or any audio URL in your server with simple slash commands.

---

## Table of Contents

- [Features](#features)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Commands](#commands)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [License](#license)

---

## Features

- 🎵 **Play anything** — YouTube videos, Spotify tracks, playlists, albums, or direct audio URLs
- ⏩ **Seek and skip** — Fast‑forward, rewind, or jump to a timestamp
- 🔄 **Queue control** — Shuffle, loop, move, and remove tracks
- 🎚️ **Smart volume** — Auto‑duck when people speak in voice chat
- 💾 **Local cache** — Frequently played songs stay on disk for instant playback
- 🔍 **SponsorBlock** — Automatically skip non‑music segments
- 📦 **Docker ready** — One command and you're running
- 🌐 **Slash commands** — Clean, modern Discord integration

---

## Quick Start

The easiest way to run GoBard is with Docker.

### 1. Grab the code

```bash
git clone https://git.grainedlotus.com/GrainedLotus515/GoBard.git
cd GoBard
cp .env.example .env
```

### 2. Add your Discord token

Edit `.env` and set at least:

```bash
DISCORD_TOKEN=your_bot_token_here
```

> **Need a bot token?** Head to the [Discord Developer Portal](https://discord.com/developers/applications), create an app, enable the **Bot** scope, and copy the token. Don't forget to invite the bot to your server with the `bot` and `application.commands` scopes.

### 3. Run it

```bash
docker-compose up -d
```

Or with plain Docker:

```bash
docker build -t gobard .
docker run -d --name gobard --env-file .env gobard
```

That's it! The bot should appear online in your server and respond to `/play`.

### Optional extras

- **YouTube search** — Add a `YOUTUBE_API_KEY` for faster, more reliable searches
- **Spotify** — Add `SPOTIFY_CLIENT_ID` and `SPOTIFY_CLIENT_SECRET` to play Spotify links

See [`.env.example`](.env.example) for every available option.

---

## Configuration

All settings are environment variables. The ones you'll care about most:

| Variable | Required? | Description |
|----------|-----------|-------------|
| `DISCORD_TOKEN` | **Yes** | Your Discord bot token |
| `YOUTUBE_API_KEY` | No | Faster YouTube search |
| `SPOTIFY_CLIENT_ID` | No | Spotify integration |
| `SPOTIFY_CLIENT_SECRET` | No | Spotify integration |
| `CACHE_DIR` | No | Where to store cached audio (default: `./cache`) |
| `CACHE_LIMIT` | No | Max cache size, e.g. `2GB` |
| `DEFAULT_VOLUME` | No | Default playback volume `0`–`100` |
| `REDUCE_VOL_WHEN_VOICE` | No | Duck audio when someone speaks (`true`/`false`) |
| `ENABLE_SPONSORBLOCK` | No | Skip sponsor segments (`true`/`false`) |

> **Tip:** After changing `.env`, run `docker-compose restart` to pick up the new settings.

---

## Commands

All commands are Discord slash commands. Type `/` in any text channel to see them.

### Playback

| Command | What it does |
|---------|-------------|
| `/play <query>` | Search or queue a track, playlist, or URL |
| `/pause` | Pause the music |
| `/resume` | Resume playback |
| `/skip` | Skip to the next track |
| `/stop` | Stop and clear the queue |
| `/disconnect` | Leave the voice channel |

### Queue

| Command | What it does |
|---------|-------------|
| `/queue` | See what's coming up |
| `/now-playing` | See the current track |
| `/shuffle` | Randomise the queue |
| `/loop` | Toggle looping the current track |
| `/move <from> <to>` | Reorder a track |
| `/remove <position>` | Delete a track from the queue |
| `/clear` | Clear the queue (keeps the current track) |

### Playback control

| Command | What it does |
|---------|-------------|
| `/volume <level>` | Set volume (`0`–`100`) |
| `/seek <timestamp>` | Jump to a time, e.g. `1:30` or `90s` |
| `/fseek <seconds>` | Fast‑forward by that many seconds |

### Bot settings

| Command | What it does |
|---------|-------------|
| `/config show` | See current settings |
| `/config set-reduce-vol-when-voice <enabled>` | Toggle auto‑ducking |
| `/config set-reduce-vol-when-voice-target <volume>` | Ducking volume level |

---

## Troubleshooting

| Problem | Likely cause | Fix |
|---------|------------|-----|
| Bot won't join voice | Missing permissions | Give the bot **Connect** and **Speak** in the channel |
| No audio | FFmpeg or yt‑dlp missing | This shouldn't happen in Docker; restart the container |
| YouTube search is slow | No API key | Add `YOUTUBE_API_KEY` to `.env` |
| Spotify links don't work | Missing credentials | Add `SPOTIFY_CLIENT_ID` and `SPOTIFY_CLIENT_SECRET` |
| Commands don't show up | Registration delay | Wait up to an hour for global commands, or restart the bot |

Still stuck? Open an issue or check out [DEVELOPMENT.md](DEVELOPMENT.md) for more technical details.

---

## Contributing

PRs are welcome! A few guidelines:

1. Fork the repo and create a feature branch.
2. Make your changes.
3. Submit a pull request with a clear description.

See [DEVELOPMENT.md](DEVELOPMENT.md) for the full development workflow, architecture, and CI details.

---

## License

MIT — see the [LICENSE](LICENSE) file.

---

**Made with ❤️ and Go**
