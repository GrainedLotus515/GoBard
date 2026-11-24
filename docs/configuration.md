# Configuration

GoBard reads its settings from environment variables (normally `.env`), validates
them at startup, and exits on invalid configuration rather than guessing. After
changing a value, recreate the container with
`docker compose up -d --force-recreate`.

Set exactly one credential source: `DISCORD_TOKEN` or `DISCORD_TOKEN_FILE`.

## Application settings

| Variable | Default | Notes |
| --- | --- | --- |
| `DISCORD_TOKEN` | required | Discord bot token. Keep `.env` mode `0600`. |
| `DISCORD_TOKEN_FILE` | unset | Alternative regular file containing the token; mutually exclusive with `DISCORD_TOKEN`. Use `docker-compose.secrets.yml` for a Compose mount. |
| `CACHE_DIR` | `./cache` | Compose sets this to `/app/cache`. |
| `CACHE_LIMIT` | `2GB` | Positive `KB`, `MB`, or `GB` value. |
| `DEFAULT_VOLUME` | `100` | Integer from 0 to 100. |
| `REDUCE_VOL_WHEN_VOICE` | `false` | Enable speaking-based volume ducking. |
| `REDUCE_VOL_WHEN_VOICE_TARGET` | `70` | Integer from 0 to 100. |
| `PAUSE_TIMEOUT_SECONDS` | `600` | Leave voice after this long paused; the queue and position are kept. 0 disables; max 86400. |
| `ALONE_TIMEOUT_SECONDS` | `120` | Leave voice after this long with no non-bot member in the channel; the queue and position are kept. 0 disables; max 86400. |
| `MAX_PLAYLIST_TRACKS` | `500` | Integer from 1 to 500. |
| `YTDLP_MAX_CONCURRENCY` | `4` | Shared integer cap from 1 to 16 for every yt-dlp path: metadata, playlist work, cache downloads, and streaming fallback. |
| `MAX_GUILD_SESSIONS` | `16` | Retained guild-session limit, 1–1024; increase explicitly for larger deployments. |
| `MEMORY_BUDGET` | auto | Optional positive byte size (KB/MB/GB); lowers the visible RAM/cgroup budget. |
| `MAX_ACTIVE_STREAMS` | auto | Optional positive stream cap, no higher than derived memory/extractor capacity. |
| `QUEUE_LIMIT` | `500` | Maximum tracks per guild, 1–500. |
| `PLAY_COMMAND_CONCURRENCY` | `4` | Concurrent play operations, 1–16; saturation rejects with a retry-later response. |
| `LOG_FORMAT` | `json` | Charm JSON or `text` with identical safe event semantics. |
| `HEALTH_LISTEN_ADDR` | `127.0.0.1:8080` | Loopback-only health listener. |
| `REGISTER_COMMANDS_ON_BOT` | `false` | Use global command registration instead of guild-by-guild reconciliation. |
| `BOT_STATUS` | `online` | `online`, `idle`, `dnd`, `invisible`, or `offline`. |
| `BOT_ACTIVITY_TYPE` | `LISTENING` | `PLAYING`, `STREAMING`, `LISTENING`, `WATCHING`, or `COMPETING`. |
| `BOT_ACTIVITY` | `music` | Non-empty activity text. |
| `BOT_ACTIVITY_URL` | unset | Optional URL used for streaming activity. |
| `DEBUG` | `false` | Additional safe diagnostics; ordinary failures are always visible. |
| `DEBUG_PLAYBACK` | `false` | Deprecated compatibility setting; validated but no independent playback logger. |

Per-server settings changed with `/config` (player channel and voice ducking)
apply until the bot restarts; they are not yet persisted.

## Compose resource limits

Compose hardening defaults can be changed without editing the file:

| Variable | Default |
| --- | --- |
| `GOBARD_MEMORY_LIMIT` | `1g` |
| `GOBARD_CPUS` | `2.0` |
| `GOBARD_PIDS_LIMIT` | `256` |
| `GOBARD_TMPFS_SIZE` | `128m` |

## Capacity profiles

The base Compose file is the conservative small profile, not a hard playback
limit. Capacity is measured in simultaneous guild playback sessions; adding
listeners to one voice channel does not create another FFmpeg or Opus pipeline.

| Profile | CPU | Memory | yt-dlp | Cache | Minimum validation target |
| --- | ---: | ---: | ---: | ---: | ---: |
| Small | 2 vCPU | 1 GiB | 4 | 2 GiB | 4 simultaneous guilds |
| Medium | 4 vCPU | 2 GiB | 8 | 10 GiB | 12 simultaneous guilds |
| Large | 8 vCPU | 4 GiB | 12 | 25 GiB | 24 simultaneous guilds |

Use the same published image with a sizing override:

```bash
docker compose -f docker-compose.yml -f docker-compose.medium.yml up -d
docker compose -f docker-compose.yml -f docker-compose.large.yml up -d
```

For a local checkout, use `make docker-run-medium` or `make docker-run-large`.
The existing `GOBARD_CPUS`, `GOBARD_MEMORY_LIMIT`, and `GOBARD_PIDS_LIMIT`
variables still override a profile. `GOBARD_PROFILE_YTDLP_MAX_CONCURRENCY` and
`GOBARD_PROFILE_CACHE_LIMIT` override its media-process and cache defaults.

Resource profiles are workload targets. `MAX_GUILD_SESSIONS` bounds retained
state (default 16; the large Compose profile defaults to 32). Active streams have
a separate memory-derived limit, also capped to leave one extractor slot for
metadata when possible. `GOBARD_PROFILE_MAX_GUILD_SESSIONS` overrides the
retained-session budget. See [runtime admission](deployment.md#runtime-memory-admission)
and [measurement limits](performance.md).
