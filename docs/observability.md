# Observability

Use Charm Log with identical event/field semantics in text and JSON. Each
accepted request has a stable request ID; each attempt has a distinct attempt
ID. Session/guild IDs are operational correlation, not metric labels.

The attempt owner emits `playback.terminal` after cleanup with request_id,
attempt_id, command_id, stage, outcome, category, recovery, frames and elapsed_ms. Completion
requires at least one accepted frame and successful source finalization. ERROR
means a terminal failure; INFO means completion or intentional cancellation.
Lower layers return wrapped causes without duplicate terminal logs.

The command owner separately emits `command.terminal` for accepted/rejected,
canceled or failed metadata/admission work. Its command ID is generated before
lookup and carried into every resulting request and attempt. An accepted
command event reports accepted track count; it never claims delivered audio.

Never log raw errors from external commands/SDKs blindly. Bounded stderr is
classified using allowlisted patterns (authentication, unavailable, throttled);
unknown failures expose a fingerprint and exit status, never raw stderr, URLs,
headers, cookies or tokens. Original errors remain available through Unwrap.

Production examples (field contract, not evidence of a live run):

```
event=playback.terminal stage=source outcome=source_failure category=authentication recovery=none frames=0
event=playback.terminal stage=output outcome=transport_failure category=transport recovery=none frames=148
event=playback.terminal stage=read outcome=encode_failure category=zero_frames recovery=none frames=0
```

Readiness distinguishes command acceptance/Discord connection from source
availability, pipeline status and active delivery. Idle is not a pipeline
failure; a successful gateway login is not evidence of working source retrieval.
