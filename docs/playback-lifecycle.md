# Playback lifecycle

Reference contracts for the replacement:

* Request: submitted → rejected OR accepted/queued → selected → terminal.
  Admission has a bounded mailbox and queue. A caller cancellation before
  admission rejects; after acceptance it does not silently roll back a command.
* Attempt: opening → streaming → closing → terminal. Source-open, frame-read,
  transport-send and close failures remain distinguishable. Exactly one result
  and one terminal event are published after cleanup.
* Session: idle → active ↔ paused → idle/disconnected → closed. Only the owner
  mutates selection. Skip/removal bypass loop; completion may repeat; source
  exhausted source failures advance; stop clears; disconnect retains safely resumable queue state.
* Transport: disconnected → connecting → ready → failed/closing → disconnected.
  No attempt success implies human audibility; delivered frames mean output
  accepted complete packets.

Outcomes: completed, skipped, stopped, seeked, removed, disconnected, shutdown,
source_failure, encode_failure, transport_failure. Failure classification,
wrapped cause and recovery decision are separate from outcome. EOF with no
accepted audio is failure, even if subprocesses exited successfully. Process
nonzero exit after some audio remains failure with a nonzero delivered count.

The attempt has no retries. The session is the only recovery owner. Provider
auth/rejection is terminal; it must not trigger cookies, provider switching or
loop retries. Transport reconnect policy requires a finite attempt/time budget.

A classified connection reset or network timeout after accepted audio permits
one source resume per logical request. The track must have a known finite
duration and the delivered position must precede its end. The coordinator joins
the failed attempt, advances the offset by accepted 20 ms frames, and starts a
distinct attempt with the same request ID. The existing opener prefers a complete
cache entry when available; otherwise it reacquires the same source. Authentication,
throttling, unknown failures, zero frames and live media do not qualify. A second
failure exhausts the budget and advances the queue. Stop/skip/shutdown override
recovery; seek and looping do not replenish this per-request source budget.

Cancellation is cooperative and propagated through every boundary. Output Send
must release the packet before returning and unblock on context cancellation.
Close stops/reaps any owned processes before returning. The session joins its
attempt before declaring shutdown complete. An established source/codec/transport
failure or natural completion is not overwritten by a later cancellation; the
requested control outcome still applies when cancellation caused the return.
Stop decides whether to retain voice from the actual terminal result and transport
cleanup state, so a failure during Stop disconnects broken transport instead of
being reported as a healthy stopped attempt. Completion already observed by the
owner wins over a later control. Tests exercise both orders and blocked cleanup.

## Implemented session controls

The session mailbox is 32 entries. Add admits a bounded batch atomically;
queue capacity is validated at construction (1–500). Display metadata has
per-field byte bounds. Snapshots copy the queue. Completed entries retain their
display positions until the queue drains, preserving the previous indexing.
Loop retries only successful completion. Skip/removal bypass loop; seek creates
a new attempt for the same request; disconnect/transport failure retain finite
seekable selection at its delivered position. Live/unknown-duration media ends
on interruption and advances, because safe seeking cannot be established.
The managed voice path supplies a new output through the finite reconnect policy
below. The fixture constructor requires an explicitly supplied output.

Pause uses a cancellation-aware notification gate, with at most one encoded
packet held. Session owns gain/ducking policy; codecs read an atomic gain value.
Speaking membership is capped at 256. Controls during a pending transition
receive busy, except stop and inspection; stop supersedes queue retention and
clears admitted requests after the attempt joins. A request Ticket settles once;
loop, seek and disconnect can generate several distinct attempts before then.

Play-command resolution is separately bounded (constructor limit 1–16 workers,
no pending queue). Each admitted lookup receives a command ID carried by every
resulting request/attempt. Stop/disconnect advance a session generation; a late
lookup cannot add tracks into a later generation, even if already reconnected.
Voice membership is checked again after lookup and compared to the owned channel
at admission. Closing the command service cancels and joins all accepted work
before sessions and sources are closed.

Source acquisition now validates canonical YouTube input before launching
yt-dlp, disables hidden config/retries, retains 16 KiB of stderr, kills the
extractor process group on cancellation and waits before returning capacity.
Each uncached attempt owns an 8 MiB compressed-byte ring, one acquisition worker
and one decoder feeder. Preparation waits for 256 KiB, successful source EOF or
two seconds after the first bytes. The 30-second startup budget includes process
admission, preparation and the first encoded packet. Open primes that packet and
ends the startup timer before returning, so a subsequent user pause cannot cause
a false startup timeout. A full ring backpressures acquisition without dropping
bytes. Long/live inputs can retain an extractor slot until playback consumes more;
short inputs release the slot as soon as the extractor is reaped.

