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
  exhausted source failures advance; stop clears; intentional disconnect preserves queue.
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
attempt before declaring shutdown complete. First owner-accepted control reason
wins over later cancellation side effects; completion already observed by the
owner wins over a later control. Tests must exercise both orders.

## Implemented session controls

The session mailbox is 32 entries. Add admits a bounded batch atomically;
queue capacity is validated at construction (1–500). Display metadata has
per-field byte bounds. Snapshots copy the queue. Completed entries retain their
display positions until the queue drains, preserving the previous indexing.
Loop retries only successful completion. Skip/removal bypass loop; seek creates
a new attempt for the same request; disconnect/transport failure retain selection.
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
Finalization after stdout EOF is bounded to two seconds plus the subprocess
WaitDelay. FFmpeg receives a pipe with only the pipe protocol enabled.
Tests use a fake extractor executable and real FFmpeg. The v2 pipeline is wired
into production composition. A local live test confirmed partial YouTube audio
and human audibility, then exposed a source connection reset after 2,233 frames.
Full-track live completion and real recovery remain separate verification gates.
# Managed voice lifecycle

The guild coordinator serializes Connect/Disconnect after the preceding audio
attempt joins. Join has a 12-second budget; cleanup has a separate 3-second
budget. Stop during a join cancels it and rejects late success. Session shutdown
joins the transport operation and performs idempotent cleanup. A voice adapter
owns both the returned packet output and the underlying connection.

Metadata commands reserve a slot in their session before resolution. At most 16
reservations exist; queue-drain disconnect waits for them, including a failed
lookup. Stop/disconnect still invalidate the admission generation immediately.
Transport failure retains the selected request and permits at most three
reconnect attempts, with 0/1/2-second delays. Successful connection alone does
not reset this budget: replay failures consume it too. A completed/advanced
request resets the budget. Exhaustion retains the queue and disconnects.

Session Changes notifications have capacity one and coalesce. Inspect does not
produce another notification. These are UI hints, not terminal accounting;
Engine remains the authoritative attempt-result owner.
