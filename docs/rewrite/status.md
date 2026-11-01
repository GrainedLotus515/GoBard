# Rewrite status — 2026-11-01

## Consolidated merge candidate

The recovery edits and quality-fix branch are integrated on `v2-rewrite`.
The November 1 active-stream budget supersedes the October 27 retained-guild
formula; [migration](../deployment.md) documents the rejected pre-release names.
Healthy Stop retains voice, established failures survive racing Stop, and finite
voice recovery resumes after delivered frames. Registration retries and the
pinned Nix shell are retained. The limiter remains one shared channel; benchmark
interactive probes measure slot availability without synthetic priority classes.
Fresh integration checks are recorded in [merge-candidate validation](merge-candidate-2026-11-01.md).
Historical measurements below apply only to their recorded candidate.
Live acceptance remains pending and this PR does not authorize deployment.

## Historical October 26–27 audit


## Recovery and verification checkpoint

- Recovered the existing `v2-rewrite` branch at starting revision `25a8161d1789357d31b767ad5f8c9140953fee90`.
  Local history and reflog contain all 32 rewrite checkpoints through the prior
  local-activation record; no destructive checkout, reset, branch replacement or
  cache operation was needed.
- Pre-existing user changes remain outside this audit: modified `AGENTS.md`,
  deleted `CLAUDE.md`, untracked `.claude/`, `docs/ERROR_4006_ANALYSIS.md`, and
  `docs/PARALLEL_COMMAND_REGISTRATION.md`. Git author/committer identity is not
  configured, so new commits are blocked until the owner supplies one; work is
  preserved in the working tree rather than assigning an invented identity.
- The behavior inventory, SDK-free core boundaries, playback reference slice,
  real Discord wiring and legacy removal are present in the checkout. Historical
  reports below are evidence for their own revisions, not newly repeated gates.
- The post-fix combined Docker gate passed on 2026-10-26 and the final-tree
  rerun exited zero (Docker reused the verified layers): `docker build --target
  test --progress=plain .`, `docker build --target local-test-check
  --progress=plain .`, `docker build --target lint --progress=plain .`, and
  `docker build --target vulncheck --progress=plain .`. This executed the GoBard
  race suite, pinned discordgo/disgo-voice/DAVE suites, six Python helper tests,
  formatting/vet, golangci-lint with zero issues, and govulncheck with zero
  reachable findings and one unreachable module finding.
- `docker build --target runtime --platform linux/amd64 -t gobard:local .` and
  the hardened read-only runtime smoke passed, including UID/GID 1000, the
  root-owned non-writable executable, libdave linkage, and yt-dlp/FFmpeg/Deno
  checks. `sh ./scripts/check-compose-profiles.sh docker compose` passed all
  Compose profiles. This host has no `make` or native Go, so these are the exact
  underlying Docker commands rather than skipped or weakened Make recipes.

## Current recovery-audit changes

- Stop now clears playback and the queue while retaining a healthy established
  voice connection; pending joins, recovery and broken transport are still
  canceled and cleaned. This deliberately aligns implementation and user receipts
  with the v2 acceptance contract instead of preserving the legacy disconnect.
  Skip/Remove clear stale pause state, removing the final selection during
  recovery triggers drain cleanup, and managed/unmanaged Connect race guards are
  explicit. A final race run also exposed and fixed Stop's terminal result
  incorrectly triggering natural-drain disconnect; the session suite then passed
  ten consecutive runs. Regression tests cover active playback, queue settlement
  and races.
- The process limiter is now one shared bounded admission channel with
  cancellation and idempotent release. Unused priority classes and their
  synthetic benchmark pressure were removed; real guild, audio and cache-pressure
  workloads remain.
- The pinned build toolchain is Go 1.27.3. This removes ten reachable Go 1.27.1
  standard-library vulnerabilities found during the audit; current govulncheck
  reports zero reachable findings and one unreachable module finding.
- The pinned voice source was checked against current official Voice Gateway
  documentation: v8, DAVE negotiation, Speaking-before-audio, preferred AES256-GCM
  and required XChaCha20 transport modes are present on the consumed path. This
  static compatibility check does not replace live reconnect/DAVE acceptance.
- Fresh microbenchmarks and a 30-second concurrent 4/12/24-guild regression pass
  are recorded in [performance.md](../performance.md) and
  [raw short results](capacity-short-2026-10-26.json). The first unchanged
  15-minute run [failed marginal medium/large pacing assertions](capacity-soak-2026-10-26.json);
  the [unchanged repeat passed](capacity-soak-repeat-2026-10-26.json) with zero
  missed deadlines, worker failures or retained temporary files. The large
  repeat still reached 58.97 ms against the 60 ms threshold. Timing variability
  remains unattributed on the non-idle host; neither run is a production-capacity
  or listening-quality guarantee.