One optional cache transaction consumes the same acquisition bytes. It never
launches a second extractor. Its process-wide single-writer reservation and
min(cache size, 256 MiB) scratch limit are independent of the committed cache
budget. Cache admission/write failure does not fail playback. Publication requires
source success and complete nonzero decoding; skip, cancellation, decode failure
or live media cannot publish. The reservation may therefore remain held through
playback. Cache leases remain owned until stream Close, including seeked replay.
Cancellation wakes both ring directions, interrupts I/O, reaps processes and joins
workers before returning. There is no background warming owner.

Finalization after stdout EOF is bounded to two seconds plus the subprocess
WaitDelay. FFmpeg receives a pipe with only the pipe protocol enabled.
Tests use a fake extractor executable and real FFmpeg. The v2 pipeline is wired
into production composition. A local live test confirmed partial YouTube audio
and human audibility, then exposed a source connection reset after 2,233 frames.
A subsequent live request resumed once after a reset at 203.980 seconds and
completed the remaining audio. The [October 12 rerun](rewrite/live-2026-10-12.md)
was reported successful and again exercised source recovery. Earlier stuttering
remains unexplained; accepted packets and local timings do not prove gapless
reception at the listener.

The [October 16 source probes](rewrite/provider-2026-10-16.md) completed MOTTO
and PANDEMONIUM through the new acquisition path without recovery, then replayed
all frames from cache. They used paced fake output, not Discord voice. Full album
listening and live reconnect/active-shutdown remain separate acceptance gates.
# Managed voice lifecycle

The guild coordinator serializes Connect/Disconnect after the preceding audio
attempt joins. Join has a 12-second budget; cleanup has a separate 3-second
budget. Stop keeps a healthy established voice connection but cancels an
in-progress join/recovery, rejects late success and cleans broken transport.
Disconnect always leaves voice. Session shutdown joins the transport operation
and performs idempotent cleanup. A voice adapter owns both the returned packet
output and the underlying connection.

Metadata commands reserve a slot in their session before resolution. At most 16
reservations exist; queue-drain disconnect waits for them, including a failed
lookup. Stop/disconnect still invalidate the admission generation immediately.
Reservations retain their admitting generation; superseded or duplicate lookup
cleanup cannot disconnect the healthy voice connection retained by Stop. Stop
also clears any earlier natural-drain intent held open by those reservations.
Transport failure retains a finite seekable request and permits at most three
reconnect attempts, with 0/1/2-second delays. Successful connection alone does
not reset this budget: repeated partial failures consume it too. A completed/advanced
request resets the budget. Exhaustion retains the queue and disconnects.

After the failed attempt joins, its accepted frame count advances that attempt's
offset by 20 ms per frame, including after a nonzero seek. Request identity is
retained; every resume gets a distinct attempt identity and terminal event.
Intentional disconnect uses the same position accounting but never automatically
reconnects. At or beyond declared duration, settle the interruption outcome and
advance even if repeat is enabled; do not pretend an interrupted source completed.
Live/unknown-duration tracks also settle and advance instead of replaying.
Stop supersedes pending recovery; other racing controls retain the first accepted
transition. Delivered means transport acceptance, not human audibility.

The output media clock uses successful Write completion time. Ordinary timer
jitter preserves phase; a delayed successful write or long pause rebases the
next packet so it cannot immediately catch up in a burst. Native write deadlines,
cancellation and the concurrent Close join barrier remain unchanged.

Session Changes notifications have capacity one and coalesce. Inspect does not
produce another notification. These are UI hints, not terminal accounting;
Engine remains the authoritative attempt-result owner.

Control cancellation returned as the attempt's explicit cause (including a
wrapped cause) is recognized during open, read, send and cleanup. Unrelated
errors remain failures even if they race cancellation. The extractor's signal
exit after owned cancellation is expected teardown, while an observed nonzero
normal exit, acquisition read error or finalization deadline remains a source
failure. Close joins the extractor and releases scratch/cache/process resources
before the one authoritative terminal result is emitted.
