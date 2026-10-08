# Observability

Use Charm Log with identical event/field semantics in text and JSON. Each
accepted request has a stable request ID; each attempt has a distinct attempt
ID. Session/guild IDs are operational correlation, not metric labels.

The attempt owner emits `playback.terminal` after cleanup with request_id,
attempt_id, command_id, stage, outcome, category, reason, recovery, frames and elapsed_ms. Completion
requires at least one accepted frame and successful source finalization. ERROR
means a terminal failure; INFO means completion or intentional cancellation.
Lower layers return wrapped causes without duplicate terminal logs.

The command owner separately emits `command.terminal` for accepted/rejected,
canceled or failed metadata/admission work. Its command ID is generated before
lookup and carried into every resulting request and attempt. An accepted
command event reports accepted track count; it never claims delivered audio.

Never log raw errors from external commands/SDKs blindly. Bounded stderr is
classified using allowlisted patterns (authentication, unavailable, throttled,
connection reset and network timeout);
unknown failures expose a fingerprint and exit status, never raw stderr, URLs,
headers, cookies or tokens. Original errors remain available through Unwrap.

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
This reset example reflects the first live audio test; the recovery event is a
contract example verified by deterministic tests, not yet a live observation.

Readiness distinguishes command acceptance/Discord connection from source
availability, pipeline status and active delivery. Idle is not a pipeline
failure; a successful gateway login is not evidence of working source retrieval.

`LOG_FORMAT=json` is the default; `text` changes rendering only. `/live` and
`/ready` include dimensions for Gateway connectivity, command registration,
last observed source and pipeline success/failure, and sampled active deliveries.
Source includes cache acquisition. Unknown is explicit until an audio attempt
provides evidence; a successful metadata lookup is not audio-source proof.
Delivery counts observe increasing accepted frames, not human audibility.
Observers sample at one second, and a slow failure notice can delay a sample by
up to eight additional seconds. Readiness requires initialized local tools/cache,
Gateway connection, voice adapter and reconciled commands; it never makes an
external provider probe a requirement for an idle process.
