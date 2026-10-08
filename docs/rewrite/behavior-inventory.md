# Behavior inventory

Reference: `44a47e8` on `v2-rewrite`, inspected 2026-10-07. The replacement
engineering standard in AGENTS.md and the prior removal of CLAUDE.md are user
changes and are excluded from rewrite checkpoints.

## Intentional behavior to retain

| Surface | Evidence | Contract |
| --- | --- | --- |
| `/play query` | bot/commands.go, handlers.go, fastpath_test.go | Guild only; caller in voice; same channel as connected bot. YouTube text search, canonical HTTPS video/playlist URLs. Deferred response; queue admission is not proof of playback. |
| `/pause`, `/resume` | handlers.go, player tests | Pause delivery, preserve selection and position; resume it. |
| `/skip` | track_test.go, playloop_test.go | Advance exactly once; bypass single-track loop. |
| `/stop` | handlers.go | Stop, clear queue, disconnect. |
| `/disconnect` | handlers.go | Leave voice but retain selection/queue for later play. |
| `/queue`, `/now-playing` | botui/cards.go, ui_helpers.go | Readable without voice membership; paginated queue, progress, current item. |
| `/clear` | track_test.go | Retain current item, remove other queued items. |
| `/shuffle`, `/loop` | track_test.go | Shuffle upcoming items; loop current item on successful completion only. |
| `/volume level` | player_test.go | 0–100, including mute. |
| `/seek position`, `/fseek seconds` | handlers.go, player_test.go | Absolute timestamp or nonnegative forward offset; reject live/unseekable and invalid positions. |
| `/move from to`, `/remove position` | track_test.go | One-based displayed positions; move upcoming items only; removing current selects immediate successor, bypassing loop. |
| `/config` | commands.go, handlers.go | Show ducking settings; mutate enabled/target with Manage Guild or administrator. Active voice membership also required when connected. Settings are currently process-local. |
| Buttons | components.go, cards_test.go | Pause/resume, skip, stop, loop and queue pagination; stale controls receive truthful idle state. Same mutation permissions as commands. |
| Mentions/DMs | security_test.go | Guild member required; no untrusted allowed mentions. |
| Voice lifecycle | bot.go, lifecycle_test.go | DAVE-capable delivery; actual voice-speaking events drive ducking; intentional disconnect is distinct from transport loss. |
| Cache | cache/cache.go, cache_test.go | Stable SHA-256-prefix `.webm` keys; restart discovery; leases protect active entries; temporary writes then rename; bounded committed bytes. Preserve existing files. |
| Sources | sourceurl/policy.go, youtube_test.go | Validate before every extractor invocation, canonicalize exact YouTube allowlist; bounded playlist expansion; shared extractor concurrency. |
| Health | health_test.go, bot.go | `/live`, `/ready`, executable healthcheck, loopback listener. Existing readiness checks startup, gateway and tools/cache; it does not prove provider availability or delivered audio. |
| Deployment | Dockerfile, Compose, workflows | linux/amd64, UID/GID 1000, immutable executable/root FS, writable cache, bounded tmpfs; pinned tools/checksums; GHCR publishing, Gitea checks only. |

## Configuration baseline

Retain strict token/token-file exclusivity; cache directory/size; default volume;
ducking enabled/target; playlist cap (1–500); extractor concurrency (1–16);
loopback health address; global versus guild registration; presence status,
activity type/text/URL; debug settings. `.env.example` and config tests are the
field-level reference. New admission/resource limits must be strict and documented.

## Historical and uncertain behavior

`823d468` removed the Spotify client. Its parent contains client-credentials
metadata lookup and paginated playlist lookup (`internal/spotify/spotify.go`).
Historical Spotify handling searched YouTube for audio; this is evidence of a
metadata bridge, not an independent Spotify audio backend. No current credential
or external verification is available. Restoration requires separate capability
and terms assessment. Current code retains only a Spotify URL detector.

SponsorBlock, YouTube API credentials, arbitrary media URLs and DMs are not
current supported behavior. They are not silently added by this rewrite.
SOPS/age is an operator compatibility requirement; no tracked decrypt/deploy
workflow establishes how the stopped VPS currently obtains its secrets.

## Concrete issues, not preservation requirements

* `player.GuildPlayer` and `Queue` expose mutable state; bot workers separately
  hydrate, replace, select and advance pointer-identified tracks.
* `player.newSessionCustomEncoder` compares function pointers using reflection
  to detect global test hooks. Tests should inject dependencies at boundaries.
* `player.newPlaybackSession` starts from Background; composition later attempts
  to stop independently owned work. A lifecycle parent should own attempts.
* `voiceconn.SendOpusFrame` has no cancellation parameter or ownership/pacing
  contract. Pacing currently lives in the player.
* `logger.PlaybackFramesComplete` can label a count as completion independently
  of source/process results. Lower encoder layers also log terminal errors.
* FFmpeg file encoder uses an unbounded bytes.Buffer for stderr and logs it raw;
  streaming code reports extractor stderr byte counts without a safe reason.
* Guild player maps and queue Add have no admission limit. Playlist limits do
  not bound repeated queue additions or guild/session memory.
* Cache commit/eviction performs filesystem I/O while holding its shared mutex;
  temporary downloads are not bounded by the committed-byte limit alone.
* Compatibility no-ops and context-free entry points remain (`RewindCurrent`,
  bot helper wrappers). Retaining these APIs is not a product requirement.

## Migration decisions

Do not activate new external sources by default. Prove local fixture audio
offline first; a fixture source is not a public local-path command. Add explicit
admission rejection at finite resource limits. Preserve command names and
permissions. Keep old runtime until replacement contract tests cover its behavior;
temporary coexistence is a migration checkpoint, not the final architecture.

## Initial verification

Docker initially required sandbox escalation to access `/var/run/docker.sock`;
this is an environment permission, not a code failure. The baseline race gate
was started using `make docker-test`; final results are recorded in status.md.
No bot, Compose service, live cache, credentials or production endpoint was used.
