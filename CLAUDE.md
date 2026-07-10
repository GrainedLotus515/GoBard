# GoBard Claude Code Guide

## Required workflow

Use Docker for every supported build, test, lint, and smoke check. Native libdave is intentionally not assumed to exist on a contributor workstation.

```bash
make docker-test
make docker-lint
make docker-build
make docker-smoke
```

`docker-compose.yml` runs the published GHCR image. Use `make docker-run` for the explicit local-build override. Do not delete or recursively re-own `./cache`: it is persisted user media. After environment changes, use `docker compose up -d --force-recreate`, not `restart`.

## Deployment contract

- Target: `linux/amd64` only.
- Runtime user: UID/GID 1000; `/app/gobard` remains root-owned and non-writable.
- Runtime root filesystem is read-only; `/app/cache` is the only persistent writable path and `/tmp` is a bounded tmpfs.
- Health contract: `GET /live`, `GET /ready`, and `gobard healthcheck --url http://127.0.0.1:8080/ready`.
- GHCR is the only publisher. Gitea validates Docker quality gates and never publishes or deploys images.
- Never replace pinned image/action/tool references with mutable tags, or remove checksum verification.

## Product and security constraints

- GoBard accepts YouTube search text and exact allowlisted HTTPS YouTube video/playlist URLs only, then canonicalizes them before playback. Spotify, SponsorBlock, YouTube API keys, arbitrary media URLs, and DMs are out of scope.
- Preserve URL validation before every yt-dlp subprocess invocation. Do not allow local, private-network, userinfo, redirect, or arbitrary URL bypasses.
- Keep all playback, cache, subprocess, health, and command-registration background work rooted in the bot lifecycle context and joined during shutdown.
- Queue transitions require explicit reasons: completion, skip, seek, removal, stop, intentional disconnect, transport failure, and source failure.
- Playback/queue mutations require the caller in the bot’s active voice channel; configuration mutation additionally requires `Manage Guild`.
- Do not log Discord tokens, signed stream URLs, or other credentials. Bot output must not allow untrusted mentions.

## Configuration

`DISCORD_TOKEN` and `DISCORD_TOKEN_FILE` are mutually exclusive. Configuration is strict: invalid booleans, bounds, activity/status, cache sizes, or non-loopback `HEALTH_LISTEN_ADDR` values must fail startup. See `.env.example` and `README.md` for supported variables and defaults.

## Change hygiene

The working tree may already contain user changes. Preserve unrelated edits, use `apply_patch` for tracked-file modifications, and add tests with changes to queue semantics, configuration, URL handling, cache lifecycle, Docker behavior, or CI gates.
