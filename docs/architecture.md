# Architecture

Product constraint confirmed 2026-10-15: playback and controls stay in Discord.
The delivery target remains Discord voice; an external official-player page is
not a substitute. Source/provider alternatives must satisfy actual audio delivery
and provider authorization, independently of metadata or player-control APIs.

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
| Command | Bounded metadata operations; refresh authorization after external work; correlate queue acceptance with later attempts. |
| Session | One event loop owns queue, selected request, control transitions and attempt policy; commands have explicit acknowledgement. |
| Playback | One synchronous attempt owns open → read → send → cleanup → terminal result. It never advances queues or reconnects. |
| Source | Metadata resolution and authorized acquisition. No implicit fallback to another provider. |
| Audio | Decode/encode to stereo 48 kHz Opus, 20 ms packets, fixed buffers; process wait result is part of success. |
| Voice adapter | Connection, DAVE/encryption, packet pacing and synchronous transport acceptance; cancellation and disconnect. |
| Cache | Byte/entry budgets, stable keys, leases and atomic publication. No ownership migration of persisted files. |
| Observability | Charm events emitted at operation owners; allowlisted diagnostics; independent health dimensions. |

Decision 2: the decoded audio slice uses synchronous pull and synchronous output.
A reusable packet and PCM frame provide natural backpressure without a decoded
frame producer goroutine or buffer pool. Interfaces exist only for media opening/frames and
voice delivery, which need actual production implementations and deterministic
fault-injection doubles.

Decision 3: no speculative second Discord implementation, public local-file
command, Spotify capture or generic provider-fallback framework. The current
SDK remains in adapters until its independent replacement exists.

Implementation status and temporary migration boundaries are in
[rewrite/status.md](rewrite/status.md). `internal/app` now composes the v2
runtime; the original orchestration has been removed.

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

Metadata now lives in the SDK-independent `internal/media` value type. The
session manager bounds admitted guilds and joins constructors/removals before
shutdown; it never holds its registry mutex across joins or external calls.
Adapter-owned outputs outlive their sessions and are closed after the manager.

`source.Resolver` handles metadata; `source.YouTubeOpener` owns cache lookup and
attempt-scoped byte acquisition. Cached media leases last until decoder cleanup,
and the stable cache-key algorithm is retained. The shared URL
grammar lives in `sourceurl`; every extraction path validates canonical inputs
before subprocess launch.

Acquisition decision (2026-10-16): the October 15 paced-extraction reproduction
failed mid-track while fast acquisition of the same track succeeded. This
justifies decoupling compressed-byte acquisition from decoder/voice pacing with
an 8 MiB ring. It does not establish the network reset's exact cause or promise
provider availability. One acquisition worker and one decoder feeder belong to
each uncached attempt; stream Close cancels and joins both. A 30-second startup
deadline covers admission through the first encoded packet. Preparation waits
for 256 KiB, successful EOF, or two seconds after first bytes. Full buffers apply
backpressure without dropping bytes. The extractor slot is released after reap.

The same acquisition optionally writes one cache transaction, capped at
min(cache size, 256 MiB). There is no background warming worker or duplicate
download. Publication requires nonempty successful acquisition and successful
nonzero decoding; skip/failure/close aborts the transaction. The single writer
reservation may last through playback. This deliberately favors validation and
bounded scratch storage over concurrent cache filling. Cache-write admission or
failure never becomes an audio failure. Live media bypasses the cache.

Decision (2026-10-08): `command.Player` owns a finite number of concurrent play
lookups with no waiting queue; excess requests receive a busy result. The
Discord adapter supplies current voice observations through a two-field core
value. The session coordinator checks that observation against its owned channel
when applying the mutation. Slow lookups capture a session generation; stop,
disconnect and transport failure invalidate it. A reconnect cannot revive an
older lookup. Trusted lifecycle calls use `Apply`; interactions use `ApplyAs`
and `AddResolved`. The adapter must establish the connected target before lookup.

Runtime cutover decision: a guild-scoped voice capability connects/disconnects
adapter-owned transport. The session coordinator owns the connection operation,
its cancellation/join, and at most three transport-recovery attempts. Connection
I/O never runs on the coordinator or under registry locks. Pending metadata work
holds an explicit session reservation so queue-drain cleanup cannot race a new
play lookup. Stop invalidates reservations and cancels joins. Discord callbacks
perform bounded admission; accepted handler goroutines are canceled and joined
by the adapter before its dependencies close.

Acceptance preparation decision (2026-10-13): the existing joined guild observer
owns automatic message updates. One active panel per admitted guild stores only
bounded IDs and view state. A cancellable capacity-one gate serializes edits;
no registry lock spans HTTP and no interaction token is retained. Observations
predating a newly rendered command cannot overwrite it. A shared `netguard`
socket helper bounds context-free SDK WebSocket writes and joins its own
cancellation callback. It does not promise joins for private SDK workers; see
[the SDK lifecycle completion blocker](rewrite/sdk-lifecycle-review.md).
