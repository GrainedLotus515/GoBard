# Local Discord acceptance pass

Use the authorized local `gobard-v2-test` instance. No VPS rollout is part of
this pass. Check `make local-test-status` first. Record the image ID, approximate
time, command/query or public media link, and what you saw/heard. Do not share
tokens, private env files or signed media URLs.

For the October 16 completion candidate, execute three full NF *HOPE* album
passes: two independently empty temporary test caches, then one replay of the
second pass's warm cache. Keep the normal named test cache and production cache
intact; coordinate isolated volumes with the operator rather than deleting them.
Record each resolved track, accepted terminal outcome, skipped/failing entry,
first-frame timing and audible interruption. A recovered reset remains an
interruption. Cache warming is now part of successful playback, not a second
background acquisition. The two-track source probe is not this album gate.

Complete the controls/reconnect/active-shutdown rows below on the exact candidate
and a 30-minute two-guild concurrent listening session. The SDK fixes must be
pinned before the final reconnect/shutdown acceptance. If a guild, credential or
listener is unavailable, leave that gate explicitly unverified.

| Check | Expected result |
| --- | --- |
| Play an exact permitted YouTube link, then a search query | Joins your voice channel, displays the resolved track and delivers audio. Search is provider-ranked: use an exact link to disambiguate a song. |
| Watch a full track and the transition to a queued second track | No unexplained gaps; card updates to the next track. Progress advances in 15-second steps. Report any interruption even if audio resumes. |
| Repeat play, queue and player commands in the same channel | One shared card is edited; command receipts are private with an Open player link. No stream of new public acknowledgements. |
| Observe an interruption or final failure | A concise explanation appears on the shared card, distinguishing attempted recovery from exhausted recovery or queue advancement. Technical error output is absent. |
| Pause, resume, skip and repeat | Labels and repeat state follow the actual session. Paused audio stops; resume continues; skip moves once. Switch repeat off before testing natural drain. |
| Add enough tracks for multiple queue pages | Queue/Player navigation works, long titles remain readable, positions match move/remove, and the current selection stays clear. |
| Seek, volume, shuffle, move/remove and clear | Each acknowledgement matches its effect. Seek may restart acquisition; controls must not falsely report completion. |
| Stop; separately disconnect and play again | Stop clears the queue and leaves voice. Disconnect preserves selection; the next play reconnects with that queue retained. |
| Use a superseded card, then Refresh | Old mutation buttons explain privately that the player was replaced. Refresh or `/now-playing` establishes the current view. |
| Delete the active card, then `/now-playing` | Audio continues; automatic edits stop on the deleted message. A new view resumes updates. |
| Try a mutation outside the bot's voice channel | Private denial, no changed music or destroyed shared player. Read-only queue/player views remain available. |
| Try configuration without/with Manage Guild | Unauthorized changes fail; authorized settings behave as documented. |
| Let the queue finish naturally | Audio stops, voice drains cleanly, and the card shows Queue finished. No extra empty-queue/leaving messages or successful zero-frame completion. |
| Disconnect/kick the bot from the test voice channel | It either recovers within the finite session policy or gives a truthful failure; no runaway reconnects or duplicated track advance. Capture the time for log correlation. |
| Coordinate a local restart during playback | Clean stop, no hung local container or orphan audio process, followed by successful new playback. Queue/settings are in-memory and do not survive restart. |
| If available, play in two guilds concurrently | Controls and audio remain isolated; stopping one guild does not affect the other. |

The operator correlates failures with `command.terminal`, `playback.terminal`,
`session.recovery` and `discord.panel` logs. A source rejection should be explicit;
provider anti-bot/authentication rejection is not fixed by trying personal cookies.
One successful song proves that observed path only. Fake-transport tests do not
verify Discord connectivity or listening quality.

The [SDK lifecycle blocker](sdk-lifecycle-review.md) requires a verified library
fix; the user rejected a temporary exception. This checklist cannot prove private
SDK worker joins or waive that requirement. The checklist is pending until actual
client results are recorded.
