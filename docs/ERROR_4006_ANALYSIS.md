# Voice Gateway Error 4006 — "Session is no longer valid"

## Summary

When a second `/play` command is issued while a track is already playing, the Discord voice gateway closes the connection with **close code 4006 ("Session is no longer valid")**. This kills audio playback for the current track and prevents the next track from playing until the bot reconnects.

This document records the full root-cause analysis, what was tried, and what remains unresolved.

---

## Architecture Context

GoBard uses a **hybrid library approach** for Discord voice:

- **discordgo** (ozraru fork) — handles the main gateway (commands, events, state tracking)
- **disgo** (`github.com/disgoorg/disgo/voice`) — handles the voice transport (UDP, Opus frames, DAVE/E2EE)

Voice gateway events (`VOICE_STATE_UPDATE`, `VOICE_SERVER_UPDATE`) arrive on the discordgo main gateway WebSocket. GoBard forwards them to disgo's voice manager via bridge methods in `internal/discordvoice/manager.go`.

Both discordgo and disgo maintain **separate internal state** about the bot's voice session. Both receive the same gateway events.

### The Voice Connection Lifecycle

1. Bot sends **Opcode 4** (VOICE_STATE_UPDATE) to the main gateway requesting to join a channel
2. Discord responds with a **VOICE_STATE_UPDATE** event containing the bot's `session_id`
3. Discord sends a **VOICE_SERVER_UPDATE** event containing the voice server endpoint + token
4. disgo uses the `session_id` + `token` + `endpoint` to **IDENTIFY** on the voice gateway WebSocket
5. The voice gateway responds with **READY** (includes SSRC)
6. Bot opens UDP connection to the voice server
7. Bot sends **SPEAKING** opcode, then streams Opus frames over UDP

The `session_id` from step 2 is critical — it ties the voice gateway connection to the main gateway's voice state. If it changes or becomes stale, the voice gateway invalidates the session.

---

## What Happens During the Bug

### Timeline from diagnostic logs

**Song 1 starts playing:**
```
T+0.000  /play "song one" received
T+0.252  Bot sends Op 4 (join channel) → Discord assigns session_id=A
T+0.300  Discord sends VOICE_STATE_UPDATE with session_id=A (stale, from previous bot run)
T+0.302  Bot sends Op 4 (join channel) again → Discord assigns session_id=B
T+0.310  Discord sends VOICE_STATE_UPDATE with session_id=B (current)
T+0.320  Discord sends VOICE_SERVER_UPDATE
T+0.550  Voice channel joined, playback begins
```

**Second /play arrives while song 1 is playing:**
```
T+12.000  /play "song two" received
T+12.060  Discord sends VOICE_STATE_UPDATE for bot with session_id=A (stale)
T+12.060  4006: "Session is no longer valid" — voice gateway closes
T+12.100  disgo close handler fires → conn.Close() → sends Op 4 with channel_id="" (leave)
T+12.500  resolveQuery completes, song two queued
T+15.000  Frame send fails ("use of closed network connection")
T+15.100  Track "completed" (actually killed by 4006)
```

### Key Observations

1. **The 4006 is triggered by a stale VOICE_STATE_UPDATE event**, not by anything the second `/play` command does directly. The second `/play` happens to coincide with Discord delivering a delayed voice state event from a previous bot session.

2. **Two session_ids are visible**: `session_id=A` (stale, from a previous bot run before restart) and `session_id=B` (current, assigned during this run). Discord sends events with both session_ids.

3. **disgo overwrites the session_id unconditionally** in `conn.go:194`:
   ```go
   func (c *connImpl) HandleVoiceStateUpdate(update botgateway.EventVoiceStateUpdate) {
       // ...
       c.state.SessionID = update.SessionID  // line 194 — overwrites every time
   }
   ```
   When a stale event with `session_id=A` arrives, disgo replaces the active `session_id=B` with `session_id=A`. The voice gateway then sees a mismatch and sends 4006.

4. **discordgo also overwrites the session_id** in `wsapi.go:786`:
   ```go
   func (s *Session) onVoiceStateUpdate(st *VoiceStateUpdate) {
       // ...
       voice.sessionID = st.SessionID  // also overwrites
   }
   ```
   This is harmless in GoBard's case because discordgo's own voice connection (`s.VoiceConnections[guildID]`) is never used for actual audio transport — disgo handles that. But it confirms both libraries have the same pattern.

5. **The 4006 close is non-recoverable in disgo**: `GatewayCloseEventCodeSessionNoLongerValid` has `Reconnect: false`. disgo's close handler (`handleGatewayClose`) calls `conn.Close()`, which sends Op 4 with `channel_id=""` (leaving the channel), closes the UDP socket, and removes the connection from the manager.

