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
- User-confirmed audible `/play hope` on the local bot. Streaming failed after
  2,233 accepted frames with a connection reset; an independent paced probe
  reproduced the same fingerprint (`ff00ab2b92eaf6f7`) and errno 104. A separate
  cache download contains 5,548 Opus frames over 110.961 seconds. Partial live
  delivery is verified; successful full-track live completion is not.
- Safe connection-reset/timeout diagnostics and one session-owned resume from
  delivered position; deterministic tests cover budget, identity, exclusions,
  stop and shutdown. No authentication bypass or hidden extractor retry.
- Idle connected local-container SIGTERM completed in 1.377 seconds, exit 0,
  no OOM. The stopped checkpoint remains as `gobard-v2-test-e7309a5`.

The first audible test image was
`sha256:1f22be9ee97caa798e98f4f677b9731be7c85ba1f148f800ebaf1e144a24932c`.
The local bot now runs the source-recovery
image `sha256:25688d4eab4aa33a74fc67bddb632532c0e51c869a1647532887e43b0b8d21a1`,
with the same separate temporary test cache. Docker race tests and lint passed
after the recovery change, including real cached decoding at a nonzero offset;
the hardened runtime build and smoke check passed too. Live recovery remains
unverified. Refreshing the idle connected bot took 1.264 seconds to stop, exit 0,
with no OOM; its stopped checkpoint remains `gobard-v2-test-a6b5155`.

## Remaining gates and risks

1. Full-track live completion, source resume, live controls, reconnect and
   shutdown during active audio still need user-driven Discord tests. Partial
   extraction and audible voice delivery passed; the connection-reset failure
   was recorded truthfully. Offline recovery tests do not verify live recovery.
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
