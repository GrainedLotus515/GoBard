# Development Guide

This document covers the technical details of GoBard — architecture, project structure, building from source, testing, and the CI/CD pipeline.

---

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Project Structure](#project-structure)
- [Building from Source](#building-from-source)
  - [Prerequisites](#prerequisites)
  - [Native Build](#native-build)
  - [Docker Build](#docker-build)
- [Development Workflow](#development-workflow)
  - [Building](#building)
  - [Testing](#testing)
  - [Linting](#linting)
- [CI/CD Pipeline](#cicd-pipeline)
- [Comparison with Muse](#comparison-with-muse)
- [Contributing](#contributing)

---

## Architecture Overview

GoBard is written in Go with a modular, service-oriented architecture.

```
GoBard
├── cmd/gobard          # Application bootstrap
├── internal
│   ├── bot             # Discord bot core (commands, handlers, lifecycle)
│   ├── player          # Queue + playback logic (one per guild)
│   ├── cache           # LRU file cache with size limits
│   ├── youtube         # yt-dlp integration for YouTube content
│   ├── spotify         # Spotify Web API → YouTube conversion
│   └── config          # Environment-driven configuration
└── scripts
    └── scripts.sh      # Helper scripts (e.g. libdave installer)
```

### Key Patterns

- **Service-Oriented Design** — Each subsystem (YouTube, Spotify, Cache, Player) is an isolated service with clean interfaces and dependency injection through the main `Bot` struct.
- **Concurrent Playback** — Each Discord guild owns a dedicated `Player` goroutine that manages its own queue and audio stream. Queue operations are protected by `RWMutex`.
- **Double-Checked Locking** — `Cache.GetOrCreate()` avoids race conditions and redundant downloads when multiple guilds request the same track simultaneously.
- **Graceful Stop** — `Player.Stop()` flushes buffers, signals goroutines, and drains channels before shutting down.
- **Dual Encoding** — Cached files are encoded with a custom FFmpeg pipeline; live streams use a streaming encoder (yt-dlp → FFmpeg → Opus).

### Player System Details

The player system is the heart of GoBard:

- **Guild-specific** — Every Discord server gets its own isolated player instance.
- **playLoop** — A main goroutine handles queue advancement and track transitions.
- **playTrack** — Individual tracks run in their own goroutine with proper stop signaling via `stopChan`.
- **State Safety** — Always check `p.Queue.Next()` return values to prevent infinite loops when the queue ends. Use `p.Stop()` before starting new playback in seek operations to avoid duplicate streams.

---

## Project Structure

```
gobard/
├── cmd/
│   └── gobard/
│       └── main.go          # Application entry point
├── internal/
│   ├── bot/
│   │   ├── bot.go           # Bot lifecycle & session management
│   │   ├── commands.go      # Slash command registration
│   │   └── handlers.go      # Interaction handlers
│   ├── cache/
│   │   └── cache.go         # LRU file cache with GetOrCreate()
│   ├── config/
│   │   └── config.go        # Environment variable loading
│   ├── player/
│   │   ├── player.go        # Queue & playback logic
│   │   ├── track.go         # Track metadata & state
│   │   ├── ffmpeg_encoder.go # Cached file encoding
│   │   └── streaming_encoder.go # Live stream encoding
│   ├── spotify/
│   │   └── spotify.go       # Spotify → YouTube conversion
│   └── youtube/
│       └── youtube.go       # yt-dlp integration
├── scripts/
│   └── install-libdave.sh   # libdave E2EE library installer
├── .env.example
├── Dockerfile
├── docker-compose.yml
├── Makefile
├── go.mod
├── go.sum
├── LICENSE
├── README.md
└── DEVELOPMENT.md
```

---

## Building from Source

### Prerequisites

| Component | Minimum Version | Notes |
|-----------|-----------------|-------|
| Go | 1.21+ | Currently using 1.25.2 |
| FFmpeg | 4.1+ | Required for audio processing |
| yt-dlp | Latest | Required for YouTube support |
| libdave | 1.1.0+ | Required for Discord voice connections (DAVE/E2EE) |

### Native Build

> ⚠️ **Note:** Native builds require `libdave` because Discord non-Stage voice channels now require DAVE/E2EE encryption. This is the main reason Docker is recommended for most users.

```bash
git clone https://git.grainedlotus.com/GrainedLotus515/GoBard.git
cd GoBard
go mod download

# Install libdave (required for voice connections)
sh ./scripts/install-libdave.sh

# Make libdave discoverable
export PKG_CONFIG_PATH="$HOME/.local/lib/pkgconfig:$PKG_CONFIG_PATH"
export LD_LIBRARY_PATH="$HOME/.local/lib:$LD_LIBRARY_PATH"

# Build
go build -o gobard ./cmd/gobard

# Run
./gobard
```

The `Makefile` includes these environment variables for convenience:

```bash
make build    # Build the binary
make run      # Run directly
make all      # Clean, deps, lint, build
```

### Docker Build

For a hassle-free build with all dependencies handled:

```bash
docker build -t gobard .
```

The Dockerfile uses a multi-stage build:
1. **Builder stage** — Go compiler, build tools, and libdave installation
2. **Runtime stage** — Minimal Debian image with FFmpeg, yt-dlp, and the compiled binary

Images are published for `linux/amd64` and `linux/arm64`.

---

## Development Workflow

### Building

```bash
go build -o gobard ./cmd/gobard
```

Or via Make:

```bash
make build
```

### Testing

```bash
go test ./...
```

With race detection (recommended):

```bash
go test -v -race -coverprofile=coverage.out ./...
```

Or via Make:

```bash
make test
```

> **Race Detector** — GoBard uses extensive concurrency. Always run tests with `-race` before submitting changes.

### Linting

```bash
golangci-lint run   # Comprehensive linting (see .golangci.yml)
go fmt ./...        # Format code
go vet ./...        # Static analysis
```

Or via Make:

```bash
make lint           # Runs fmt + vet
make all            # Full pipeline: clean, deps, lint, build
```

### Development Tools

```bash
make install-tools  # Install yt-dlp, check FFmpeg, install libdave
make help           # Show all available targets
```

---

## CI/CD Pipeline

GoBard uses **Gitea Actions** (`.gitea/workflows/`):

- **Go Tests** — Run against Go 1.24 and 1.25 with race detection enabled.
- **Linting** — `golangci-lint` enforces style, security, and complexity checks.
- **Security Scanning** — `trivy` scans Docker images for vulnerabilities.
- **Docker Build** — Multi-platform images (`linux/amd64`, `linux/arm64`) built and published to the registry.
- **Auto-Deploy** — Pushes to `main` trigger automatic deployment.

All pull requests must pass the full CI pipeline.

---

## Comparison with Muse

GoBard is a feature-complete recreation of [Muse](https://github.com/museofficial/muse) with a different runtime philosophy:

| Aspect | GoBard | Muse |
|--------|--------|------|
| Language | Go (compiled, strong typing) | TypeScript (interpreted, dynamic) |
| Performance | High, low memory overhead | Moderate |
| Deployment | Single static binary or Docker | Node.js environment required |
| Runtime Safety | Built-in race detector | N/A |
| Linting | golangci-lint | ESLint |
| CI | Gitea Actions | GitHub Actions |
| **Result** | Lightweight, scalable bot | Feature-rich but heavier runtime |

---

## Contributing

We welcome contributions! Please follow these guidelines:

1. Fork the repository and create a feature branch.
2. Run `make test` and `make lint` locally.
3. Submit a pull request; ensure all CI checks pass.
4. Provide clear commit messages and PR descriptions.

When adding new functionality:
- Follow the existing concurrent patterns (RWMutex, channel signaling).
- Ensure thread-safe queue operations.
- Add tests alongside source files (`*_test.go`).
- Run tests with `-race` to catch concurrency issues.