## Release-completion work — 2026-10-27

- Fixed the independently reproduced Stop/transport ordering bug. An established
  terminal failure now survives later cancellation, and Stop uses the actual
  result plus transport cleanup state before retaining voice. The exact blocked-
  cleanup ordering, healthy Stop, codec failure and duplicate-disconnect cases
  passed ten race-enabled playback/session runs.
- Failed command registration now schedules owned, bounded reconciliation with
  5-second exponential backoff capped at 5 minutes, honoring Discord `Retry-After`
  up to the HTTP boundary's one-hour limit. Gateway resume also reconciles, so a
  retry timer expiring while disconnected cannot stall readiness until restart.
  Tests cover 429-to-success, persistent failure/readiness, capped backoff,
  shutdown during a pending retry and resumed-Gateway reconciliation.
- Startup derives guild admission from cgroup v1/v2 or host memory, reserving at
  least 256 MiB/25% headroom and 48 MiB per retained guild. Optional strict
  operator overrides are documented; `MAX_GUILD_SESSIONS` is only an upper bound.
  Unit tests cover nested cgroup mounts, unlimited sentinels, invalid budgets,
  standard profiles and 512 MiB/five-guild derivation.
- Added a pinned NixOS development flake with Go 1.27.3, checksum-verified
  libdave 1.2.1, CGO and media tools. `nix flake check --print-build-logs`, `nix
  develop --command go test -race -count=1 ./...`, `nix develop --command go vet
  ./...` and all six Python helper tests passed. Docker remains the release gate.
- The new 30-second reduced/small/medium/large run passed with 5/4/12/24 guilds
  and zero missed deadlines, worker failures or retained temporary files. See
  [raw results](capacity-release-short-2026-10-27.json).
- Uncached Docker gates passed for the full GoBard/consumed-SDK race suite,
  formatting/vet/lint, six Python helper tests, vulnerability analysis, runtime
  build and hardened smoke, and every Compose profile. The local candidate is
  `sha256:49936adb1c655d1da7addcbd9170ade7f15270bde9e4028d2571edccb086b9a1`.
  Uncached microbenchmarks also passed; exact commands and samples are in
  [performance.md](../performance.md).
- The final `sh ./scripts/docker-bench.sh` soak passed all four concurrent
  15-minute profiles with zero missed 60 ms deadlines, worker failures or
  retained temporary files. Maximum gaps were 51.72/24.61/25.55/51.00 ms for
  reduced/small/medium/large. See [raw final results](capacity-release-soak-2026-10-27.json)
  and [measured conditions](../performance.md). This completes the offline
  implementation gates; live verification remains pending.

The final gate sequence exited zero with these commands (Docker emitted only
its two existing constant-`FROM --platform` warnings):

```sh
nix develop --command sh -ec 'go test -race -count=1 ./... && go vet ./...'
docker build --no-cache --target test --progress=plain .
docker build --no-cache --target local-test-check --progress=plain .
docker build --no-cache --target lint --progress=plain .
docker build --no-cache --target vulncheck --progress=plain .
docker build --no-cache --target runtime --platform linux/amd64 --progress=plain -t gobard:local .
docker run --rm --read-only --tmpfs /tmp:rw,noexec,nosuid,size=128m --entrypoint /bin/sh gobard:local -ec 'test "$(id -u)" = 1000; test "$(id -g)" = 1000; test "$(stat -c %u /app/gobard)" = 0; command -v ffmpeg; command -v yt-dlp; yt-dlp --version >/dev/null; command -v deno; deno eval --no-config --no-npm "0"; test ! -w /usr/local/bin/deno; python3 -c "import sys; sys.path.insert(0, \"/usr/local/bin/yt-dlp\"); import yt_dlp_ejs"; test -x /app/gobard; test ! -w /app/gobard; ! command -v curl; ! command -v pgrep; ldd /app/gobard | grep -q libdave'
sh ./scripts/check-compose-profiles.sh docker compose
docker build --no-cache --target bench --progress=plain .
nix flake check --print-build-logs
git diff --check
```

## Current offline quality-fix candidate

The independent [review](quality-review-2026-11-01.md) reproduced three defects
at `25a8161` and identified runtime admission/diagnostic gaps. Its original
artifacts are copied here unchanged; new execution evidence is separate in
[quality-fix validation](quality-fixes-2026-11-01.md).

