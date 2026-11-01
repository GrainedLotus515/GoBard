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
The pre-release `PLAYBACK_MEMORY_LIMIT` and `PLAYBACK_MEMORY_HEADROOM` settings
are superseded by `MEMORY_BUDGET` and `MAX_ACTIVE_STREAMS` below. Startup rejects
the old names rather than silently ignoring an operator's limit. Remove them,
review the new reserve formula, and set a smaller memory/stream budget if needed.
There is no direct headroom override; retained sessions and active streams are
budgeted separately. Restart after changing container memory or these settings.
`DEBUG_PLAYBACK` remains validated for deployment compatibility but has no
separate logger in v2.

## Runtime memory admission

At startup, GoBard takes the smallest visible host RAM, cgroup v2 `memory.max`
or `memory.high`, or v1 `memory.limit_in_bytes`, walking visible ancestor limits.
Unlimited values do not override a smaller limit. If no usable limit is visible,
the fallback is 512 MiB. Hidden ancestor quotas cannot be discovered through a
private cgroup namespace; set a smaller `MEMORY_BUDGET` when needed. Detection is
a startup snapshot: recreate after quota/config changes. This is an admission
policy, not a dynamic OOM prevention guarantee or a Go heap limit.

The reserved budget is `max(192 MiB, RAM/4) + 32 MiB × extractor concurrency +
4 MiB × retained guild limit`. The base covers Go/native/global cache and tmpfs
headroom; worker space covers metadata/extractor work. The guild allowance covers
500 bounded metadata entries, tickets, snapshots and adapter state. Each active
stream then receives a conservative 128 MiB allowance for acquisition, FFmpeg,
libopus/DAVE, extractor and allocator overhead. These are planning allowances,
not measured exact costs. The review's short local fixtures measured roughly
12–19 MiB anonymous memory per guild; those omit real provider/DAVE overhead and
full-ring pressure, so they cannot justify a streams-per-GB promise.

The final cap is the minimum of available stream allowances, retained guilds,
64, and `max(1, YTDLP_MAX_CONCURRENCY - 1)`. A long cold source can retain an
extractor slot until near its end, so short fixtures do not justify admitting
more cold streams than extractor capacity. With concurrency 1, metadata may wait
behind the sole stream. Defaults at 512 MiB/1 GiB admit 1/3 streams respectively;
the normal medium/large extractor settings admit at most 7/11. CPU, provider and
live listening still need independent validation.

Optional `MEMORY_BUDGET` accepts the same positive byte syntax as `CACHE_LIMIT`;
it cannot exceed a detected limit (it may replace the unavailable-limit fallback).
Optional `MAX_ACTIVE_STREAMS` must be a positive integer no larger than the
derived cap. Empty, zero, over-budget or insufficient-memory settings fail
startup with an actionable error. Existing `MAX_GUILD_SESSIONS` keeps its meaning;
it does not grant the same number of concurrent streams. Raising retained state
also consumes headroom. The `capacity.configured` event records the chosen budget.

Queue admission rejects excess active streams atomically with private feedback.
Existing admitted queues retain their slot through pause, seek and bounded
recovery; stop, completed drain, intentional disconnect and exhausted recovery
release only after the attempt joins. Reconnecting a retained queue reacquires
capacity. Idle retained sessions consume no stream slot. The configured disk
`CACHE_LIMIT` is not an in-memory allocation; cgroup file-cache charges and other
processes remain operational uncertainties.

Failure logs retain correlation IDs, category, bounded fingerprint/exit code,
allowlisted OS/network/codec reason and fixed operator `guidance`. Contextual
wrapping preserves the originating classification for both logs and Discord
feedback. Unknown diagnostics give stage-specific investigation guidance;
arbitrary stderr, URLs, cookies, credentials and metadata are never copied into
those fields. A fingerprint groups failures but cannot reconstruct their cause.

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
