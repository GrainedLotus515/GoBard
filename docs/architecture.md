# Architecture

Decision 1 (2026-10-07): modular monolith; retain the tested deployment and URL
policy while replacing orchestration. Existing internals are reference material.

Dependency direction: composition → Discord adapter → guild session → playback
contracts. Source and audio implementations satisfy playback-consumed interfaces.
Cache/config/diagnostics are independent of Discord. SDK types must never reach
session, queue, source or playback packages.

| Component | Owner and responsibility |
| --- | --- |
| Composition | Root context, config, Charm logger, budgets, adapters, shutdown join. |
| Discord adapter | SDK interaction parsing, responses, voice membership, gateway events, command registration. |
| Session | One event loop owns queue, selected request, control transitions and attempt policy; commands have explicit acknowledgement. |
| Playback | One synchronous attempt owns open → read → send → cleanup → terminal result. It never advances queues or reconnects. |
| Source | Metadata resolution and authorized acquisition. No implicit fallback to another provider. |
| Audio | Decode/encode to stereo 48 kHz Opus, 20 ms packets, fixed buffers; process wait result is part of success. |
| Voice adapter | Connection, DAVE/encryption, packet pacing and synchronous transport acceptance; cancellation and disconnect. |
| Cache | Byte/entry budgets, stable keys, leases and atomic publication. No ownership migration of persisted files. |
| Observability | Charm events emitted at operation owners; allowlisted diagnostics; independent health dimensions. |

Decision 2: the reference slice uses synchronous pull and synchronous output. A
single reusable packet and PCM frame provide natural backpressure without a
frame producer goroutine or buffer pool. Introduce a bounded prefetch ring only
if measurements justify it. Interfaces exist only for media opening/frames and
voice delivery, which need actual production implementations and deterministic
fault-injection doubles.

Decision 3: no speculative second Discord implementation, public local-file
command, Spotify capture or generic provider-fallback framework. The current
SDK remains in adapters until its independent replacement exists.

Implementation status and temporary migration boundaries are in
[rewrite/status.md](rewrite/status.md); this document distinguishes intended
architecture from the old runtime still being migrated.

Current replacement packages are `internal/session` (queue/controls),
`internal/playback` (attempts and bounded control data), `internal/audio`
(FFmpeg/libopus), `internal/source` (YouTube acquisition), and `internal/failure`
(safe classifications). The Discord output implementation remains in
`internal/discordvoice`. An AST-based test in `internal/architecture` enforces
the SDK import boundary over internal packages and commands.

Review decision (2026-10-08): queue admission clones bounded metadata strings,
because a short Go substring can otherwise retain an arbitrarily large backing
allocation. Session IDs include a random incarnation to avoid reusing request
IDs after a guild session is recreated. Source failure classification is shared
across open, read and finalization, rather than treating all close errors as
codec errors. These are ownership/correctness changes, not performance claims.
