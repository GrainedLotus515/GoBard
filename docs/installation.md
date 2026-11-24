# Installing GoBard

This guide covers running the published GoBard image with Docker Compose. For
settings, see [configuration](configuration.md); for rollout policy and the v2
migration, see [deployment](deployment.md).

## Before you start

- A Discord application with a bot user and its token.
- Docker with Compose on a `linux/amd64` host. Images are published for no other
  platform.
- A published GoBard image digest from GitHub Container Registry. Pin a digest
  rather than a mutable tag such as `latest`.

## Install and start

```bash
git clone https://github.com/GrainedLotus515/GoBard.git
cd GoBard
cp .env.example .env
chmod 600 .env
# Set DISCORD_TOKEN and GOBARD_IMAGE in .env, or export GOBARD_IMAGE:
export GOBARD_IMAGE=ghcr.io/grainedlotus515/gobard@sha256:<published-digest>
docker compose up -d
```

This pulls the image, initializes `./cache`, and then starts GoBard.

### Cache initialization

Cache preparation is built into the command above; no host `sudo`, `mkdir`, or
`chown` command is required. The base Compose file uses the required immutable
`GOBARD_IMAGE` digest, then runs the one-shot `cache-init` service before starting
`gobard`. The initializer creates the `./cache` bind-mount directory when needed,
assigns the mount point to container UID/GID `1000:1000`, and exits. Compose
starts the bot only after that step succeeds.

The initializer is safe to run again during later `docker compose up -d`
operations. It changes only the cache mount point and deliberately does not
recursively change ownership of existing cache contents. To confirm the step
completed, run:

```bash
docker compose ps -a cache-init
```

## Using a token file instead of `DISCORD_TOKEN`

Comment out `DISCORD_TOKEN` in `.env`, set `DISCORD_TOKEN_FILE_HOST` to an
absolute host path in `.env`, and add the secret override. The application
receives the file at `/run/secrets/discord_token`:

```bash
docker compose -f docker-compose.yml -f docker-compose.secrets.yml up -d
```

Compose file-source secrets are bind mounts, so Docker may not apply requested
UID/GID/mode values. Before startup, ensure the host token file and its parent
directory are traversable/readable by container UID 1000; for example, use owner
`1000:1000` and mode `0400`. Do not relax that file to world-readable permissions.

## Applying configuration changes

After changing `.env`, recreate the container so Docker applies the new
environment. `docker compose restart` does not.

```bash
docker compose up -d --force-recreate
```

## Checking that it is running

Use `docker compose ps`, `docker compose logs -f gobard`, or
`docker inspect --format '{{.State.Health.Status}}' gobard`. The health probe
calls the application's `/ready` endpoint; it does not merely look for a process.

## Larger servers

The base Compose file is the conservative small profile. Medium and large
overrides use the same image:

```bash
docker compose -f docker-compose.yml -f docker-compose.medium.yml up -d
docker compose -f docker-compose.yml -f docker-compose.large.yml up -d
```

See [capacity profiles](configuration.md#capacity-profiles) for what each one
provides.

## Images and verification

Release images are published only to GitHub Container Registry and only for
`linux/amd64`. Published images are signed and carry SPDX SBOM and SLSA
provenance attestations. Before the first release, the maintainer must commit
the public half of the configured signing key as `cosign.pub`; the workflow fails
closed if it is absent or does not match `COSIGN_PRIVATE_KEY`. Verify a digest
with that trusted key before deployment:

```bash
cosign verify --key cosign.pub "$GOBARD_IMAGE"
cosign verify-attestation --key cosign.pub --type spdxjson "$GOBARD_IMAGE"
```

There is no deployment automation in this repository. CI validates and publishes
an image; rollout and rollback remain operator actions.

## Container hardening

The bot container has a read-only root filesystem, no Linux capabilities,
`no-new-privileges`, and a bounded `noexec,nosuid` `/tmp`. It runs as UID/GID
1000 and writes persistent audio only to the cache mount.
