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
still blocked. Subsequent acquisition-backed capacity measurements are below.

## Health and local update checkpoint — 2026-10-16

The observer now owns a bounded, canceled/joined editor child. A stalled HTTP
request cannot delay health sampling; session removal joins the editor. Added
busy-session observations covering preparation, pause, metadata reservations and
transport work. The local update helper fails closed on missing/unknown activity.
Docker race tests, helper tests and lint passed. The acquisition candidate also
passed the hardened runtime smoke gate. It has not replaced the running test bot.

## Capacity checkpoint — 2026-10-16

The 4/12/24-guild 15-minute profiles now exercise the acquisition ring, real
decoding/encoding and paced fake output. All passed, with zero missed 60 ms
deadlines, worker failures or retained temporary files. Raw measurements and
the increased memory footprint are recorded in performance.md. The generated
input fits the ring; long high-bitrate source admission remains a separate
constraint. Docker race/helper tests and lint passed for the new workload.
These gates do not verify provider networking, native DAVE or human audibility.

## Simplification checkpoint — 2026-10-16

Removed the unused pathname-creator/single-flight cache API and its separate
creator-admission state. Production and benchmark cache writes now use the same
single-writer transaction; tests retain persistence, lease incarnation, eviction,
unsafe-key, symlink, byte-budget, cancellation and abort coverage. Docker race
and lint gates pass. Repeated 15-minute capacity validation passed at 4/12/24
guilds with zero missed 60 ms deadlines, worker failures or temporary files;
see capacity-transactions-2026-10-16.json. Final runtime smoke and rendered Compose
profile validation passed. The canceled/closing activity guard was committed as
`697128d` and the opt-in real-provider regression/evidence as `039e738`.

## External source and SDK checkpoints — 2026-10-16

MOTTO and PANDEMONIUM completed the real provider/acquisition/codec path and
replayed identical full frame counts from cache with extraction disabled. The
paced output was fake; this is not Discord listening evidence. Reproduction and
startup observations are in provider-2026-10-16.md.

Four separate SDK repositories are preserved durably under
`/home/lotus/Gitea/GoBard-SDK-Fixes`; exact commits, tests and limitations are in
sdk-lifecycle-review.md. Combined SDK/native/disposable-GoBard race tests and vet
pass. Main pins and the running bot remain unchanged pending publication and
quiet local replacement permission. Full album, controls, reconnect, active
shutdown and multiguild listening gates remain open; the strict blocker is not
waived. No new product capabilities or provider bypasses were introduced.