---

## Root Cause

The root cause is a **stale session_id collision** between bot restarts:

1. Bot run 1 joins voice → Discord assigns `session_id=A`
2. Bot is restarted (container rebuild, crash, etc.) without cleanly leaving the voice channel
3. Bot run 2 joins voice → Discord assigns `session_id=B`
4. Discord's voice state reconciliation eventually delivers a VOICE_STATE_UPDATE with the old `session_id=A`
5. disgo overwrites its active `session_id=B` with `session_id=A`
6. The voice gateway invalidates the session → 4006

The stale session_id persists because:
- Docker container restarts don't give the bot time to send a clean Op 4 (leave) to Discord
- Discord retains voice state for disconnected clients for a period (minutes)
- Neither discordgo nor disgo track which session_id is "current" vs "stale"

---

## What Was Tried

### Attempt 1: Reconnection logic in playLoop (partial mitigation)

Added reconnection logic to `playLoop` that detects when the voice connection dies mid-track, rejoins the voice channel, and replays the current track from the beginning.

**Result:** The bot recovers and continues playing, but the current track restarts from the beginning rather than resuming from where it left off. The 4006 still occurs; this is a workaround, not a fix.

**Code:** `internal/bot/handlers.go` — after `waitForTrackCompletion`, checks `IsVoiceConnected()` and calls `rejoinVoiceChannel` if false.

### Attempt 2: Filter stale bot VoiceStateUpdate events

Added a filter in `voiceStateUpdate` handler to stop forwarding bot VoiceStateUpdate events to disgo after the bot is already connected (only forward during initial join and disconnect events).

**Result:** The 4006 still occurs. The stale event arrives before the filter can catch it in some timing windows, or Discord invalidates the session independently of the event being forwarded to disgo.

**Code:** `internal/bot/bot.go` — `voiceStateUpdate` checks `p.IsVoiceConnected()` before forwarding.

### Attempt 3: Send leave before join to clean up stale sessions

Added `session.VoiceStateUpdate(guildID, "", false, false)` before `VoiceManager.Join()` to force Discord to clean up any stale voice sessions before joining.

**Result:** No effect. The 4006 still occurs. Discord doesn't immediately clean up the old session when a leave is sent; the stale state persists and collides later.

---

## What Was NOT Tried (Potential Approaches)

### A. Track and compare session_ids in the bridge layer

Instead of filtering all bot VoiceStateUpdate events, compare the incoming `session_id` against the one disgo already has. Only forward the event if the `session_id` matches the current active session or if disgo doesn't have a session_id yet (initial join).

**Challenge:** disgo's `conn.state.SessionID` is not exposed via the `Conn` interface. Would require either:
- Adding a method to disgo's `Conn` interface (upstream change)
- Using reflection to read the private field
- Maintaining a separate `sessionID` tracker in GoBard's bridge layer

### B. Prevent stale sessions at the source

Send a leave (Op 4 with empty channel) during bot **shutdown** (in `bot.Stop()`) for every guild where the bot is connected. This would clean up Discord's voice state before the next bot run starts.

**Limitation:** Only helps if the bot shuts down cleanly. Container kills (SIGKILL, OOM, crash) would still leave stale sessions.

### C. Ignore 4006 and force-reconnect

Override disgo's `Reconnect: false` behavior for 4006 by adding a custom close handler that attempts to reconnect regardless of the close code.

**Challenge:** disgo's close handler (`handleGatewayClose`) immediately calls `conn.Close()`, which sends a leave and tears down the connection. By the time a custom handler runs, the connection is already gone. Would need to replace disgo's `handleGatewayClose` entirely via a custom `ConnCreateFunc`.

### D. Use disgo for the main gateway too

Eliminate the hybrid approach entirely. Use disgo for both the main gateway and voice, so there's only one library maintaining voice state and no bridging of events.

**Challenge:** Large refactor. discordgo is used for slash commands, state tracking, message sending, and component interactions. Migrating all of that to disgo is a significant effort.

### E. Prevent discordgo from processing voice events internally

discordgo's `onVoiceStateUpdate` (in `event.go:233`) runs automatically for every VoiceStateUpdate event and updates `s.VoiceConnections[guildID].sessionID`. Even if GoBard's handler filters events before forwarding to disgo, discordgo's internal handler still runs and may interfere with state.

**Approach:** Don't register discordgo's voice event handlers at all, or clear `s.VoiceConnections` after joining to prevent discordgo from tracking voice state. Since GoBard uses disgo for voice transport, discordgo's `VoiceConnections` map is unused.

---

## Relevant Source Code References

### disgo (v0.19.2)

