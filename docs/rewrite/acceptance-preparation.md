# Acceptance preparation decisions

Automatic UI updates belong to the Discord adapter, using the existing joined
guild observer. Retain one active message per admitted guild, its channel/message
IDs and view, never an interaction token. A per-panel cancellation-aware gate
serializes message edits; no registry mutex spans HTTP. New slash-command views
replace the tracked view; old mutation buttons are rejected privately. Read-only
navigation can refresh an old view. Deleted/forbidden messages stop automatic
updates. Updates coalesce at a three-second minimum interval; progress changes
in fifteen-second steps. Failed updates have a three-attempt budget with bounded
backoff and explicit rate-limit delay. Manual interaction can establish a fresh
view. As revised on October 16, the observer owns one canceled/joined editor
child and a capacity-one hint channel so HTTP cannot delay health observation.
No unbounded message history is retained.

Local live testing will use a dedicated Docker named volume and a tracked helper
for start/status/stop/update. Preserve the existing temporary test cache by copy
on the one-time move to the volume; never mount or modify repository `./cache`.
Credentials remain inherited from the explicitly named local test container or
an operator-supplied private env file, never printed or committed. Restart policy
stays `no`; local authorization does not authorize an unattended production bot.

The final SDK review must distinguish joins proven by GoBard from undocumented
third-party behavior. If public SDK APIs cannot establish a guarantee, record
the precise blocker and containment rather than implement a general Discord
library here or claim a listening test proves it. The user rejected an exception;
the focused SDK fixes are now pinned and offline-verified. Live DAVE reconnect,
active-playback shutdown and listening acceptance remain separate final gates.
