# Deployment and migration

The stopped VPS stays stopped. No deployment, publishing, restart, secret
rotation, cache removal or ownership migration is authorized by this rewrite.
Do not run `docker compose up` as a verification step. An external updater or
host restart may later start a pinned deployment; operators must check their
own stopped-service policy before rollout.

Retain linux/amd64, UID/GID 1000, root-owned non-writable `/app/gobard`, read-only
root FS, `/app/cache` persistence, bounded noexec/nosuid tmpfs, capability drops,
tool/image checksums, GHCR-only publishing and Gitea validation-only workflows.

Retain token or token-file exclusivity and strict configuration validation.
SOPS/age decryption stays outside the application; mount the existing protected
decrypted token via the Compose secret override. Never commit plaintext or log
decryption output. The operator's live SOPS layout is uninspected and unchanged.

Local checks: `make docker-test`, `make docker-lint`, `make docker-build`,
`make docker-smoke`. These build/run isolated test/runtime-tool containers; do
not supply a bot token or mount persisted cache. Offline tests use temporary
fixtures. Live Discord/YouTube verification requires an authorized test guild
and credentials and is reported separately.

Rollout, when explicitly approved: retain the old digest and configuration;
back up cache without changing it; test with separate cache and a test bot;
verify health, permissions, source failure diagnostics and shutdown; pin the
approved GHCR digest; recreate rather than restart after config changes.
Rollback selects the old digest. Never claim readiness proves audibility.
# V2 runtime migration

`cmd/gobard` now starts the new composition root. The old bot/player/YouTube
orchestration and separate logger are removed. Existing cache keys and completed
media remain compatible; no production cache migration has been performed.
Settings are still process/guild-memory settings, not newly persisted state.

New strict variables: `MAX_GUILD_SESSIONS=16` (1–1024), `QUEUE_LIMIT=500`
(1–500), `PLAY_COMMAND_CONCURRENCY=4` (1–16), `LOG_FORMAT=json` (`json`/`text`).
The guild budget counts retained sessions, including disconnected selections and
settings. Set it explicitly for larger installations. `DEBUG_PLAYBACK` remains
validated for deployment compatibility but has no separate logger in v2.

Local live tests use a separately named container and a dedicated named volume.
The stopped local `gobard-temp` container is retained; the VPS remains stopped
and untouched. Login/readiness is only an integration checkpoint: real voice,
DAVE, provider retrieval and disconnect/shutdown must be checked separately.

## Durable local acceptance instance

`scripts/local-test.py` manages only containers labeled `gobard.purpose=v2-local-test`
and named `gobard-v2-test` / `gobard-v2-test-previous`. Its named cache volume is
`gobard-v2-test-cache`; it never mounts repository `./cache`. An existing legacy
`/tmp/gobard-v2-live-cache-*` test mount is copied once, while stopped, without
overwriting or deleting either copy. Only completed media files are copied.

```sh
make local-test-status
make local-test-stop
make local-test-start
make local-test-update  # builds locally, replaces only the labeled test instance
```

For first setup, build the image and use `python3 scripts/local-test.py start
--env-file /private/test.env`. The operator supplies an existing private (0600)
test env file; do not put credentials in command arguments or commit them.
Updates inherit the test container's configuration through a temporary 0600 file,
removed on completion. Token-file mounts are not provisioned by this helper;
use a private env file containing `DISCORD_TOKEN` for this isolated test setup.
Production SOPS/age and token-file deployment remain unchanged.

Updates require explicit `busy_sessions=0` and zero sampled delivery. Paused or
preparing playback and pending resolution/transport work prevent replacement.
Older images lacking the field and unreadable health are unknown, never idle;
stop them explicitly before replacement. This observation is not a command
admission lock: arrange a quiet test window so commands do not race replacement.
Failed replacement
readiness restores the previous container; the named cache survives failures.
Only one stopped rollback container is retained. The helper never removes a
volume or changes production Compose. Containers retain UID/GID 1000, read-only
root, bounded tmpfs, 1 GiB/2 CPU/256 PID limits, bounded logs, dropped capabilities
and no-new-privileges. Restart policy is `no`: after a host restart, explicitly
run `make local-test-start`. This is an acceptance environment, not an unattended
deployment or image publishing mechanism.