Implementation commit `a2f6bf3` preserves delivered offsets across voice recovery and intentional
disconnect, prevents bursts after slow successful transport writes, preserves
specific wrapped metadata classifications, derives active-stream admission from
memory/extractor capacity, and logs safe actionable reason/guidance fields.
Finite request/attempt identity and control priorities remain owned by sessions.
Live/unknown-duration interruptions end that selection instead of replaying it.

This work is offline only. It has not updated or inspected the existing local
bot, accessed a VPS, changed credentials, published an image, or touched real
media caches. All local-bot/image claims below are historical observations at
their recorded checkpoints, not a claim about today's running state.

The missing SDK join API blocker is superseded by `0b15b9e` and its pinned SDK
regressions. Exact remaining live gates are DAVE reconnect, shutdown during
active encrypted audio, UI/permissions, 30-minute two-guild listening, and two
isolated cold plus one warm full NF *HOPE* album passes on the approved candidate.
Offline fake transport cannot verify audibility. Full acceptance is still open.

## Historical existing-commitment completion work (October 16)

- `de47336`: bounded 8 MiB acquisition, preparation/first-packet deadline,
  attempt-owned canceled/joined workers, early extractor-slot release and one
  optional same-stream cache transaction; duplicate warming downloads removed.
- `36889e6`: editor work isolated from health, canceled/joined at session removal;
  busy-session observations and fail-closed local update guards.
- `e026c0a`: acquisition-backed 4/12/24-guild 15-minute soaks, all passing with no
  missed 60 ms deadlines, worker failures or retained scratch files. Memory cost
  and workload limits are recorded in [performance.md](../performance.md).
- `15c392f`: removed the obsolete cache-creator API; one bounded transaction
  path now serves production and benchmark writes. Repeated 15-minute soaks at
  4/12/24 guilds passed with no missed 60 ms deadlines, failures or retained
  temporary files; [final raw results](capacity-transactions-2026-10-16.json).
- `697128d`: canceled/closing observations cannot report safe idle.
- `039e738`: opt-in real-provider test; MOTTO and PANDEMONIUM completed cold
  playback and full warm replay without recovery. See [source evidence](provider-2026-10-16.md).
- Docker race/helper, formatting/vet/lint and hardened runtime smoke gates pass
  for those checkpoints; final Compose profile validation also passed. Local candidate image:
  `sha256:55761fc75d41e4521e007d7d28358b36b991667839999f3f1606f477bd3d071b`.
  It replaced the local test bot with user authorization; all 21 completed cache
  checksums matched and commands became ready. [Local evidence](local-2026-10-16.md).
- Focused SDK/native cleanup patches are published in four separate repositories
  with user authorization. Main dependency pins now use immutable published
  pseudo-versions and verified checksums; the adapter uses their cancellation/join
  APIs (`0b15b9e`). The strengthened Docker race gate, lint and smoke pass.
  Inherited GitHub Actions were disabled before publication. See
  [exact revisions and remaining gates](sdk-lifecycle-review.md).

At that checkpoint, strict completion remained blocked on live DAVE
reconnect/active-shutdown and full listening acceptance. Album/listening
acceptance was not established by offline soaks. No new provider or convenience
feature was added, and no VPS service, secrets, production cache or publisher
was changed.

The v2 runtime was then running as local `gobard-v2-test` on the final
SDK-enabled candidate from `0b15b9e`:
`sha256:8a4c447ae068ec6868003b5319211a68945e22f50bbfdf6b4365c14b01980158`.
The strengthened Docker race gate (including the published SDK suites), lint and
hardened smoke passed. Discord and commands were ready; all 21 completed
cache-file checksums were preserved. The acquisition/health predecessor was
stopped as the rollback container. [Exact local evidence](local-2026-10-16.md)
distinguishes Gateway readiness from still-pending DAVE and listening
acceptance. On 2026-10-26, `docker ps` showed no running GoBard container; this
audit did not start one or change any VPS service or image publisher.

## October 15 album investigation

The user stopped the older VPS instance. Local v2 testing of NF's *HOPE* shows
intermittent zero-frame source failures and repeated mid-stream connection
resets. Both suspect album entries downloaded successfully in isolated probes;
a paced MOTTO acquisition/encoding probe reproduced a reset after 166.8605
seconds of encoded audio. See [evidence and limits](live-2026-10-15.md).

The diagnostic patch adds safe public track IDs and specific HTTP 403/missing
format reasons without changing recovery policy. It does **not** establish or
fix the cause of the two historical startup failures. Streaming reliability
remains an open issue. The SDK join-API blocker present at that checkpoint was
subsequently addressed by the pinned fixes noted above; live DAVE acceptance remains open.

