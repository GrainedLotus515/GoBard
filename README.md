# GoBard

A self-hosted Discord music bot. Ask it for a song, a YouTube link or a whole
playlist, and it joins your voice channel and plays it, with a live player card
your server can control with buttons.

```text
/play never gonna give you up
/play https://www.youtube.com/playlist?list=...
```

GoBard runs as a single container on your own machine or server. You bring a
Discord bot token; there are no accounts, API keys or YouTube cookies to set up.

> **Status:** GoBard v2 is a rewrite that is still being validated. Real
> YouTube-to-Discord playback has been tested end to end, but some live checks
> (controls, voice reconnects, long albums, several servers at once) are still
> open. See [rewrite status](docs/rewrite/status.md) before relying on it.

## What it does

- **Plays from YouTube.** Search by name, or paste a YouTube video or playlist
  link (up to 500 tracks).
- **Keeps a queue per server.** Shuffle, loop, reorder, remove, seek and adjust
  volume while it plays.
- **Shows a live player.** One card in the channel follows the current track,
  with Pause/Resume, Skip, Repeat, Stop and Queue buttons. Command replies are
  private, so the channel stays clean.
- **Picks up where it left off.** If GoBard leaves voice, because you used
  `/disconnect`, everyone left, or playback sat paused too long, the queue is
  kept and the next `/play` resumes the song from where it stopped (live
  streams move on to the next track instead).
- **Lowers the music when people talk** (optional).
- **Respects who's listening.** Only people in the bot's voice channel can
  change playback, and only members with **Manage Server** can change settings.

What it deliberately doesn't do: Spotify, SponsorBlock, direct audio file URLs,
non-YouTube sites, or commands in DMs.

## Commands

| Command | What it does |
| --- | --- |
| `/play <song or link>` | Play a song or playlist, or add it to the queue |
| `/pause`, `/resume` | Pause or resume playback |
| `/skip` | Skip to the next song |
| `/stop` | Stop and clear the queue (the bot stays in voice) |
| `/disconnect` | Leave voice but keep the queue |
| `/now-playing` | Show the current song |
| `/queue` | Show what's coming up |
| `/clear` | Clear the queue except the current song |
| `/shuffle` | Shuffle the queue |
| `/loop` | Toggle looping of the current song |
| `/volume <0-100>` | Set the volume |
| `/seek <position>` | Jump to a position, e.g. `1:30` or `90s` |
| `/fseek <seconds>` | Skip forward by some seconds |
| `/move <from> <to>` | Move a song in the queue |
| `/remove <position>` | Remove a song from the queue |
| `/config` | View or change player-channel and voice-ducking settings (changing needs Manage Server) |

More detail on the player card and buttons is in the
[Discord interface guide](docs/discord-interface.md).

## Getting started

You'll need Docker with Compose on a `linux/amd64` host and a
[Discord bot token](https://discord.com/developers/applications).

```bash
git clone https://github.com/GrainedLotus515/GoBard.git
cd GoBard
cp .env.example .env
chmod 600 .env
# Edit .env: set DISCORD_TOKEN and GOBARD_IMAGE (a published image digest).
docker compose up -d
```

Then invite the bot to your server, join a voice channel and type `/play`.

Check it's healthy with `docker compose ps` or `docker compose logs -f gobard`.
After editing `.env`, apply changes with `docker compose up -d --force-recreate`.

The [installation guide](docs/installation.md) covers token files, larger-server
profiles, image verification and the cache setup.

## Configuration

Common settings in `.env`:

| Setting | Default | |
| --- | --- | --- |
| `DEFAULT_VOLUME` | `100` | Starting volume, 0–100 |
| `PAUSE_TIMEOUT_SECONDS` | `600` | Leave voice after being paused this long (0 = never) |
| `ALONE_TIMEOUT_SECONDS` | `120` | Leave voice after being alone this long (0 = never) |
| `REDUCE_VOL_WHEN_VOICE` | `false` | Lower the music while people speak |
| `CACHE_LIMIT` | `2GB` | Disk space for cached audio |
| `BOT_ACTIVITY` | `music` | The "Listening to …" status text |

Every setting, resource limit and capacity profile is listed in the
[configuration reference](docs/configuration.md).

## Documentation

- [Installation](docs/installation.md): running and verifying the published image
- [Configuration](docs/configuration.md): all settings and capacity profiles
- [Discord interface](docs/discord-interface.md): the player card and controls
- [Architecture](docs/architecture.md) and [playback lifecycle](docs/playback-lifecycle.md): how it works inside
- [Deployment](docs/deployment.md): rollout policy, migration from v1, memory admission
- [Observability](docs/observability.md) and [performance](docs/performance.md): logs, health and measurements
- [Rewrite status](docs/rewrite/status.md): what has and hasn't been verified

## Contributing

GoBard is written in Go. Docker is the canonical build and test environment;
on NixOS, `nix develop` gives an equivalent native shell with all dependencies,
including libdave.

```bash
make docker-test    # tests with the race detector
make docker-lint    # formatting, vet, golangci-lint
make docker-build   # hardened runtime image
make docker-smoke   # final-image checks
make docker-run     # run this checkout locally
```

See [DEVELOPMENT.md](DEVELOPMENT.md) and [CONTRIBUTING.md](CONTRIBUTING.md) for
the full workflow, benchmarks and CI details.

## License

GoBard is released under the [0BSD license](LICENSE).
