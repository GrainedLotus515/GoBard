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

Cards are snapshots: control presses, navigation and Refresh player fetch current
state. There is no continuously advancing timer, automatic track-change edit or
persistent per-guild player message yet. Old cards can be refreshed; their controls
act on the current guild session with current authorization. These limitations
remain visible work, not a claim that the full interface overhaul is complete.

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
Docker race/lint/build/smoke are the supported quality gates. Rendering and button
usability in the real Discord client still require user review; these tests do
not prove the appearance of the client or live interactions.
