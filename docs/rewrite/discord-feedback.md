# Discord feedback overhaul

User decision: one evolving player card, minimal channel noise. Keep detailed
Charm diagnostics in production logs. Discord receives bounded, allowlisted
explanations of what happened, what recovery is underway, and what to do next.

Reuse the active guild card for commands in its channel. Routine slash commands
receive private receipts; controls edit the shared card. Player/queue requests
in another channel explicitly establish the active view there. No unsolicited
message stream, new worker, event history or retry framework is introduced.

Playback failures and recovery appear inside the player/queue card. A delivered
frame after recovery clears the interruption notice for that request. A failed
previous track is distinguished from the current selection; cancellation and
successful completion must never appear as failures. Missing cards are restored
by an explicit player request, with existing finite HTTP failure budgets retained.

Permission, capacity, provider, decoding, connection and timeout failures have
different user guidance. Raw errors, diagnostic fingerprints, attempt IDs,
signed URLs and implementation jargon do not belong in Discord. Mentions remain
suppressed. SDK lifecycle completion remains blocked independently of this work.
