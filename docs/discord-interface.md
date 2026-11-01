# Discord player interface

The v2 runtime initially replaced `/play` with a generic admission receipt and
hid the existing track card and controls. Playback contract tests passed but did
not establish interface usability. This regression was identified by user review
and corrected in the player-focused pass below.

## Player and controls

`/play` privately acknowledges the number of tracks added and shows the current selection on the shared card:
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

Slash commands receive compact private receipts with an Open player link when a
shared card exists. `/play`, `/queue` and `/now-playing` reuse that card in the
same channel; they no longer create a public reply for every invocation. A view
request in a different channel establishes the active card there. Read-only
requests before a session exists return a private idle/empty view.

Mutation buttons on older
messages return a private explanation; read-only navigation can refresh and adopt
an older message. The adapter retains bounded channel/message IDs, not interaction
tokens or message history. Views are not persisted across bot restarts: use
`/now-playing` to establish a new one.

Deleted or forbidden messages stop updating. Other failures use at most three
attempts, with 30/60/90-second backoff; HTTP 429 respects the server delay with a
30-second minimum. Delays of an hour or more suspend updates. Manual refresh can
establish a new view. A failed UI edit never stops audio or advances the queue.
An explicit view request encountering a deleted card can create one replacement;
the observer never recreates messages on its own. If an applied command's card
edit fails, the private receipt distinguishes that UI failure from the accepted
operation, rather than encouraging an accidental duplicate command.

Button mutations keep the existing voice-channel permission checks. A denied
button press receives an ephemeral follow-up, preserving the shared card and
buttons. Read-only Queue/Player/Refresh navigation does not require voice membership.
All responses suppress allowed mentions; untrusted link labels and artists are
escaped. Recovery notices describe the music interruption and resume, without
frame counts or production-log instructions.

## Playback feedback

The intended middle ground is a compact music card with familiar controls and
useful status, without a stream of bot messages. Interruptions, recovery, final
failures and queue completion update the same card. There are no separate
automatic “interrupted,” “empty queue” or “leaving voice” channel posts.

| Situation | Visible feedback |
| --- | --- |
| Media connection drops with a permitted resume | “Trying to resume”; explicitly says one attempt, not that audio has already resumed. |
| Audio delivery resumes for the same request | The interruption notice clears. |
| Failed track followed by another selection | “Previous track couldn't finish”; explains that the queue moved on. |
| Voice reconnect in progress / exhausted | Distinct notices; exhaustion explains that the queue is retained and how to reconnect. |
| Provider refuses access or returns no audio | Specific plain-language explanation and a next step; no raw extractor output. |
| Queue finishes | The card becomes “Queue finished,” only after a completion with delivered frames. |
| Permission, capacity or timeout rejection | A private card explains the constraint; timeouts advise checking the queue before retrying. |

Failure copy is allowlisted. Provider/Discord access failures, decoding failures,
rate limiting and voice failures are distinguished. Unknown failures stay safe;
tokens, signed URLs, stderr, fingerprints and internal IDs are never shown.
The card is current state, not a durable event log: fast intermediate transitions
may coalesce, and complete attempt accounting stays in production Charm logs.

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
Additional tests prove that repeated play/queue/player commands create only one
shared message, all slash acknowledgements are private, recovery clears only
after delivery for the same request, deleted views require explicit recreation,
applied commands survive UI rate limits, and hostile error causes never appear.
Docker race/lint/build/smoke are the supported quality gates. Rendering and button
usability in the real Discord client still require user review; these tests do
not prove the appearance of the client or live interactions.