| File | Line | What |
|------|------|------|
| `voice/conn.go` | 173-195 | `HandleVoiceStateUpdate` — overwrites `c.state.SessionID` unconditionally |
| `voice/conn.go` | 272-276 | `handleGatewayClose` — calls `c.Close()` on any gateway close |
| `voice/conn.go` | 293-305 | `Close()` — sends Op 4 with nil channel (leave), closes UDP + gateway |
| `voice/gateway.go` | 460-510 | Gateway close handling — reads `closeCode.Reconnect` to decide whether to reconnect |
| `voice/gateway_opcodes.go` | 111-116 | `GatewayCloseEventCodeSessionNoLongerValid` — code 4006, `Reconnect: false` |
| `voice/manager.go` | 82-90 | `CreateConn` — returns existing conn if one already exists for the guild |

### discordgo (ozraru fork v0.26.2)

| File | Line | What |
|------|------|------|
| `wsapi.go` | 749-766 | `VoiceStateUpdate` — sends Op 4 to main gateway |
| `wsapi.go` | 769-790 | `onVoiceStateUpdate` — internal handler, overwrites `voice.sessionID` |
| `event.go` | 231-234 | Voice events dispatched to internal handlers in goroutines |

### GoBard

| File | Line | What |
|------|------|------|
| `internal/bot/bot.go` | 170-210 | `voiceStateUpdate` — bridges events to disgo + handles voice ducking |
| `internal/discordvoice/manager.go` | 82-94 | `HandleVoiceStateUpdate` — converts discordgo event to disgo event |
| `internal/bot/bot.go` | 240-254 | `JoinVoiceChannel` — calls `VoiceManager.Join` |
| `internal/bot/handlers.go` | 282-506 | `playLoop` — playback loop with reconnection logic |

---

## Current State

The 4006 error **still occurs** when a second `/play` is issued during playback. The reconnection logic (Attempt 1) allows the bot to recover by rejoining and replaying the current track, but the playback is interrupted and restarts from the beginning.

The root cause is in the interaction between Discord's stale voice session reconciliation and disgo's unconditional session_id overwriting. A complete fix likely requires one of the approaches in section "What Was NOT Tried" — most promisingly **approach A** (session_id comparison in the bridge layer) or **approach E** (prevent discordgo from tracking voice state internally).

---

## Upstream disgo Issue

This is a **known bug in disgo** — the library does not properly handle 4006 close codes.

### Issue #534 — "No audio received after voice gateway connection interrupted"

- **URL:** https://github.com/disgoorg/disgo/issues/534
- **Status:** Open (opened Mar 25, 2026)
- **Reporter:** nicklipple
- **Version:** v0.19.2 (same version GoBard uses)

The reporter observes the same 4006 error: `"failed to open voice gateway connection: websocket: close 4006: Session is no longer valid."` After disgo auto-reconnects following a voice gateway interruption, no audio is received.

**topi314** (disgo maintainer) confirmed:
> please note a 4006 is usually not recoverable and you are required to do a full voice reconnect in those cases. disgo does not currently handle this and doesnt really allow you to hook into the close either, kinda akward, fixing those things is on my to do list

### PR #537 — "fix(voice): improve voice connection handling and state management"

- **URL:** https://github.com/disgoorg/disgo/pull/537
- **Status:** Open (opened Apr 8, 2026, not yet merged)
- **Author:** topi314
- **Linked issue:** #534

The PR adds a `NewConnection` flag to close code metadata (including 4006) so disgo knows it needs to do a full reconnect (new voice state update + new session) rather than just reconnecting the voice gateway WebSocket. It also reworks the open/close signaling from channel-based to cancel-func-based and removes the `AutoReconnect` config toggle.

Copilot review caught a bug in the PR: `handleGatewayClose` calls `c.Open()` for `NewConnection` close codes but then unconditionally calls `c.Close()` right after, which would immediately tear down the reconnection. This has not been addressed yet.

### Related closed PR #536 — "Fix 4006 reconnect"

- **URL:** https://github.com/disgoorg/disgo/pull/536
- **Status:** Closed (superseded by #537)

### Implications for GoBard

Until PR #537 is merged and released, disgo v0.19.2 cannot recover from a 4006 on its own. GoBard's reconnection logic in `playLoop` (Attempt 1) is the correct workaround — it detects the dead connection after frame sending fails, rejoins the voice channel via `VoiceManager.Join`, and replays the current track.

Once the PR lands, GoBard should upgrade disgo and evaluate whether the `playLoop` reconnection logic can be simplified or removed (disgo would handle the full reconnect internally). The stale-session-id filtering in `voiceStateUpdate` (Attempt 2) may still be needed to prevent the 4006 from occurring in the first place, since disgo's fix handles recovery but not prevention.