The diagnostic patch passed `make docker-test` (race suite and five local-test
helper checks), `make docker-lint` (formatting/vet, zero lint issues), and
`make docker-smoke DOCKER_IMAGE=gobard:nf-diagnostics`. Identifier tests cover
resolution, queue/retry identity, terminal logging and rejection of unsafe
values; failure tests cover safe guidance, precedence and no new retry paths.
The staged runtime is
`sha256:a5f6ca8adfc41c2dd4e037b584674b9b53cea652e4ec820d0077fcf361632829`.
It was not loaded while active playback continued. The running bot still uses
the `6faf849` image identified in the evidence document. No audio hot-path or
optimization change is claimed; the new track field is bounded to 32 bytes.

## Implemented checkpoints

- `81d254f`: session-owned voice join, drain cleanup, cancellation and finite
  recovery; deterministic join/stop/recovery tests.
- `e7309a5`: application cutover to v2 command/session/source/voice adapters;
  original bot/player/YouTube orchestration and duplicate logger removed.
- `a6b5155`: review fixes for bounded Gateway state, metadata object expansion,
  cleanup under saturation, skip during recovery, departed speakers, forced
  health shutdown and Docker/profile gate compatibility.
- `6d7070f`: safe source-reset diagnostics and one bounded resume from delivered
  position; verified with a real source interruption on 2026-10-09.
- `074ce6c`: bounded startup/read/send timing summaries with Docker quality
  gates and measured short capacity regression checks.
- `8772466`: restored player cards, playback controls and private errors.
- `947f542`: automatic player/queue updates, concurrent interaction tests and
  explicit containment/review of temporary SDK lifecycle limits.
- `779cba2`: durable isolated local test helper, cache preservation and rollback.
- `6faf849`: one evolving shared player/queue card, private slash receipts,
  inline recovery/failure guidance and no automatic channel notice stream.

All inventoried commands and buttons have replacement mapping/core tests.
Permissions and queue lifecycle policy live in the session owner. Charm events
separate command acceptance, attempt outcomes, technical categories and recovery.
[The report](report.md) distinguishes implemented, verified and proposed work.

## Verified evidence

- Docker race suite; formatting/vet/lint (zero issues); hardened runtime
  build/smoke; rendered small/medium/large Compose profile checks.
- Real fixture media through FFmpeg/libopus/session/paced fake delivery;
  zero frames, cancellation, stalls, skip/completion races, fault injection,
  multiple guilds, cache persistence and resource cleanup.
- Full `make docker-bench`: concurrent 15-minute profiles at 4/12/24 guilds,
  no missed 60 ms deadlines, worker failures or retained temporary files.
  [Raw soak reports](capacity-soak-2026-10-08.json) record the image and limits;
  [performance](../performance.md) records workload/measurement caveats.
- A final two-second 4/12/24-guild harness run and microbenchmark gate passed
  after review. The final benchmark image disables irrelevant bot health probes.
- Actual Discord Gateway login and command reconciliation in four guilds.
- User-confirmed audible `/play hope` on the local bot. Streaming failed after
  2,233 accepted frames with a connection reset; an independent paced probe
  reproduced the same fingerprint (`ff00ab2b92eaf6f7`) and errno 104. A separate
  cache download contains 5,548 Opus frames over 110.961 seconds. Partial live
  delivery was verified in this initial test.
- Safe connection-reset/timeout diagnostics and one session-owned resume from
  delivered position; deterministic tests cover budget, identity, exclusions,
  stop and shutdown. No authentication bypass or hidden extractor retry.
- A subsequent live request failed after 10,199 frames (203.980 seconds),
  resumed once at that offset, then completed another 3,228 frames. This verifies
  live source recovery and pipeline completion, not uninterrupted listening.
  The user reported stuttering/gaps while Cyberpunk was open; the cause of gaps
  beyond the source interruption remains unresolved. See [live evidence](live-2026-10-09.md).
- Idle connected local-container SIGTERM completed in 1.377 seconds, exit 0,
  no OOM. The stopped checkpoint remains as `gobard-v2-test-e7309a5`.
- On October 12 locally, the user confirmed the rerun worked. A source reset
  after 9,027 frames triggered one resume; another 4,400 frames completed.
  Neither attempt recorded read/send calls over 60 ms (read maxima 3 ms;
  send maxima 22/21 ms). This is a successful listening report plus measured
  recovery, not proof of gapless delivery or the earlier stutter's cause.
  See [rerun evidence](live-2026-10-12.md).

