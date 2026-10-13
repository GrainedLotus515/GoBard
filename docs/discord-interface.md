# Discord player interface

The v2 runtime initially replaced `/play` with a generic admission receipt and
hid the existing track card and controls. Playback contract tests passed but did
not establish interface usability. This regression was identified by user review
and corrected in the player-focused pass below.

## Player and controls

`/play` acknowledges the number of tracks added and shows the current selection:
state, linked title, artist, artwork when available, progress/length, requester,
next song and volume. Adding another track still displays the current player;
the queue shows upcoming selections. Unknown durations and live streams have
explicit labels rather than a fabricated duration.

The first button row contains Pause/Resume, Skip, Repeat on/off and Stop.
The second contains Queue and Refresh player. State comes from session snapshots;
starting, paused, seeking, changing track, connecting and disconnected states
remain distinct. Pending controls do not claim completed transitions. Stop and
disconnect acknowledgements remove playback buttons and explain queue behavior.

One message per admitted guild follows current state automatically. Track changes,
pause/resume, queue changes and drain update through the existing guild observer.
Progress advances in fifteen-second steps; automatic edits are at least three
seconds apart and identical views are suppressed. Controls and Refresh fetch a
fresh snapshot. This is sampled progress, not a continuously running clock.

A new player/queue response becomes the active view. Mutation buttons on older
messages return a private explanation; read-only navigation can refresh and adopt
an older message. The adapter retains bounded channel/message IDs, not interaction
tokens or message history. Views are not persisted across bot restarts: use
`/now-playing` to establish a new one.

Deleted or forbidden messages stop updating. Other failures use at most three
attempts, with 30/60/90-second backoff; HTTP 429 respects the server delay with a
30-second minimum. Delays of an hour or more suspend updates. Manual refresh can
establish a new view. A failed UI edit never stops audio or advances the queue.

Button mutations keep the existing voice-channel permission checks. A denied
button press receives an ephemeral follow-up, preserving the shared card and
buttons. Read-only Queue/Player/Refresh navigation does not require voice membership.
All responses suppress allowed mentions; untrusted link labels and artists are
escaped. Recovery notices describe the music interruption and resume, without
frame counts or production-log instructions.

## Queue

Queue pages contain eight upcoming items, each in its own bounded embed field,
and retain the one-based positions consumed by `/move` and `/remove`. The footer
counts current/upcoming tracks rather than completed entries retained internally.
Previous/Next navigate pages; Player returns to the current playback card.
Long titles, artists and Unicode metadata cannot overflow one combined field.

## Verification and remaining review

Deterministic tests cover visible state and control labels, navigation mapping,
permission-denied private replies without replacing the shared message, mention
suppression, pagination positions and worst-case embed field/total lengths.
Integrated fake interactions exercise play, simultaneous toggles, authorization
changes and stop against real session/playback ownership. Automatic-edit tests
cover coalescing, transitions, stale mutations, cancellation, deleted messages,
finite failures and rate limits.
Docker race/lint/build/smoke are the supported quality gates. Rendering and button
usability in the real Discord client still require user review; these tests do
not prove the appearance of the client or live interactions.
