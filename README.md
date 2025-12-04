# GoBard 🎧

[![Build Status](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions/workflows/go-test.yml/badge.svg)](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions)
[![Docker Build](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions/workflows/docker-build.yml/badge.svg)](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> **GoBard** is a fully self‑hosted Discord music bot written in Go.  
>  It is a feature‑complete recreation of [Muse](https://github.com/museofficial/muse) that prioritises simplicity, reliability, and performance.

---

## Table of Contents

- [Why GoBard?](#why-gobard)
- [Features](#features)
- [Architecture Overview](#architecture-overview)
- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [Installation](#installation)
    - [Native Build](#native-build)
    - [Docker](#docker)
    - [Docker Compose](#docker-compose)
- [Configuration](#configuration)
  - [Environment Variables](#environment-variables)
  - [Cache Settings](#cache-settings)
  - [Command Registration](#command-registration)
  - [Playback Settings](#playback-settings)
- [Commands](#commands)
- [Project Structure](#project-structure)
- [Development](#development)
  - [Building](#building)
  - [Testing](#testing)
  - [Linting](#linting)
- [Troubleshooting](#troubleshooting)
- [Comparison with Muse](#comparison-with-muse)
- [CI/CD Pipeline](#ci-cd-pipeline)
- [Contributing](#contributing)
- [License](#license)

---

## Why GoBard?

| Aspect | GoBard | Muse |
|--------|--------|------|
| Language | Go (concurrent, compiled) | TypeScript (interpreted) |
| Performance | High, low memory overhead | Moderate |
| Deployment | Easy binary or Docker image | Node.js environment |
| Runtime Safety | Strong typing, race detector | Dynamic typing |
| Community | Growing Go ecosystem | Mature JavaScript ecosystem |
| **Result** | A lightweight, scalable Discord bot | A feature‑rich but heavier bot |

---

## Features

- 🎵 **Universal Music Support** – Play from YouTube, Spotify, or any direct audio URL.
- 📺 **Live‑streaming** – Stream live videos with low latency.
- ⏩ **Seeking** – Fast‑forward or rewind with `/seek` and `/fseek`.
- 🔄 **Queue Management** – Shuffle, move, remove, clear, and loop tracks.
- 🎚️ **Dynamic Volume** – Set volume 0–100, auto‑normalize, and duck when users speak.
- 💾 **Local Caching** – Store audio files on disk with configurable size limit.
- 🔍 **SponsorBlock** – Skip non‑music segments automatically.
- 🚫 **No Vote‑to‑Skip** – Direct control for a smoother experience.
- 🌐 **Full Discord Integration** – Slash commands, component interactions, and global registration.
- 📦 **Docker Ready** – Multi‑platform image with `Dockerfile` and `docker-compose.yml`.
- 🔧 **Developer Friendly** – CI/CD, linting, tests, and a clean architecture.

---

## Architecture Overview

```
GoBard
├── cmd/gobard          # Application bootstrap
├── internal
│   ├── bot             # Discord bot core
│   ├── player          # Queue + playback logic
│   ├── cache           # LRU file cache
│   ├── youtube         # yt‑dl integration
│   ├── spotify         # Spotify → YouTube conversion
│   └── config          # Environment‑driven configuration
└── scripts
    └── scripts.sh      # Helper scripts
```

> **Key Patterns**
>
> * **Service‑Oriented** – Each subsystem (YouTube, Spotify, Cache, Player) is an isolated service with clean interfaces.
> * **Concurrent Playback** – Each guild owns a dedicated `Player` goroutine that manages queue and audio streams.
> * **Double‑Checked Locking** – `Cache.GetOrCreate` avoids race conditions and redundant downloads.
> * **Graceful Stop** – `Player.Stop()` flushes buffers, signals goroutines, and drains channels.

---

## Getting Started

### Prerequisites

| Component | Minimum Version |
|-----------|-----------------|
| Go | 1.21+ |
| FFmpeg | 4.1+ |
| yt‑dlp | Latest |

### Installation

#### Native Build

```bash
git clone https://git.grainedlotus.com/GrainedLotus515/GoBard.git
cd GoBard
go mod download
cp .env.example .env   # Edit with your credentials
go build -o gobard ./cmd/gobard
./gobard
```

#### Docker

```bash
docker build -t gobard .
docker run -d --name gobard --env-file .env gobard
```

#### Docker Compose

```bash
docker-compose up -d
```

> **Tip** – All Docker images are built for multi‑platform (`linux/amd64`, `linux/arm64`) and published to `git.grainedlotus.com/grainedlotus515/gobard`.

---

## Configuration

All configuration is done via environment variables. The following table lists each variable, its default value, and a brief description.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DISCORD_TOKEN` | *required* | Discord bot token |
| `YOUTUBE_API_KEY` | *optional* | Enables YouTube Data API v3 for faster search |
| `SPOTIFY_CLIENT_ID` | *optional* | Spotify client ID |
| `SPOTIFY_CLIENT_SECRET` | *optional* | Spotify client secret |
| `CACHE_DIR` | `./cache` | Directory to store cached audio |
| `CACHE_LIMIT` | `2GB` | Maximum cache size (e.g., `512MB`, `10GB`) |
| `BOT_STATUS` | `online` | Bot presence status |
| `BOT_ACTIVITY_TYPE` | `LISTENING` | Activity type: `PLAYING`, `LISTENING`, `WATCHING`, `STREAMING` |
| `BOT_ACTIVITY` | `music` | Activity text |
| `BOT_ACTIVITY_URL` | *required if STREAMING* | URL for STREAMING activity |
| `REGISTER_COMMANDS_ON_BOT` | `false` | Register commands globally (may take up to 1 hour) |
| `WAIT_AFTER_QUEUE_EMPTIES` | `30` | Seconds to wait before leaving voice channel |
| `ENABLE_SPONSORBLOCK` | `false` | Skip sponsor blocks |
| `SPONSORBLOCK_TIMEOUT` | `5` | SponsorBlock API timeout (seconds) |
| `DEFAULT_VOLUME` | `100` | Default playback volume |
| `REDUCE_VOL_WHEN_VOICE` | `false` | Enable ducking when voice detected |
| `REDUCE_VOL_WHEN_VOICE_TARGET` | `70` | Target volume when ducking |

> **Remember** – Create a `.env` file from `.env.example` and fill in the required tokens.

---

## Commands

All commands are slash commands; they can be invoked in any text channel that has the bot present.

### Playback

| Command | Description |
|---------|-------------|
| `/play <query>` | Search or queue a track, playlist, or URL |
| `/pause` | Pause current playback |
| `/resume` | Resume playback |
| `/skip` | Skip to the next track |
| `/stop` | Stop playback and clear the queue |
| `/disconnect` | Leave the voice channel |

### Queue Management

| Command | Description |
|---------|-------------|
| `/queue` | Show the current queue |
| `/now-playing` | Show currently playing track |
| `/clear` | Clear the queue (keeps current track) |
| `/shuffle` | Randomise the queue |
| `/move <from> <to>` | Reorder a track |
| `/remove <position>` | Delete a track from the queue |
| `/loop` | Toggle looping of the current track |

### Playback Control

| Command | Description |
|---------|-------------|
| `/volume <level>` | Set volume (0‑100) |
| `/seek <position>` | Seek to a specific timestamp (`1:30`, `90s`) |
| `/fseek <seconds>` | Fast‑forward by X seconds |

### Configuration

| Command | Description |
|---------|-------------|
| `/config set-reduce-vol-when-voice <enabled>` | Enable/disable ducking |
| `/config set-reduce-vol-when-voice-target <volume>` | Set ducking target volume |
| `/config show` | Display current configuration |

> **Tip** – Use `/config show` to verify your settings after startup.

---

## Project Structure

```GoBard
gobard/
├── cmd/
│   └── gobard/
│       └── main.go          # Application entry point
├── internal/
│   ├── bot/
│   │   ├── bot.go           # Bot lifecycle
│   │   ├── commands.go      # Command registration
│   │   └── handlers.go      # Interaction handlers
│   ├── cache/
│   │   └── cache.go         # LRU file cache
│   ├── config/
│   │   └── config.go        # Environment loading
│   ├── player/
│   │   ├── player.go        # Queue & playback logic
│   │   └── track.go         # Track metadata & state
│   ├── spotify/
│   │   └── spotify.go       # Spotify → YouTube conversion
│   └── youtube/
│       └── youtube.go       # yt‑dl integration
├── .env.example
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```

> **Navigation Tip** – `internal/` contains all production code. `cmd/gobard/main.go` bootstraps the bot and injects dependencies.

---

## Development

### Building

```bash
go build -o gobard ./cmd/gobard
```

### Testing

```bash
go test ./...
```

> **Race Detector** – Run `go test -race ./...` for concurrency checks.

### Linting

```bash
golangci-lint run
go fmt ./...
go vet ./...
```

> **Automated Linting** – CI runs `make lint` which includes `fmt` and `vet`.

---

## Troubleshooting

| Issue | Likely Cause | Fix |
|-------|--------------|-----|
| Bot does not join a voice channel | Missing **Connect** or **Speak** permissions | Grant permissions in the server channel |
| No audio output | FFmpeg or yt‑dlp missing | Install `ffmpeg` and `yt-dlp` (`sudo apt install ffmpeg yt-dlp`) |
| YouTube search fails | No API key or quota exceeded | Add `YOUTUBE_API_KEY` |
| Spotify commands fail | Missing credentials | Set `SPOTIFY_CLIENT_ID` & `SPOTIFY_CLIENT_SECRET` |
| Commands not visible | Global registration delay | Wait up to 1 hour or set `REGISTER_COMMANDS_ON_BOT=false` for guild‑only |
| Queue never empties | Bot stuck after stopping | Verify `WAIT_AFTER_QUEUE_EMPTIES` is set (default 30s) |

---

## Comparison with Muse

| Feature | Muse | GoBard |
|---------|------|--------|
| Language | TypeScript | Go |
| Build | Node.js | Single static binary |
| Docker | Yes | Yes |
| Cache | Yes | Yes |
| SponsorBlock | Yes | Yes |
| Streaming | Yes | Yes |
| Linting | ESLint | golangci-lint |
| Race Detection | N/A | Built‑in `-race` |
| CI | GitHub Actions | Gitea Actions |

> **Result** – GoBard matches Muse feature‑wise while offering a more lightweight runtime and tighter concurrency guarantees.

---

## CI/CD Pipeline

GoBard uses **Gitea Actions** for continuous integration:

- **Go Tests** – Runs against Go 1.24 and 1.25 with race detection.
- **Linting** – `golangci-lint` enforces style and security checks.
- **Security** – `trivy` scans Docker images for vulnerabilities.
- **Docker Build** – Multi‑platform images are built and published to the registry.
- **Release** – Tags trigger automatic image publishing.

> **Pipeline Docs** – See `.gitea/workflows/` for detailed YAML files.

---

## Contributing

We welcome contributions! Please follow these guidelines:

1. Fork the repository and create a feature branch.
2. Run `make test` and `make lint` locally.
3. Submit a pull request; ensure all CI checks pass.
4. Provide clear commit messages and PR descriptions.

---

## License

MIT – see the [LICENSE](LICENSE) file.

---

**Made with ❤️ and Go**  
```
