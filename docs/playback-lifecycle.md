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
  failures advance; stop clears; intentional disconnect preserves queue.
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

Cancellation is cooperative and propagated through every boundary. Output Send
must release the packet before returning and unblock on context cancellation.
Close stops/reaps any owned processes before returning. The session joins its
attempt before declaring shutdown complete. First owner-accepted control reason
wins over later cancellation side effects; completion already observed by the
owner wins over a later control. Tests must exercise both orders.
