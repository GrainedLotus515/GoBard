# Observability

Use Charm Log with identical event/field semantics in text and JSON. Each
accepted request has a stable request ID; each attempt has a distinct attempt
ID. Session/guild IDs are operational correlation, not metric labels.

The attempt owner emits `playback.terminal` after cleanup with request_id,
attempt_id, command_id, stage, outcome, category, reason, recovery, frames and elapsed_ms. Completion
requires at least one accepted frame and successful source finalization. ERROR
means a terminal failure; INFO means completion or intentional cancellation.
Lower layers return wrapped causes without duplicate terminal logs.

`track_id` is the optional public provider identifier, distinct from the private
acquisition key (`MediaID`). YouTube metadata supplies its validated video ID;
queue admission preserves it across attempts. Playback emits only nonempty IDs
of at most 32 ASCII letters, digits, hyphens or underscores. Other sources must
omit this field unless they can guarantee a public identifier. Acquisition URLs,
paths, titles and requester data are never substituted into this field.

Terminal events also include `first_frame_ms`, `max_read_ms`, `max_send_ms`,
`slow_reads` and `slow_sends`. First-frame latency includes source opening and
startup. Read/send maxima and counts measure only subsequent accepted packets:
read includes source acquisition plus decoding/encoding; send includes pacing
plus transport/encryption. A slow call exceeds 60 ms (three audio frames).
Explicit pause gates, failed calls and EOF finalization are excluded. Zero
first-frame latency with zero frames means no audio was accepted. Small positive
durations can round to zero milliseconds.

The attempt owner keeps fixed-size maxima/counters, with no packet history,
background worker or per-frame logs. These measurements can locate local stalls
but cannot identify their underlying CPU/network cause, measure remote packet
loss or prove smooth listening. They do not directly measure inter-packet gaps:
several smaller delays or scheduling outside these calls can still cause jitter.

The command owner separately emits `command.terminal` for accepted/rejected,
canceled or failed metadata/admission work. Its command ID is generated before
lookup and carried into every resulting request and attempt. An accepted
command event reports accepted track count; it never claims delivered audio.

Never log raw errors from external commands/SDKs blindly. Bounded stderr is
classified using allowlisted patterns (authentication, unavailable, throttled,
connection reset, network timeout, HTTP 403 and unavailable requested format);
unknown failures expose a fingerprint and exit status, never raw stderr, URLs,
headers, cookies or tokens. Original errors remain available through Unwrap.
HTTP 403 remains category `source`, reason `http_forbidden`: the status alone
does not prove CAPTCHA, a missing account or an expired signed URL. Missing
formats use `format_unavailable`. Neither reason permits automatic recovery;
authentication/unavailable/throttled classification takes precedence. Discord
shows fixed guidance in the existing card, with no extra channel message.

`startup_timeout` identifies expiry of the 30-second source-opening budget,
including process admission and first encoded packet. It does not qualify for
the session's source-resume policy. Optional cache failures produce bounded,
sanitized `cache.fill` warnings without changing a successful audio outcome;
they are not a second playback terminal event. Cache writes share acquisition
bytes rather than generating separate download/retry logs.

Production examples (field contract, not evidence of a live run):

```
event=playback.terminal stage=source outcome=source_failure category=authentication recovery=none frames=0
event=playback.terminal stage=output outcome=transport_failure category=transport recovery=none frames=148
event=playback.terminal stage=read outcome=encode_failure category=zero_frames recovery=none frames=0
event=playback.terminal stage=acquire outcome=source_failure category=source reason=connection_reset recovery=none frames=2233
event=session.recovery recovery=source_resume reason=connection_reset offset_ms=44660 attempts=1
```

The terminal event describes the finished attempt, whose engine performs no
retry. A separate session-owned `session.recovery` WARN records the policy
decision and links the failed request/attempt IDs. It does not replace or turn
the failed attempt into a success. The resumed attempt gets a new attempt ID.
This reset example reflects the first live audio test; the recovery line at that
offset is illustrative. A subsequent real `session.recovery` event at offset
203980 ms was followed by a completed attempt; see
[the live evidence](rewrite/live-2026-10-09.md).

Readiness distinguishes command acceptance/Discord connection from source
availability, pipeline status and active delivery. Idle is not a pipeline
failure; a successful gateway login is not evidence of working source retrieval.

`LOG_FORMAT=json` is the default; `text` changes rendering only. `/live` and
`/ready` include dimensions for Gateway connectivity, command registration,
last observed source and pipeline success/failure, and sampled active deliveries.
Source includes cache acquisition. Unknown is explicit until an audio attempt
provides evidence; a successful metadata lookup is not audio-source proof.
Delivery counts observe increasing accepted frames, not human audibility.
Observers sample at one second independently of the player editor. Delivery is
a sampled indicator, not an instantaneous count. `busy_sessions` separately
observes outstanding playback (including pause/preparation), metadata reservations
and transport work. It is -1 if inspection fails. It is not an admission lock:
new commands can arrive after a probe. Readiness requires initialized local tools/cache,
Gateway connection, voice adapter and reconciled commands; it never makes an
external provider probe a requirement for an idle process. Failed registration
passes keep readiness false and emit `discord.commands` with the scheduled retry
delay; later bounded reconciliation can restore readiness without a restart.

`capacity.admission` is emitted once at startup with the memory source, limit,
headroom, per-guild reservation, configured upper bound and effective guild
limit. It contains no host identifiers or process environment values.

`discord.panel` WARN reports automatic player-edit failures with safe HTTP status,
failure count, retry delay and whether automatic updates remain enabled. No
message content, webhook token or response body is logged. Deleted/forbidden
messages and exhausted retries disable edits until another interaction establishes
a view; playback continues independently. Progress edits generate no INFO events.

Discord-facing feedback is separate from production logs: one evolving shared
card carries playback state, recovery and failure guidance, while command receipts
and rejections are private. The observer does not post separate failure messages.
It may coalesce intermediate states; authoritative terminal accounting remains in
Charm logs. See [the interface contract](discord-interface.md).
