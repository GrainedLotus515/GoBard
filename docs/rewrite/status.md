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
  metadata-worker bounds, cache storage lock/index review, complete interaction/reconnect
  contracts, health dimensions, production logger wiring, load measurements and
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