The first audible test image was
`sha256:1f22be9ee97caa798e98f4f677b9731be7c85ba1f148f800ebaf1e144a24932c`.
The successful live recovery used the source-recovery
image `sha256:25688d4eab4aa33a74fc67bddb632532c0e51c869a1647532887e43b0b8d21a1`,
with the same separate temporary test cache. Docker race tests and lint passed
after the recovery change, including real cached decoding at a nonzero offset;
the hardened runtime build and smoke check passed too. Live recovery subsequently
completed. Refreshing the idle connected bot took 1.264 seconds to stop, exit 0,
with no OOM; its stopped checkpoint remains `gobard-v2-test-a6b5155`.

The follow-up timing image is
`sha256:52f3a34e9c165e4e3602348ad72471b207d6782c919c3187da03379b369f46b4`.
It adds fixed-size first-frame/read/send timing summaries for the next live
test. Docker race tests, lint, runtime build/smoke, microbenchmarks and 30-second
4/12/24-guild capacity checks passed; no deadline gaps over 60 ms or worker
failures occurred. See [performance evidence](../performance.md). This instruments
future reports; it does not claim to fix the unresolved listening-quality issue.
The local bot is healthy with reconciled commands after refresh. The preceding
idle container stopped in 1.298 seconds, exit 0, no OOM, and is retained as
`gobard-v2-test-6d7070f`; the same temporary test cache is preserved.

## Acceptance candidate

Automatic player/queue updates are implemented and tested: state changes, sampled
progress, stale buttons, private failures, concurrent controls, deleted messages,
cancellation and bounded rate-limit recovery. Docker race tests, five local-helper
safety tests, lint (zero issues), hardened build/smoke and Compose profiles passed.
A final 30-second 4/12/24-guild regression passed with no gaps over 60 ms, worker
failures or retained files; the earlier fifteen-minute soak remains the sustained
measurement. See [performance](../performance.md) and [interface](../discord-interface.md).

The prior acceptance bot ran `sha256:9186c751a4677255212a658f7d497ad3edf570912fb0e17c3f58bf513377c076`,
with Gateway and commands ready. Its completed test media was copied to
`gobard-v2-test-cache` and verified by SHA-256; the original test copy remains.
The preceding image is retained as stopped `gobard-v2-test-previous` (exit zero,
no OOM). Start/status/stop/update commands and safety boundaries are documented
in [deployment](../deployment.md#durable-local-acceptance-instance).
An additional idle stop/start passed, with the same cache checksum and healthy
Gateway/command readiness. The original `gobard-temp` remains stopped.

The Discord feedback overhaul at `6faf849` passed Docker race tests, lint
(zero issues), runtime build and hardened-image smoke checks. Repeated play,
queue and player requests create one shared message in deterministic interaction
tests. Tests also cover safe failure guidance, private acknowledgement, truthful
recovery/next-track states, explicit deleted-card replacement, and accepted
commands whose UI update fails. Snapshot request identity and voice recovery
state support presentation only; playback policy is unchanged. No performance
improvement is claimed, and this UI work did not establish real client rendering.
The local bot then ran feedback image
`sha256:6f5bb7484e1b6d7135db84e03bd07bfeb6002b0f07a3e6d30efa2bd57552d711`;
Gateway and commands were ready after the idle update. Client appearance and the
new message flow remained user acceptance checks, as did the SDK lifecycle fix
at that historical checkpoint.

## Remaining gates and risks

1. Live controls, shared-card UI, permission checks, registration
   recovery/readiness, voice reconnect and shutdown during active audio still
   need user-driven Discord tests on the final tree.
   Historical listening, source resume and pipeline completion runs passed.
   Earlier stuttering was not attributed; those runs do not establish
   provider-wide reliability or verify the current Stop receipt and lifecycle.
2. The focused SDK fixes are now pinned and their race suites run in the Docker
   gate. Native calls remain cooperative rather than preemptible, and real DAVE
   reconnect/active-playback shutdown remains unverified. See
   [the source review and exit criteria](sdk-lifecycle-review.md).
3. Two independently cold full NF *HOPE* album passes, one warm replay, and a
   30-minute two-guild concurrent listening session remain acceptance gates.
4. YouTube may reject authentication/automation externally. No personal cookies,
   auth bypass or unsupported fallback is required or implemented.
5. Spotify metadata/player control and public self-hosted sources remain
   assessed proposals; there is no independent Spotify audio backend.

The [acceptance checklist](acceptance-checklist.md) consolidates the remaining
client tests. Live acceptance is not complete until those gates are resolved.
No automatic VPS rollout or image publication is authorized.

The consolidated candidate includes the engineering instructions and historical
analysis documents; machine-local assistant permissions remain untracked.
