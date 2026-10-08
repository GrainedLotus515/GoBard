# Rewrite status

Branch: `v2-rewrite`. Baseline: `44a47e8`. This is an active rewrite, not a
completed v2 release.

* Phase 0: commands, permissions, queue, source, cache, voice, configuration and
  deployment inventoried; historical Spotify metadata bridge identified.
* Phase 1: initial ownership, lifecycle, capability and resource decisions
  documented before implementation.
* Phase 2: next checkpoint is the offline real-media playback slice with
  authoritative outcomes, safe diagnostics, bounded buffers and cancellation.
* Phases 3–6: runtime migration, provider assessment, complete adapter contracts,
  measurements and final review remain.

Evidence: baseline Docker race test is running; sandbox Docker socket access
required escalation. No live Discord/YouTube integration has been exercised.
User-reported YouTube anti-bot rejection remains an external risk. Native
libdave is not assumed. Production service/cache/secrets are untouched.
