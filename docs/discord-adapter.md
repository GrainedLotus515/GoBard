# Discord adapter contract

Isolation rule: imports of discordgo, disgo, godave and snowflake, including their
Gateway/REST/voice/DAVE types, belong only in Discord adapter packages. Today
`internal/bot`, `internal/botui`, `internal/discordvoice` are that migration
boundary; bot currently also contains domain policy that must move to session.
No new core package may import these SDKs.

Core consumes operation values, immutable snapshots and audio delivery, not an
SDK-shaped facade. An interaction is validated as a guild member operation;
mutations carry verified voice membership; configuration carries Manage Guild.
Responses always suppress allowed mentions. User-visible acceptance is distinct
from first frame and completion.

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
