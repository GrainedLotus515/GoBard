# Rewrite status

Branch: `v2-rewrite`. Baseline: `44a47e8`. This is an active rewrite, not a
completed v2 release.

* Phase 0: commands, permissions, queue, source, cache, voice, configuration and
  deployment inventoried; historical Spotify metadata bridge identified.
* Phase 1: initial ownership, lifecycle, capability and resource decisions
  documented before implementation.
* Phase 2: real generated WAV → FFmpeg PCM → libopus → fake voice Opus decoder
  passes through session admission, queue, attempt, output and cleanup. Tests
  cover nonzero decoded audio, zero frames, output backpressure, queue rejection,
  stop and repeated close. Fault tests cover source authentication, codec,
  transport and cleanup failures, one terminal event and safe diagnostics.
  Repeated skip/completion races, canceled admission/wait and 16 independent
  sessions also pass. Synthetic and real FFmpeg/Opus benchmarks are recorded in
  performance.md. The slice is not yet wired to the production Discord runtime.
* Phase 3 underway: session controls implement loop/skip/removal, move/clear,
  seek, pause/resume, volume/ducking, retained disconnect and explicit reconnect.
  New source acquisition and Discord packet adapters have deterministic tests.
  Spotify/YouTube technical and terms assessment is recorded in providers.md.
* Additional Phase 3 work: shared metadata values, bounded metadata subprocess
  output, canonical URL policy reuse, streaming download byte limits, cache
  leases/warming ownership and bounded guild registry are implemented and tested.
  They are replacement components, not yet the production composition.
* Phases 3–6 remaining: production command/composition migration,
  complete interaction/reconnect
  contracts, health dimensions, production logger wiring, longer soak tests and
  final removal of old implementations. Do not deploy this as a completed v2.

Evidence: baseline and slice `make docker-test` passed (race-enabled).
`make docker-lint` passed with zero issues after explicit cancellation ownership
and bounded fixture conversion annotations. Docker socket access
required escalation. No live Discord/YouTube integration has been exercised.
User-reported YouTube anti-bot rejection remains an external risk. Native
libdave is not assumed. Production service/cache/secrets are untouched.

2026-10-08 checkpoint evidence: Docker runtime build and `make docker-smoke`
passed (read-only runtime, UID/GID/tool/linked-libdave checks; no bot start).
The new source tests include nonzero extractor exit after real audio delivery,
not only empty/auth failures. The packet adapter is tested against a fake
deadline-capable transport. Neither result establishes real Discord connectivity.

Cache storage review: filesystem mutation now occurs outside the shared metadata
lock; the index admits at most 65,536 entries and creation at most 16 distinct
keys concurrently. Directory scans use 256-entry batches. Duplicate waiters can
cancel, symlink publication is rejected, and leases identify file incarnations
so an old lease cannot unpin a replacement. Docker race and lint gates passed.
Existing cache names and completed media are preserved; the actual persisted
cache has not been opened or modified during development.

Command admission checkpoint: bounded synchronous metadata workers, joined
shutdown, stable command-to-attempt correlation, owner-checked voice/Manage Guild
authorization and stop/disconnect generations are implemented. Deterministic
tests cover saturation, shutdown, late lookup after reconnect, voice moves during
lookup, safe auth diagnostics, and correlation reaching the actual attempt.
The production Discord command adapter has not yet adopted these contracts.
The replacement interaction mapper covers every existing command/button and has
malformed-input fixtures plus a JSON fuzz target. Mapping-to-real-session tests
verify voice/Manage Guild enforcement. Toggle-pause and forward-seek operations
now execute atomically against coordinator-owned state.

Performance checkpoint: the capacity tool now uses actual v2 sessions,
FFmpeg/libopus (explicit 128 kbit/s), and the Discord pacing adapter over a fake
packet writer. Independent 30-second runs at 1/4/12/24 guilds passed their quota,
deadline and cleanup checks. Raw results and workload limits are in
`capacity-2026-10-08.json` and `../performance.md`. The review corrected cumulative
timer drift, bounded decoder/filter threads and separated anonymous memory from
total cgroup memory. These runs do not verify DAVE, network delivery, provider
capacity, gapless track changes, or the default 15-minute soak.
Final checkpoint gates passed: Docker race tests, Docker lint (zero issues),
runtime build/tool-permission smoke check, and Docker microbenchmarks. The latest
microbenchmark samples and their comparison limits are recorded explicitly.

## Remaining cutover work

1. Verify local Discord login, registration, voice/DAVE delivery, user commands
   and bounded recovery against the real temporary SDK. Credentials/connectivity
   have not yet been exercised by the new runtime.
2. Audit remaining SDK limitations: its own heartbeat/listener goroutines have
   no public join API; native DAVE operations are non-preemptible. The adapter
   joins GoBard callbacks/work and cancels sockets, but a strict SDK-worker join
   is not proven. SDK-maintained guild/voice-state storage also needs a measured
   admission envelope distinct from GoBard's bounded session registry.
3. Complete the longer soak, review provider evidence and remove stale docs.
   Spotify metadata/official-player designs remain proposals, not audio support.

Runtime cutover checkpoint: `cmd/gobard` now composes v2 sources, sessions,
commands, HTTP/Gateway and voice adapters through `internal/app`. Original
bot/player/YouTube orchestration, obsolete implementation tests, dormant Spotify
stub, separate logger and global process limiter are removed. Behavior coverage
now lives in mapper/adapter, command, session, source, sourceurl and cache tests.
The old runtime remains available in git history, not in the executable.

Verified after cutover: `make docker-test` (race), `make docker-lint` (zero
issues), and `make docker-smoke` including runtime build passed. New contracts
cover HTTP cancellation/safe 429 rejection, interaction capacity/control
reservation, mentions, queue pagination, paused voice loss, callback admission
and joined runtime shutdown. Live Discord/provider gates remain separate.

The full rewrite is not complete and must not be deployed as v2 yet. No external
gate prevents continuing the independent cutover work above.
