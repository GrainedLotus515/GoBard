# Discord adapter contract

Isolation rule: imports of discordgo, disgo, godave and snowflake, including their
Gateway/REST/voice/DAVE types, belong only in Discord adapter packages. Today
`internal/bot`, `internal/botui`, `internal/discordvoice` are that migration
boundary. Queue/retry/permission mutation policy belongs to session.
No new core package may import these SDKs.

Core consumes operation values, immutable snapshots and audio delivery, not an
SDK-shaped facade. An interaction is validated as a guild member operation;
mutations carry verified voice membership; configuration carries Manage Guild.
Responses always suppress allowed mentions. User-visible acceptance is distinct
from first frame and completion.

The implemented `command.AccessSource` returns current `session.Access` values
(voice channel and Manage Guild capability). `Session.ApplyAs` serializes the
authorization check with mutations; `AddResolved` also checks the generation
captured before metadata lookup. Stop/disconnect and voice-move lookup races
have deterministic tests. The production runtime now maps Discord interactions
and Gateway voice state to these values.
`bot.MapInteraction` now maps every existing slash command and music button to
core operations. Tests feed fake SDK interactions through the mapper into a real
session to verify permission enforcement. Wrong types, missing/duplicate options,
integer overflow/NaN and malformed subcommands are rejected before SDK helpers
can panic. The production event handler uses this mapper.

Audio delivery is stereo Opus at 48 kHz in 20 ms packets. Caller owns bytes;
Send may read them only until it returns. Adapter owns pacing, RTP/encryption,
DAVE and connection state. A successful Send means transport acceptance, not
audibility. Send must honor cancellation and bounded write deadlines. Close
must unblock pending writes and join owned workers; repeated Close is safe.
Reconnect never advances the core queue; the session chooses its finite retry.

Replacement checklist:

1. Implement only consumed interaction, gateway and voice capabilities in the
   separate library; keep its general-purpose work out of this repository.
2. Map values at this boundary; do not export SDK errors or session types.
3. Run command/permission/mention and malformed-interaction contract tests.
4. Run fake delivery tests for packet ownership, pacing, stalled consumers,
   cancellation, disconnect, reconnect and shutdown.
5. Verify real Discord login, registration, DAVE handshake, audible playback,
   reconnect and graceful close in an authorized test guild.
6. Replace adapter/composition imports, remove the old SDK, rerun Docker/race
   gates. A fake delivery test does not satisfy step 5.

## Implemented v2 packet adapter

`discordvoice.Connection.Output` exposes a playback Output after a successful
voice join. It serializes writes, paces 20 ms packets, rejects empty/over-1,400-byte
packets, rejects short writes and joins a pending send during Close. Deadline
support was checked against the pinned
[disgo v0.19.6 UDP implementation](https://github.com/disgoorg/disgo/blob/v0.19.6/voice/udp_conn.go).
Network writes have a 250 ms maximum deadline (or the caller's sooner deadline).
Cancellation during pacing is immediate; cancellation during an SDK write is
bounded by that deadline. Native encryption cannot be preempted by this adapter.

The media clock keeps its 20 ms phase through ordinary scheduler lateness, with
a 10 ms minimum scheduled interval. Longer stalls reset the clock to a full
interval; overdue packets are never sent in a catch-up burst. This avoids
accumulating each timer wakeup's lateness into sustained playback drift.
The capacity workload uses this actual adapter over a discard packet writer.

Deterministic tests cover pacing, copied packet ownership, stalled writes,
short writes, deadlines, close joining and repeated close. The connection owner
must still manage speaking state and close the SDK connection after closing
Output. The production runtime now uses this interface and core-owned recovery.
Real DAVE delivery was exercised by the user-confirmed live playback runs;
voice reconnect and shutdown during active audio remain external test gates.

## Runtime ownership and temporary SDK limitations

`internal/app` composes the source, engine, sessions and Discord runtime.
Interaction callbacks are synchronous admission only: 32 active handlers, a
separate configurable play cap, and a 16-entry rejection queue with two workers.
Per-guild UI observers coalesce terminal failure notices. All GoBard work enters
a shutdown barrier; callbacks cannot create resources after shutdown begins.

The SDK REST rate-limit sleeps ignore context. Consumed endpoints use a
small adapter-local HTTP implementation (respond, edit, ephemeral follow-up,
channel message, command
overwrite), with 8-second requests, no redirects, 64 KiB response projection and no
hidden retry. Registration owns a three-attempt budget. SDK automatic Gateway
reconnect is disabled; GoBard owns three reconnect attempts. Gateway sockets
close on root cancellation and writes have five-second deadlines.

Rejected component mutations use ephemeral follow-ups after acknowledging the
button, leaving the shared player message intact. Queue/Player/Refresh buttons
map to read-only inspection. The session snapshot's pending transition supports
truthful UI labels without moving queue policy into the adapter. See
[the interface contract](discord-interface.md) for rendering and refresh behavior.

SDK `VoiceStateUpdate` can dereference a nil connection; the adapter uses its
synchronized `GatewayWriteStruct` capability. The SDK itself holds its socket
lock across I/O; the adapter bounds that I/O and holds no GoBard registry lock
around it. SDK `VoiceState` iterates mutable state without a read lock; GoBard
retains its own bounded channel observations instead.

The pinned SDKs do not expose a join for their heartbeat/listener workers. Voice
AutoReconnect is disabled, but the SDK's heartbeat reconnect paths ignore that
flag. Both WebSocket transports have owned cancellation and five-second write
deadlines. Manager close snapshots connections through the SDK's locked iterator
and closes outside the lock, avoiding its unsafe map-wide Close implementation.
Native DAVE operations remain non-preemptible. These are unresolved engineering
gaps that block strict completion until the library is fixed and verified; the
user rejected a temporary exception. Exact source findings, containment and
required verification are in
[the SDK lifecycle review](rewrite/sdk-lifecycle-review.md).
Gateway state caching is disabled in the SDK. The adapter retains only bounded
guild IDs and user-to-voice-channel observations: 1,024 guilds, 1,024 voices per
guild, 16,384 voices globally. Oversized snapshots fail authorization closed;
ordinary leaves release their slots. Guild removal has a bounded cleanup queue.
Tests cover saturation, external-payload mutation and concurrent membership moves.
