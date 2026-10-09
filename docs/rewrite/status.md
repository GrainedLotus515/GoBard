# Rewrite status — 2026-10-09

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
- `6d7070f`: safe source-reset diagnostics and one bounded resume from delivered
  position; verified with a real source interruption on 2026-10-09.

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
  delivery was verified in this initial test.
- Safe connection-reset/timeout diagnostics and one session-owned resume from
  delivered position; deterministic tests cover budget, identity, exclusions,
  stop and shutdown. No authentication bypass or hidden extractor retry.
- A subsequent live request failed after 10,199 frames (203.980 seconds),
  resumed once at that offset, then completed another 3,228 frames. This verifies
  live source recovery and pipeline completion, not uninterrupted listening.
  The user reported stuttering/gaps while Cyberpunk was open; the cause of gaps
  beyond the source interruption remains unresolved. See [live evidence](live-2026-10-09.md).
- Idle connected local-container SIGTERM completed in 1.377 seconds, exit 0,
  no OOM. The stopped checkpoint remains as `gobard-v2-test-e7309a5`.

The first audible test image was
`sha256:1f22be9ee97caa798e98f4f677b9731be7c85ba1f148f800ebaf1e144a24932c`.
The successful live recovery used the source-recovery
image `sha256:25688d4eab4aa33a74fc67bddb632532c0e51c869a1647532887e43b0b8d21a1`,
with the same separate temporary test cache. Docker race tests and lint passed
after the recovery change, including real cached decoding at a nonzero offset;
the hardened runtime build and smoke check passed too. Live recovery subsequently
completed. Refreshing the idle connected bot took 1.264 seconds to stop, exit 0,
with no OOM; its stopped checkpoint remains `gobard-v2-test-a6b5155`.

The follow-up timing image is
`sha256:52f3a34e9c165e4e3602348ad72471b207d6782c919c3187da03379b369f46b4`.
It adds fixed-size first-frame/read/send timing summaries for the next live
test. Docker race tests, lint, runtime build/smoke, microbenchmarks and 30-second
4/12/24-guild capacity checks passed; no deadline gaps over 60 ms or worker
failures occurred. See [performance evidence](../performance.md). This instruments
future reports; it does not claim to fix the unresolved listening-quality issue.
The local bot is healthy with reconciled commands after refresh. The preceding
idle container stopped in 1.298 seconds, exit 0, no OOM, and is retained as
`gobard-v2-test-6d7070f`; the same temporary test cache is preserved.

## Remaining gates and risks

1. Listening quality, live controls, voice reconnect and shutdown during active
   audio still need user-driven Discord tests. Source resume and pipeline
   completion passed live; complete packets accepted by the transport do not
   establish uninterrupted human audibility or rule out client/network stutter.
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
