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
* Phases 3–6: runtime migration, provider assessment, complete adapter contracts,
  measurements and final review remain.

Evidence: baseline and slice `make docker-test` passed (race-enabled).
`make docker-lint` passed with zero issues after explicit cancellation ownership
and bounded fixture conversion annotations. Docker socket access
required escalation. No live Discord/YouTube integration has been exercised.
User-reported YouTube anti-bot rejection remains an external risk. Native
libdave is not assumed. Production service/cache/secrets are untouched.
