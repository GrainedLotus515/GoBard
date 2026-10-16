# Existing commitments — implementation checkpoints

Approved scope (2026-10-16): playback reliability, existing controls and truthful
feedback, bounded ownership, focused fixes to the temporary Discord libraries,
and recorded offline/live acceptance. No additional product features or providers.
Brief preparation is acceptable. The SDK lifecycle blocker is not waived.

## Acquisition decision

Replace playback-paced extractor consumption with an attempt-owned 8 MiB
compressed-byte buffer. One acquisition worker drains the extractor; one feeder
writes buffered bytes to the decoder's OS pipe. Both are canceled and joined by
the attempt. No mutex spans subprocess, pipe or disk I/O. Saturation backpressures
without dropping bytes, including long/live media. Preparation waits for 256 KiB,
successful source completion, or two seconds after the first bytes. A 30-second
startup budget includes process admission and first decoded audio.

The process slot is released after the extractor is reaped, rather than after
voice playback finishes. One optional cache transaction taps the same bytes;
there is no second warming extraction. Cache-write admission is nonblocking,
process-wide per cache, with one writer and at most min(cache size, 256 MiB)
scratch bytes. Cache errors disable the optional write without failing audio.
Publication also requires successful nonzero decoding; the writer reservation
can therefore last through playback. Skip or decoder failure aborts it. This
deliberately favors validated media and bounded scratch over concurrent fills.
Live streams never publish.
Existing committed cache keys, leases and deployment mounts are preserved.

The byte buffer protects network acquisition from the paced decoder; the cache
transaction protects atomic publication and the independent scratch budget.
Both are concrete internal types, not new provider frameworks. Existing core
Opener/Stream/Output contracts remain intact.

## Gates

- Acquisition/cancellation/cache fault tests and authoritative terminal outcomes.
- Existing control, authorization, observer and health contract checks.
- Separate SDK patches with cancellation/join and bounded native cleanup proof.
- Docker race, lint, hardened smoke and 15-minute capacity gates.
- Three playlist passes (two isolated cold caches, one warm), existing live
  controls/reconnect/shutdown checklist, and 30-minute two-guild listening.

No passing fake transport test substitutes for listening. Historical unknown
failures stay labeled unknown until new evidence identifies them. No VPS change,
production cache migration, dependency publication or image push is authorized.
Results and outstanding external gates are maintained in status.md.

## Acquisition checkpoint — 2026-10-16

Implemented the ring, preparation deadline, first-packet priming, worker joins,
early extractor-slot release and same-stream cache transaction. Removed the old
warming/download implementation. Docker race tests and Docker formatting/vet/lint
passed after the change. Regression coverage includes cache reuse and leases,
failed/live/invalid media, scratch saturation and byte exhaustion, first-audio
deadline including process admission, pause after preparation, a full ring during
cancellation, and a source reset after accepted audio. No new listening or
provider-availability claim is made by those deterministic fixtures.

Separate SDK workspaces were initialized from the exact pinned module sources
under `/tmp/gobard-sdk-fixes`. These are development checkpoints, not published
dependencies; GoBard's dependency pins remain unchanged and strict completion is
still blocked. Capacity measurements must be rerun with acquisition included.

## Health and local update checkpoint — 2026-10-16

The observer now owns a bounded, canceled/joined editor child. A stalled HTTP
request cannot delay health sampling; session removal joins the editor. Added
busy-session observations covering preparation, pause, metadata reservations and
transport work. The local update helper fails closed on missing/unknown activity.
Docker race tests, helper tests and lint passed. The acquisition candidate also
passed the hardened runtime smoke gate. It has not replaced the running test bot.
