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

Local live tests use a separately named container and a new temporary cache.
The stopped local `gobard-temp` container is retained; the VPS remains stopped
and untouched. Login/readiness is only an integration checkpoint: real voice,
DAVE, provider retrieval and disconnect/shutdown must be checked separately.
