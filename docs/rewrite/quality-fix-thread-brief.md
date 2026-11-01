# T3 thread: GoBard v2 — fix verified review defects and close contract gaps

Copy the following brief into a new T3 thread for GoBard. This is implementation
work following an independent review, not another rewrite or architecture plan.

---

Work on the GoBard v2 quality findings at baseline `25a8161`, currently on
`t3code/gobard-v2-rewrite-recovery` and `v2-rewrite`. Establish actual repository
state first; preserve unrelated edits and existing branches. Use an isolated
fix branch/worktree such as `t3code/gobard-v2-quality-fixes` if it does not exist.
If the checkout has advanced, inspect changes before applying old reproductions.

Read the applicable AGENTS.md and these review artifacts:

- `/home/lotus/.t3/worktrees/GoBard/t3code-6b8a1ea6/docs/rewrite/quality-review-2026-11-01.md`
- `/home/lotus/.t3/worktrees/GoBard/t3code-6b8a1ea6/docs/rewrite/quality-review-regressions.patch`

The review files are initially uncommitted in that worktree. Read/copy them
explicitly; do not assume a fresh T3 worktree contains them or change the review
worktree. Raw execution evidence is under `/tmp/gobard-review-20261031/`.

The existing Docker race, lint, build/smoke and Compose-profile gates pass, but
the provided patch adds three failing regression functions. Apply the patch
only in your fix worktree, reproduce the failures, then fix the implementation
and incorporate well-formatted contract tests. Do not weaken the assertions to
retain the defects. Review and improve the test design where useful.

Implement in this order:

1. **Automatic voice recovery loses delivered position (P1).**
   `internal/session/session.go` retains the request on transport loss but does
   not update its offset. The next attempt replays from zero or the previous
   seek. Preserve delivered position for finite seekable audio; explicitly
   define safe handling of live/unseekable tracks and intentional disconnect.
   Keep request identity, distinct attempt identity, exactly-once terminal
   accounting, finite reconnect attempts/time and queue advancement. Test loss
   after nonzero seek, repeated partial failures, end boundaries and racing
   skip/stop/disconnect. Never silently replay already-delivered audio.

2. **Successful slow writes cause packet bursts (P2).**
   `internal/discordvoice/output.go` calculates its next deadline from before
   the blocking Write. An 80 ms successful write followed by a normal write
   reproduced a gap of only 22.749 microseconds. Rebase after a real stall while
   preserving normal media-clock phase and bounded output ownership. Test slow
   successes, normal timer jitter, pauses, cancellation and concurrent close.
   This finding does not prove the cause of prior audible stuttering.

3. **Metadata guidance loses specific failure classifications (P2).**
   `command.Player.Play` wraps resolver errors in category Source. Discord
   feedback reads that outer wrapper while command logging uses the inner one.
   Authentication, throttling and unavailable-content errors all become generic
   user feedback. Establish shared classification semantics, preserve wrapped
   causes, and verify actual command-to-response behavior and safe logs.

4. **Finish memory-dependent admission from the original mission.**
   Runtime currently loads fixed MAX_GUILD_SESSIONS=16 independently of RAM.
   Benchmark-only cgroup sampling is not runtime capacity discovery. Discover
   effective memory limits, provide validated operator overrides, reserve safe
   headroom for Go/native/FFmpeg/extractor/cache state, and separate retained
   guild-session admission from active-stream capacity. Base assumptions on
   measurements and document uncertainty. Test small/container/nested/unlimited
   or unavailable limits, overload, releases after cancellation and config
   migration. Do not claim exact streams-per-GB from fake output alone.

5. **Close production diagnostic gaps without weakening redaction.**
   Unknown source/codec failures currently expose only category/hash/exit code;
   voice connection failures also lack an actionable safe reason. Preserve
   bounded, sanitized explanations or useful allowlisted error codes for
   OS/network/codec/provider failures. Keep Charm Log and correlation IDs, log
   the terminal failure once at its owner, and test actual structured records
   against secrets, URLs, cookies, hostile metadata and oversized diagnostics.

6. **Reconcile status and rerun acceptance gates.**
   Update durable status and operational docs around the final implementation,
   separating superseded SDK limitations from current unresolved gates. Review
   the result for redundant helpers, inconsistent error models and unnecessary
   abstractions. Keep the existing architecture; no new SDK, provider framework
   or unrelated feature project is authorized by this brief.

Use Docker for all supported Go build/test/lint/benchmark tooling. This checkout
has no Nix dev shell and host make is absent; use
`nix-shell -p gnumake --run 'make docker-test'` and equivalent commands. If a Nix
development environment is added or exists in your checkout, use it canonically.
Required final gates:

```sh
nix-shell -p gnumake --run 'make docker-test'
nix-shell -p gnumake --run 'make docker-lint'
nix-shell -p gnumake --run 'make docker-smoke DOCKER_IMAGE=gobard:quality-fixes'
nix-shell -p gnumake --run 'make docker-profile-check'
docker build --target bench --progress=plain .
```

Run meaningful capacity regression/soak tests after pacing/admission changes,
with isolated media/cache, explicit resource limits and raw results. Include
long/high-bitrate cold-source backpressure rather than relying only on fixtures
that fit the acquisition ring. Compare like-for-like conditions; report failures
and workload limitations. Do not use a test/no-op substitution to claim a pass.

No VPS access/deployment, service restart, credential change, real-cache deletion
or migration, SDK publication, infrastructure/network change or live Discord
messages are authorized. Do not start/update the existing local test bot. Finish
everything that can be tested offline, then state the exact remaining live
gate: DAVE reconnect, shutdown during active encrypted audio, UI/permissions,
two-guild listening and full cold/warm album acceptance. Seek separate permission
only when a concrete live action is ready and actually needed.

Commit coherent tested fixes on the fix branch using the existing Git identity;
do not invent one. Keep this review's original evidence distinguishable from new
post-fix evidence. Finish with findings addressed, exact commands/results,
remaining blockers, benchmark conditions and branch/commit references. Do not
claim implementation complete while runtime admission or diagnostics remain
unfinished, and never treat fake transport acceptance as proof of audibility.
