# V2 implementation report — 2026-10-13

The runtime has been replaced and is available for local Discord testing. It is
not declared a completed production rewrite: the client acceptance pass remains,
and the pinned SDK lifecycle exception requires an explicit decision or library
fix. The latest listening rerun was reported successful; the earlier stutter's
cause remains unresolved.

## Implemented

- Modular Go composition; SDK-free session, command, playback, source and cache
  contracts; original bot/player/YouTube orchestration removed.
- All inventoried slash commands and buttons, voice/Manage Guild authorization,
  bounded queue/guild/command admission and retained disconnect selection.
- Owned attempts, cancellation and cleanup; exactly one authoritative terminal
  per attempt; zero-frame rejection; explicit source/codec/transport outcomes;
  bounded voice recovery, one classified transient source resume from delivered
  position, and stop/skip/reconnect race handling.
- Charm JSON/text events with stable command/request/attempt correlation and
  safe extractor classifications; bounded Discord HTTP responses, cancellation,
  mention suppression, Gateway membership observations and callback shutdown.
- Fixed-size startup/read/send timing summaries on attempt terminal events,
  to diagnose future live stutter without per-frame logs or a sample buffer.
- YouTube resolver/acquisition and stable-key media cache, bounded metadata,
  stderr, subprocesses, downloads and warming. No personal-cookie requirement.
- Existing hardened runtime, strict config, pinned dependencies, GHCR/Gitea and
  SOPS/age deployment compatibility retained; explicit migration/rollout docs.
- Restored `/play` track card and playback controls, current progress/next track,
  Queue/Refresh navigation and private errors that preserve shared player cards.
  One active player/queue message follows state and sampled progress automatically;
  stale mutation buttons are rejected privately. Deleted messages, cancellation
  and rate limits have finite update policies. See [the interface contract](../discord-interface.md).
- Durable local test start/status/stop/update helper, labeled named cache volume,
  readiness rollback and private configuration transfer. Production cache is untouched.
- Shared cancellable WebSocket guard, disabled public voice AutoReconnect,
  locked connection snapshots for manager shutdown and exact SDK source review.

## Verified

- Docker race tests, lint/vet/formatting, runtime build/smoke and rendered Compose
  profile gates. Fake interaction and voice fault injection cover contracts.
- Real local-media FFmpeg/Opus → session → paced fake transport slice, including
  cancellation, stalls, failures, zero frames and cleanup.
- Full 15-minute offline capacity gates at 4/12/24 guilds. No missed 60 ms
  deadlines, worker failures, temporary files or workload goroutine growth.
  See `capacity-soak-2026-10-08.json` and `../performance.md` for measurements.
- Actual local Discord login and command reconciliation in four guilds.
- User-confirmed audible `/play hope`; 2,233 accepted frames before a source
  connection reset, independently reproduced with the same diagnostic fingerprint.
  A separate cache download contains all 5,548 Opus frames (110.961 seconds).
  This is partial live playback evidence, not successful full-track completion.
- A later live source reset at 203.980 seconds triggered exactly one resume;
  the next attempt completed. Total accepted audio across both attempts was
  268.540 seconds. The user's listening report was qualified, so uninterrupted
  audible playback is not claimed. See [live evidence](live-2026-10-09.md).
- Deterministic source-resume tests cover offset/identity, one-retry budget,
  auth/unknown/empty/live exclusions, and stop/shutdown during the resumed attempt.
- Timing fault-injection tests, Docker race/lint/build/smoke gates and a short
  4/12/24-guild capacity regression passed after adding timing summaries. The
  synthetic overhead sample is recorded in `../performance.md`; live timing
  data for the reported stutter is unavailable retroactively.
- Idle connected-container SIGTERM: exit 0, no OOM, 1.377-second shutdown.
- User-confirmed successful October 12 rerun: one source reset at 180.540
  seconds, then a completed 88.000-second resume. No measured read/send call
  exceeded 60 ms; see [the live timing evidence](live-2026-10-12.md).
- October 13 Docker race tests, five helper safety tests, lint (zero issues),
  hardened build/smoke and all three Compose profile checks passed. New fake
  interaction tests exercise play, concurrent controls, changed permissions and
  stop; panel tests cover transitions, coalescing, deleted messages and rate limits.
- Final 30-second 4/12/24-guild regression: no gaps over 60 ms, worker failures
  or retained temporary files. This does not replace the earlier fifteen-minute
  soak or verify real Discord UI/DAVE. See [performance](../performance.md).
- The updated local bot logged in with commands ready. Its one completed test
  cache file was copied to the named volume and verified by SHA-256; the previous
  container exited zero without OOM. No production cache was mounted.
- The helper's subsequent idle stop/start returned the bot to healthy readiness;
  the completed media checksum still matched and the original `gobard-temp`
  remained stopped. This verifies idle lifecycle and cache persistence only.

## External gates and limitations

- Provider retrieval, audible Discord delivery, live source recovery and pipeline
  completion are confirmed for the observed requests, with a successful user
  report on the latest rerun. Channel controls and voice reconnect remain
  unverified. Completion does not establish lossless delivery to the listener,
  and the earlier stutter's cause remains unresolved.
- YouTube authentication/anti-bot rejection remains possible and is terminal
  when appropriate. A rewrite cannot guarantee provider availability.
- The current SDK exposes no strict heartbeat/listener join. Some voice heartbeat
  reconnect paths ignore the disabled AutoReconnect flag. GoBard cancels its
  sockets and joins its own work; native DAVE is non-preemptible. The exact
  [SDK lifecycle review](sdk-lifecycle-review.md) records containment and the
  unresolved exception. User listening tests cannot waive it.
- No VPS rollout, production-cache migration, credential rotation, image push
  or publisher change occurred. Local testing uses its separate labeled volume.

The remaining user-facing pass is [the acceptance checklist](acceptance-checklist.md).
Live client rendering/controls, voice reconnect, active shutdown and multiguild
listening are still unverified for this candidate. Provider availability remains
external; the SDK exception is an engineering decision rather than a listening test.

## Proposed only

Spotify metadata and official-player control remain separately assessed
capabilities; no independent Spotify audio source or browser rebroadcast exists.
Public self-hosted origins and an independent official-player wrapper remain
design proposals. The local media source is an operator-owned test fixture,
not a Discord-controlled filesystem or arbitrary-URL feature.
