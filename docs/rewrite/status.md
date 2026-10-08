# Rewrite status — 2026-10-08

The v2 runtime is implemented and running as local `gobard-v2-test`.
The original local `gobard-temp` remains stopped. No VPS service, persisted user
cache, secrets configuration or image publisher was changed.

## Implemented checkpoints

- `81d254f`: session-owned voice join, drain cleanup, cancellation and finite
  recovery; deterministic join/stop/recovery tests.
- `e7309a5`: application cutover to v2 command/session/source/voice adapters;
  original bot/player/YouTube orchestration and duplicate logger removed.
- `a6b5155`: review fixes for bounded Gateway state, metadata object expansion,
  cleanup under saturation, skip during recovery, departed speakers, forced
  health shutdown and Docker/profile gate compatibility.

All inventoried commands and buttons have replacement mapping/core tests.
Permissions and queue lifecycle policy live in the session owner. Charm events
separate command acceptance, attempt outcomes, technical categories and recovery.
[The report](report.md) distinguishes implemented, verified and proposed work.

## Verified evidence

- Docker race suite; formatting/vet/lint (zero issues); hardened runtime
  build/smoke; rendered small/medium/large Compose profile checks.
- Real fixture media through FFmpeg/libopus/session/paced fake delivery;
  zero frames, cancellation, stalls, skip/completion races, fault injection,
  multiple guilds, cache persistence and resource cleanup.
- Full `make docker-bench`: concurrent 15-minute profiles at 4/12/24 guilds,
  no missed 60 ms deadlines, worker failures or retained temporary files.
  [Raw soak reports](capacity-soak-2026-10-08.json) record the image and limits;
  [performance](../performance.md) records workload/measurement caveats.
- A final two-second 4/12/24-guild harness run and microbenchmark gate passed
  after review. The final benchmark image disables irrelevant bot health probes.
- Actual Discord Gateway login and command reconciliation in four guilds.
  `/ready` reports Discord/commands true, source/pipeline unknown and no active
  delivery; no live audio command has yet been observed.
- Idle connected local-container SIGTERM completed in 1.377 seconds, exit 0,
  no OOM. The stopped checkpoint remains as `gobard-v2-test-e7309a5`.

Current local runtime image:
`sha256:1f22be9ee97caa798e98f4f677b9731be7c85ba1f148f800ebaf1e144a24932c`.
It uses a separate temporary test cache and is healthy after refresh.

## Remaining gates and risks

1. Real provider extraction, voice/DAVE delivery, audibility, live controls,
   reconnect and shutdown during active audio need a user-driven Discord test.
   The user has been invited to try `/play` and controls. Fake transport and
   successful login do not verify those capabilities.
2. The temporary SDK exposes no strict join for heartbeat/listener/internal
   event goroutines. GoBard joins its own work/callbacks, closes sockets and
   disables automatic SDK retries; native DAVE remains non-preemptible.
   These limitations are documented in [the adapter contract](../discord-adapter.md).
3. YouTube may reject authentication/automation externally. No personal cookies,
   auth bypass or unsupported fallback is required or implemented.
4. Spotify metadata/player control and public self-hosted sources remain
   assessed proposals; there is no independent Spotify audio backend.

The full production rewrite is **not declared complete** until live playback
gates are resolved and SDK limitations are accepted or addressed. No automatic
VPS rollout or image publication is authorized.

User changes to `AGENTS.md`, removal of `CLAUDE.md`, and the pre-existing
untracked `ERROR_4006_ANALYSIS.md` / `PARALLEL_COMMAND_REGISTRATION.md` remain
untouched and outside rewrite commits.
