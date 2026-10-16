# V2 implementation report — 2026-10-16

GoBard has a working replacement core and a validated local candidate for the
existing feature set. Strict completion remains blocked by dependency integration
and live acceptance. The current local bot has not yet been replaced by this
candidate. No VPS service, production cache, credentials or publisher changed.

## Implemented in GoBard

- Modular Go composition, SDK-free session/command/playback/source contracts,
  bounded admission, original orchestration removed, and adapter isolation tests.
- Inventoried commands and buttons, same-channel mutation authorization,
  Manage Guild configuration checks, queue/seek/loop policy and finite recovery.
- One authoritative terminal event per attempt after cleanup; zero-frame
  rejection; source/codec/transport classifications and stable correlation IDs.
  One classified transient source resume remains session-owned and bounded.
- An 8 MiB compressed acquisition ring separates network reads from paced
  playback. Preparation and first-packet startup have a 30-second budget;
  cancellation joins acquisition, feeder and subprocesses. Long/live media
  backpressures at the bound; extractor slots are released after process reap.
- One optional bounded cache transaction taps those same bytes. Duplicate warming
  downloads and the unused cache-creator API were removed. Cache failure does
  not fail audio; successful nonzero decoding is required before publication.
  Cache keys, leases, persistence and existing mounts are preserved.
- Charm events with safe diagnostics; no raw external stderr, tokens or signed
  URLs. One evolving shared player card, private receipts, inline failure/recovery
  guidance and finite edit retries keep channel noise low.
- Health observation is independent of stalled Discord HTTP edits. Observer-owned
  editors are bounded, canceled and joined. Local updates require a known idle
  observation; pause/preparation/reservations/cleanup cannot count as idle.
- Hardened containers, strict configuration, SOPS/age compatibility and existing
  Docker/GHCR/Gitea responsibilities remain intact. Local test state is isolated.

## Implemented separately, not integrated into the release

Four focused SDK repositories are committed under
`/home/lotus/Gitea/GoBard-SDK-Fixes`, with contracts and tests. They add terminal
Gateway/voice cancellation and joins, bounded reconnect policy, safe readiness
handoff and map cleanup, participant limits, explicit native DAVE cleanup and
frame-state synchronization. A disposable GoBard integration uses the new APIs.

The [SDK review](sdk-lifecycle-review.md) records exact revisions, native-source
inspection, test scope and exclusions. Main GoBard dependency pins are unchanged.
Publishing immutable dependency revisions requires authorization; local workspace
replacements are not a release solution. Native calls remain cooperative, and
optional unconsumed SDK helpers are outside the verified contract. Strict
completion cannot be cleared by listening tests alone.

## Executed verification

- Docker race suite, formatting/vet/lint, six local-helper safety tests and final
  hardened runtime build/smoke pass. Meaningful fault coverage includes full-ring
  cancellation, zero frames, source reset after delivered audio, startup/admission
  timeout, pause after preparation, cache saturation/leases/persistence, stalled
  panel edits, authorization and control/termination races.
- Two 15-minute capacity runs at 4/12/24 guilds exercise the real acquisition ring,
  FFmpeg/libopus and paced fake output. [Performance evidence](../performance.md)
  records image IDs, CPU/memory/timing measurements and workload limitations.
  These runs do not test provider networking or native Discord encryption.
- Real provider probes completed MOTTO and PANDEMONIUM without recovery, then
  replayed exactly 10,864 and 9,851 frames from cache with extraction disabled.
  Cold first frames were 2.026/1.817 seconds; warm first frames 73/142 ms.
  [The probe record](provider-2026-10-16.md) includes reproduction and limits.
  This is two source samples through fake output, not a full-album or p95 claim.
- Separate SDK race/vet checks and a disposable integrated GoBard race suite pass.
  Native tests exercise key-package creation, passthrough, user replacement and
  repeated cleanup. They do not prove established-MLS encryption performance or
  a native race/preemption guarantee.
- Earlier user-confirmed Discord playback and successful reruns remain historical
  evidence. Those requests included connection resets and one-time recovery;
  they do not establish uninterrupted playback on the new candidate. See the
  October [9](live-2026-10-09.md), [12](live-2026-10-12.md) and
  [15](live-2026-10-15.md) records.

## Outstanding acceptance and external blockers

1. Authorize the prepared separate dependency publication, pin immutable revisions,
   apply the tested adapter integration and rerun final GoBard quality gates.
2. Replace only the local test bot in an agreed quiet window, preserving its cache.
   The old image lacks the new busy-session field, so the helper deliberately
   refuses to infer safe idle from zero currently delivered audio.
3. Complete two isolated cold NF album passes and one warm pass, with truthful
   terminal accounting and no unexplained skipped tracks or audible gaps. Recovered
   playback must be recorded as an interruption, not uninterrupted success.
4. Execute the existing UI/control/authorization, real voice reconnect and active
   shutdown checks, plus 30 minutes of concurrent two-guild listening. Record exact
   image, commands, times and observed outcomes in the [checklist](acceptance-checklist.md).
5. Reconcile the evidence and SDK/native limitations before declaring strict
   completion. Unknown historical failures remain unknown; provider anti-bot,
   authentication or availability rejection remains external and cannot be
   guaranteed away by an internal rewrite.

The generated capacity fixture fits the ring. Longer/high-bitrate cold sources
can hold extraction slots under backpressure and reach admission deadlines;
no unlimited 24-guild cold-source claim is made. Startup percentiles and live
client rendering/audibility still need representative acceptance samples.

## Proposed only / outside this completion pass

No Muse feature-parity goal or new provider was added. Spotify metadata,
official-player control and independent delivery capabilities remain separately
assessed proposals. There is no Spotify audio backend, browser rebroadcast,
Discord-controlled local filesystem or arbitrary-URL feature. New conveniences
and persistent preference expansion follow completion of existing commitments.
