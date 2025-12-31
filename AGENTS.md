# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

### Build and Run
- `go build -o gobard ./cmd/gobard` - Build the main binary
- `go run ./cmd/gobard` - Run the application directly
- `make build` - Build using Makefile
- `make run` - Run using Makefile

### Testing
- `go test ./...` - Run all tests
- `go test -v -race -coverprofile=coverage.out ./...` - Run tests with race detection and coverage
- `make test` - Run tests using Makefile

### Linting and Code Quality
- `golangci-lint run` - Run comprehensive linting (configured in .golangci.yml)
- `go fmt ./...` - Format code
- `go vet ./...` - Run go vet
- `make lint` - Run all linting (fmt + vet)
- `make all` - Run complete pipeline (clean, deps, lint, build)

### Dependencies
- `go mod download` - Download dependencies
- `go mod tidy` - Clean up dependencies
- `make deps` - Download and tidy dependencies

### Docker
- `docker build -t gobard .` - Build Docker image
- `docker-compose up -d` - Run with docker-compose
- `make docker-build` - Build Docker image using Makefile
- `make docker-run` - Run with docker-compose
- `make docker-logs` - Show Docker container logs
- `make docker-stop` - Stop Docker container

### Development Tools
- `make install-tools` - Install yt-dlp and check FFmpeg availability
- `make help` - Show all available Makefile targets

## Architecture

GoBard is a Discord music bot written in Go with a modular architecture:

### Core Components

**Main Entry Point (`cmd/gobard/main.go`)**
- Application initialization and startup
- Environment variable loading (.env file)
- Bot instance creation and lifecycle management

**Bot Core (`internal/bot/`)**
- `bot.go` - Bot initialization, session creation, and component wiring
- `commands.go` - Discord slash command registration and routing
- `handlers.go` - Command handler implementations
- Uses DiscordGo library with a custom fork for voice connection fixes

**Player System (`internal/player/`)**
- `player.go` - Guild-specific music player management with concurrent playback
- `track.go` - Track metadata and state management with thread-safe queue operations
- `ffmpeg_encoder.go` - Cached file audio encoding using FFmpeg
- `streaming_encoder.go` - Real-time streaming encoding (yt-dlp → FFmpeg → Opus)
- Dual encoding strategy: cached files use CustomEncoder, live streams use StreamingEncoder
- Supports seeking, looping, volume control, and queue management with proper state synchronization

**Music Sources**
- `internal/youtube/` - YouTube integration using yt-dlp
- `internal/spotify/` - Spotify Web API integration (converts to YouTube)
- Support for playlists, albums, and direct URLs

**Caching (`internal/cache/`)**
- Local file caching with configurable size limits using GetOrCreate() pattern
- Thread-safe cache operations with LRU eviction
- Background download system for performance optimization
- Cache.GetOrCreate() uses double-checked locking to prevent race conditions

**Configuration (`internal/config/`)**
- Environment-based configuration management
- Supports Discord, YouTube API, Spotify credentials
- Runtime behavior settings (volume, cache, timeouts)

### Key Architectural Patterns

**Concurrent Player Management**
- Each Discord guild has an isolated player instance with dedicated goroutines
- Main playLoop handles queue advancement and track transitions
- Separate playTrack goroutines handle individual track playback with proper stop signaling
- Thread-safe queue operations with RWMutex protection
- Critical playback state synchronization prevents infinite loops and duplicate streams

**Service-Oriented Design**
- Each major component (YouTube, Spotify, Cache) is a separate service
- Dependency injection through the Bot struct
- Clean interfaces between components

**External Tool Integration**
- FFmpeg for audio encoding/decoding and volume normalization
- yt-dlp for YouTube video extraction and direct stream URL resolution
- Discord voice API for real-time Opus audio streaming
- Two-tier encoding: cached files (FFmpeg) vs live streams (yt-dlp → FFmpeg → Opus)

## Critical Implementation Patterns

**Playback State Management**
- Always check `p.Queue.Next()` return value in playLoop to prevent infinite loops when queue ends
- Use `p.Stop()` before starting new playback in seek operations to prevent duplicate streams
- Skip operations should only stop playback, letting playLoop handle queue advancement
- Pause/resume requires checking `p.Paused` flag in the frame-sending loop

**Channel Safety**
- Drain `doneChan` before starting new playback to prevent blocking
- Use timeout protection in `WaitForCompletion()` for safety
- Employ proper stop signaling through `stopChan` in playback goroutines

**Thread Safety**
- Queue operations use RWMutex for concurrent access
- Player state modifications require proper mutex locking
- Cache operations use double-checked locking pattern in `GetOrCreate()`

## Environment Configuration

Required: `DISCORD_TOKEN`
Optional: `YOUTUBE_API_KEY`, `SPOTIFY_CLIENT_ID`, `SPOTIFY_CLIENT_SECRET`
Cache: `CACHE_DIR`, `CACHE_LIMIT`
Behavior: `DEFAULT_VOLUME`, `REDUCE_VOL_WHEN_VOICE`, `WAIT_AFTER_QUEUE_EMPTIES`

See `.env.example` for complete configuration options.

## Development Dependencies

- Go 1.21+ (currently using 1.25.2)
- FFmpeg 4.1+ (required for audio processing)
- yt-dlp (required for YouTube support)
- Discord bot token (required)
- YouTube API key (optional but recommended)
- Spotify credentials (optional)

## CI/CD Pipeline

The project uses Gitea Actions (.gitea/workflows/) for:
- Go tests on multiple versions (1.24, 1.25) with race detection
- Comprehensive linting using golangci-lint with strict configuration
- Docker multi-platform builds and registry publishing
- Security scanning with Trivy
- Automated deployment on push to main branch

All pull requests must pass the full CI pipeline including tests, linting, and security scans.

## Testing and Code Quality

Tests are located alongside source files (`*_test.go`). The project maintains high code quality standards with:
- Comprehensive golangci-lint configuration covering error handling, security, formatting, and complexity
- Go vet with extensive checks enabled
- Code formatting enforced via CI
- Race condition detection in tests
- Security scanning for vulnerabilities

When adding new functionality, ensure it follows the existing patterns and passes all quality checks.