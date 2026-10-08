# V2 implementation report — 2026-10-08

The runtime has been replaced and is available for local Discord testing. It is
not declared a completed production rewrite: full-track live completion,
recovery, live controls and active SDK shutdown still require verification.

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
- YouTube resolver/acquisition and stable-key media cache, bounded metadata,
  stderr, subprocesses, downloads and warming. No personal-cookie requirement.
- Existing hardened runtime, strict config, pinned dependencies, GHCR/Gitea and
  SOPS/age deployment compatibility retained; explicit migration/rollout docs.

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
- Deterministic source-resume tests cover offset/identity, one-retry budget,
  auth/unknown/empty/live exclusions, and stop/shutdown during the resumed attempt.
- Idle connected-container SIGTERM: exit 0, no OOM, 1.377-second shutdown.

## External gates and limitations

- Partial provider retrieval and audible Discord delivery are confirmed. Live
  full-track completion, source recovery, channel controls and reconnect remain
  unverified. Deterministic fake output does not replace those gates.
- YouTube authentication/anti-bot rejection remains possible and is terminal
  when appropriate. A rewrite cannot guarantee provider availability.
- The current SDK exposes no strict heartbeat/listener join and has internal
  asynchronous event work. GoBard joins its own work and callbacks, cancels
  sockets and disables SDK automatic retries; native DAVE is non-preemptible.
  The future independent Discord library must close these contract gaps.
- No VPS rollout, production-cache migration, credential rotation, image push
  or publisher change occurred. Local testing uses a separate temporary cache.

## Proposed only

Spotify metadata and official-player control remain separately assessed
capabilities; no independent Spotify audio source or browser rebroadcast exists.
Public self-hosted origins and an independent official-player wrapper remain
design proposals. The local media source is an operator-owned test fixture,
not a Discord-controlled filesystem or arbitrary-URL feature.
