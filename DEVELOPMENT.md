# Development Guide

## Supported environment

Use Docker for builds, tests, linting, and smoke checks. Discord voice connections require libdave, and the Dockerfile is the reproducible environment used by CI. The release target is `linux/amd64` only.

```bash
make docker-test
make docker-lint
make docker-build
make docker-smoke
```

`docker-test` runs `go test -race ./...`. `docker-lint` verifies formatting without rewriting files, then runs `go vet` and the pinned golangci-lint v2 release. Both targets use Docker stages so a workstation does not need a native libdave installation.

For an interactive local container, create `.env`, secure it, and use the local override:

```bash
cp .env.example .env
chmod 600 .env
make docker-run
make docker-logs
make docker-stop
```

`make docker-run` uses `docker-compose.local.yml` and builds `gobard:local`. The base Compose definition is for the published GHCR image. Do not use `docker compose restart` after changing environment variables; run `docker compose up -d --force-recreate` instead.

For a file-backed Discord token, comment `DISCORD_TOKEN` in `.env`, set `DISCORD_TOKEN_FILE_HOST` to an absolute host path, and add `docker-compose.secrets.yml` (or use `make docker-run-secrets`). The override mounts that host file read-only at `/run/secrets/discord_token` and sets the matching application variable. Compose file-source secrets are bind mounts, so set host ownership/mode explicitly: the file and parent directory must be traversable/readable by UID 1000 (for example `1000:1000`, mode `0400`). Do not place the token itself in any Compose file or make it world-readable.

## Container build contract

The Dockerfile has independent stages:

- `test`: race-enabled Go test suite with libdave.
- `lint`: read-only `gofmt`, vet, and golangci-lint checks.
- `vulncheck`: reachable vulnerability analysis with govulncheck.
- `runtime`: minimal non-root production image.

The builder and runtime base manifests, libdave archive, yt-dlp binary, and golangci-lint binary are versioned and checksum-verified. Runtime keeps `/app/gobard` root-owned and read-only, leaves `/app/cache` writable to UID/GID 1000, and assumes a read-only root filesystem with `/tmp` supplied as a bounded tmpfs.

The application must provide these stable operations for Docker and Compose:

```text
GET /live                         process/event-loop liveness
GET /ready                        Discord, command registration, and cache readiness
gobard healthcheck --url <URL>    short readiness probe used by HEALTHCHECK
```

`HEALTH_LISTEN_ADDR` is loopback-only and defaults to `127.0.0.1:8080`; do not publish it through Compose unless an authenticated reverse proxy is deliberately added.

## Architecture constraints

GoBard is a Discord music bot with one guild-scoped playback controller per server. Keep controller transitions explicit: natural completion, skip, seek, removal, stop, intentional disconnect, transport failure, and source failure have distinct outcomes. Background work must receive a context and complete before shutdown returns.

Media input is untrusted. Accept plain search text or exact allowlisted HTTPS YouTube video/playlist URLs only, then canonicalize them before playback; never pass arbitrary URLs, local paths, or private-network addresses to yt-dlp. Preserve URL validation when adding any new source behavior.

The cache is transactional and bounded. Active readers hold leases so eviction cannot remove a file that FFmpeg is opening. Do not reintroduce background downloads that survive a skip, disconnect, or application shutdown.

## CI and release flow

Gitea Actions perform Docker-based quality validation only. They do not build or publish registry artifacts.

GitHub Actions is the sole GHCR publisher. For pull requests it runs Docker test, lint, reachable-vulnerability, runtime-image, Trivy, and SPDX SBOM gates without pushing. On `main` and version tags, it builds the `linux/amd64` image once locally, scans that exact image, generates its SBOM, pushes an unadvertised staging reference, signs and attests its digest, then promotes SHA/semver/`latest` tags by that verified digest only after every gate succeeds.

Release signing requires repository secrets `COSIGN_PRIVATE_KEY` and `COSIGN_PASSWORD`, plus the matching committed `cosign.pub`. The workflow verifies the pair before a registry push and fails closed if it is absent or mismatched. Never commit a private key. Deployment is intentionally outside CI.

## Native builds

Native work is unsupported by the standard workflow. If you deliberately need it, install libdave with `scripts/install-libdave.sh`, configure `PKG_CONFIG_PATH` and `LD_LIBRARY_PATH`, then use Go tooling manually. Native output is not a replacement for the Docker validation gates.
