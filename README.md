# GoBard

GoBard is a self-hosted Discord music bot for **YouTube searches, videos, and playlists**. It is packaged as a hardened `linux/amd64` container with Discord DAVE/libdave voice support.

It does not support Spotify, SponsorBlock, YouTube API keys, arbitrary audio URLs, DMs, or automatic host deployment.

## Quick start

Create a private configuration file and start the published image:

```bash
git clone https://github.com/GrainedLotus515/GoBard.git
cd GoBard
cp .env.example .env
chmod 600 .env
# Set DISCORD_TOKEN in .env.
# This pulls the image, initializes ./cache, and then starts GoBard.
docker compose up -d
```

### Cache installation

Cache preparation is built into the installation command above; no host `sudo`, `mkdir`, or `chown` command is required. The base Compose file pulls `ghcr.io/grainedlotus515/gobard:latest`, then runs the one-shot `cache-init` service before starting `gobard`. The initializer creates the `./cache` bind-mount directory when needed, assigns the mount point to container UID/GID `1000:1000`, and exits. Compose starts the bot only after that step succeeds.

The initializer is safe to run again during later `docker compose up -d` operations. It changes only the cache mount point and deliberately does not recursively change ownership of existing cache contents. To confirm the installation step completed, run:

```bash
docker compose ps -a cache-init
```

To use a token file instead, comment out `DISCORD_TOKEN` in `.env`, set `DISCORD_TOKEN_FILE_HOST` to an absolute host path in `.env`, and add the secret override. The application receives the file at `/run/secrets/discord_token`:

```bash
docker compose -f docker-compose.yml -f docker-compose.secrets.yml up -d
```

Compose file-source secrets are bind mounts, so Docker may not apply requested UID/GID/mode values. Before startup, ensure the host token file and its parent directory are traversable/readable by container UID 1000; for example, use owner `1000:1000` and mode `0400`. Do not relax that file to world-readable permissions.

After changing `.env`, recreate the container so Docker applies the new environment:

```bash
docker compose up -d --force-recreate
```

Check startup with `docker compose ps`, `docker compose logs -f gobard`, or `docker inspect --format '{{.State.Health.Status}}' gobard`. The health probe calls the application’s `/ready` endpoint; it does not merely look for a process.

## Images and verification

Release images are published only to GitHub Container Registry and only for `linux/amd64`. Production operators should pin an image digest rather than `latest`:

```bash
export GOBARD_IMAGE=ghcr.io/grainedlotus515/gobard@sha256:<published-digest>
docker compose up -d
```

Published images are signed and carry SPDX SBOM and SLSA provenance attestations. Before the first release, the maintainer must commit the public half of the configured signing key as `cosign.pub`; the workflow fails closed if it is absent or does not match `COSIGN_PRIVATE_KEY`. Verify a digest with that trusted key before deployment:

```bash
cosign verify --key cosign.pub "$GOBARD_IMAGE"
cosign verify-attestation --key cosign.pub --type spdxjson "$GOBARD_IMAGE"
```

There is no deployment automation in this repository. CI validates and publishes an image; rollout and rollback remain operator actions.

## Configuration

GoBard validates supplied values at startup and exits on invalid configuration. Set exactly one credential source:

| Variable | Default | Notes |
| --- | --- | --- |
| `DISCORD_TOKEN` | required | Discord bot token. Keep `.env` mode `0600`. |
| `DISCORD_TOKEN_FILE` | unset | Alternative regular file containing the token; mutually exclusive with `DISCORD_TOKEN`. Use `docker-compose.secrets.yml` for a Compose mount. |
| `CACHE_DIR` | `./cache` | Compose sets this to `/app/cache`. |
| `CACHE_LIMIT` | `2GB` | Positive `KB`, `MB`, or `GB` value. |
| `DEFAULT_VOLUME` | `100` | Integer from 0 to 100. |
| `REDUCE_VOL_WHEN_VOICE` | `false` | Enable speaking-based volume ducking. |
| `REDUCE_VOL_WHEN_VOICE_TARGET` | `70` | Integer from 0 to 100. |
| `MAX_PLAYLIST_TRACKS` | `500` | Integer from 1 to 500. |
| `YTDLP_MAX_CONCURRENCY` | `4` | Shared integer cap from 1 to 16 for every yt-dlp path: metadata, playlist work, cache downloads, and streaming fallback. |
| `HEALTH_LISTEN_ADDR` | `127.0.0.1:8080` | Loopback-only health listener. |
| `REGISTER_COMMANDS_ON_BOT` | `false` | Use global command registration instead of guild-by-guild reconciliation. |
| `BOT_STATUS` | `online` | `online`, `idle`, `dnd`, `invisible`, or `offline`. |
| `BOT_ACTIVITY_TYPE` | `LISTENING` | `PLAYING`, `STREAMING`, `LISTENING`, `WATCHING`, or `COMPETING`. |
| `BOT_ACTIVITY` | `music` | Non-empty activity text. |
| `BOT_ACTIVITY_URL` | unset | Optional URL used for streaming activity. |
| `DEBUG`, `DEBUG_PLAYBACK` | `false` | Enable diagnostic logging only when troubleshooting. |

Compose hardening defaults can be changed without editing the file:

| Variable | Default |
| --- | --- |
| `GOBARD_MEMORY_LIMIT` | `1g` |
| `GOBARD_CPUS` | `2.0` |
| `GOBARD_PIDS_LIMIT` | `256` |
| `GOBARD_TMPFS_SIZE` | `128m` |

The bot container has a read-only root filesystem, no Linux capabilities, `no-new-privileges`, and a bounded `noexec,nosuid` `/tmp`. It writes persistent audio only to the cache mount.

## Commands and access

Commands are guild-only. `/play` accepts a search query or an exact HTTPS YouTube URL (`youtube.com`, `www.youtube.com`, `music.youtube.com`, or `youtu.be`); other URLs are rejected.

`/queue` and `/now-playing` are read-only. Playback controls and queue mutations require the caller to be in the bot’s active voice channel. Changing `/config` requires the `Manage Guild` permission. `/disconnect` intentionally leaves voice while preserving the queue; the next `/play` reconnects and resumes the preserved queue before appending the new request. `/stop` clears the queue and disconnects.

Available controls include `/play`, `/pause`, `/resume`, `/skip`, `/stop`, `/disconnect`, `/queue`, `/now-playing`, `/clear`, `/shuffle`, `/loop`, `/volume`, `/seek`, `/fseek`, `/move`, `/remove`, and `/config`.

## Development

Docker is the supported development environment because it supplies libdave. Do not rely on a native build unless libdave is intentionally installed and configured.

```bash
make docker-test    # Go tests with -race
make docker-lint    # gofmt check, vet, golangci-lint
make docker-build   # hardened runtime image
make docker-smoke   # final-image tools and permission checks
make docker-run     # checkout + docker-compose.local.yml
```

`docker-compose.yml` is the production-image definition. `docker-compose.local.yml` is an explicit override for building this checkout. See [DEVELOPMENT.md](DEVELOPMENT.md) and [CONTRIBUTING.md](CONTRIBUTING.md) for contributor workflow and CI details.
