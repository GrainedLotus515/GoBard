# GoBard v2 review fixes — 2026-11-01 UTC

Baseline: `25a8161`. Worktree/branch: `t3code/gobard-v2-quality-fixes`.
Implementation commit: `a2f6bf303bd498ab3c02b04bb8f7991fd2b91c06`.
Original review evidence is copied unchanged in [the review](quality-review-2026-11-01.md),
[regression patch](quality-review-regressions.patch) and
[capacity results](quality-review-capacity-2026-11-01.json). The original checkout
and `/tmp/gobard-review-20261031/` were not modified.

## Implementation

1. Voice recovery now advances the logical request by the completed attempt's
   accepted frames, starting from that attempt's offset. Finite seekable media
   resumes; live/unknown-duration media and end boundaries settle the interruption
   and advance, including with loop enabled. Intentional disconnect preserves
   the delivered position without automatic reconnect. Request IDs, distinct
   attempt IDs, first-control priority, stop precedence and finite recovery
   budgets are retained. Tests cover nonzero seek, repeated partial failures,
   end/overflow boundaries, disconnect, skip and stop.
2. Packet deadlines use Write completion time. Normal timer jitter retains the
   media-clock phase; successful stalled writes and pauses rebase instead of
   sending overdue packets in a burst. Regression coverage retains cancellation,
   short-write, packet ownership and concurrent-close checks. This fixes the
   reproduced pacing contract; it does not establish the historical stutter's cause.
3. `failure.From` establishes originating-classification semantics through
   contextual wrappers; `Wrap` preserves category/reason/fingerprint/exit status
   and the cause chain. The command service, playback and Discord feedback use
   the same classification. A real Player-to-response test verifies authentication,
   throttling and availability guidance plus one correlated, redacted JSON record.
4. Startup discovers host and visible cgroup memory limits and computes separate
   active-stream admission. Validated operator overrides can reduce the budget.
   Slots are reserved before queue mutation and held until attempts join, including
   pause/seek/recovery. Retained disconnected queues reacquire capacity on join.
   Runtime also preserves an extractor slot for metadata when possible: long cold
   sources retain their subprocess slot under backpressure. Tests cover nested,
   container, v1/v2, unlimited, missing, malformed and small limits; overload,
   concurrent admission, cancellation/cleanup, reconnect and config migration.
   [Deployment](../deployment.md#runtime-memory-admission) documents the formula,
   conservative allowances and uncertainty rather than promising streams per GB.
5. Known OS/network/codec/provider failures gain allowlisted reasons and fixed
   operator guidance. Unknown payloads remain bounded fingerprints with
   stage-specific investigation guidance. Production playback, command and voice
   records are tested against credentials, signed URLs, cookies, hostile metadata,
   control characters and oversized diagnostics. No raw external text is logged.
6. Status, lifecycle, migration and acceptance docs distinguish current policy,
   historical observations, resolved SDK join APIs and remaining live acceptance.
   Existing SDK/dependency pins, URL validation and Docker hardening are unchanged.

## Reproduction and Docker gates

The supplied patch failed before implementation, as expected: voice recovery
restarted at zero, the successful 80 ms write produced a 42.264 microsecond gap,
and all three metadata cases returned generic guidance. `make docker-test`
returned exit 2 because its Docker test stage failed. The original assertions
remain in the permanent tests; two older recovery fixtures now explicitly have
finite duration, and separate tests cover the new unknown-duration policy.

All five required final gates passed, exit 0: race/dependency/helper tests,
formatting/vet/lint (zero issues), hardened runtime build/smoke, Compose profiles
and microbenchmarks. The separate capacity image build also passed. Exact
commands/results, the runtime image ID, source fingerprint and raw-log hashes are
recorded in [gate evidence](quality-fixes-gates-2026-11-01.json). Raw execution logs are
retained under `/tmp/gobard-quality-fixes/`, separate from review evidence.

```sh
nix-shell -p gnumake --run 'make docker-test'
nix-shell -p gnumake --run 'make docker-lint'
nix-shell -p gnumake --run 'make docker-smoke DOCKER_IMAGE=gobard:quality-fixes'
nix-shell -p gnumake --run 'make docker-profile-check'
docker build --target bench --progress=plain .
```

There is no Nix development shell in this repository; only temporary gnumake
comes from Nix. Go tooling, formatting, real audio tests and benchmarks run in
the pinned Docker environment. BuildKit's constant `linux/amd64` FROM warnings
are consistent with the project's platform contract.

## Capacity evidence and limitations

Results are recorded in [capacity evidence](quality-fixes-capacity-2026-11-01.json), with
exact commands, image IDs, resource limits, stderr and exit codes, including
failures. All probes use disposable network-disabled containers, read-only root
filesystems, a 128 MiB noexec/nosuid tmpfs and 256 PID limits. No real cache mount,
credential, Discord connection or YouTube request is involved. FFmpeg, libopus,
the acquisition ring, session ownership and production pacing are real; provider
extraction is a local file script and voice acceptance uses a discard writer.

The initial short profiles use the review's same 30-second, 64 kbit/s conditions
at 1/4/12/24 guilds, 2/2/4/8 CPU quotas and 1/1/2/4 GiB limits. The 24-guild run
recorded one 77.174 ms gap while quality builds overlapped it and failed the
unchanged 60 ms deadline gate. This is retained alongside a later quiet rerun;
build overlap is context, not proof of the gap's cause or permission to discard it.
The final quiet 12-guild stress run also failed on two gaps over 60 ms (maximum
65.037 ms), despite no
overlapping builds. Therefore these stress counts do not have a clean timing
acceptance result, and no scheduling or performance improvement is claimed.
They exceed runtime admission (seven streams for that profile). The unchanged
strict deadline gate remains useful negative evidence; it was not weakened.

A 35-second, 12-guild stress probe used a 20-minute, 256 kbit/s fixture of
38,613,206 bytes, much larger than the 8 MiB acquisition ring. Eight streams
started; four exhausted preparation while waiting for the eight extractor slots.
It failed with four worker failures and no missed delivery deadlines. This
demonstrates why the old short fixtures cannot establish cold-source capacity.
Runtime admission limits this profile to seven, preserving one metadata slot.

Three concurrent 15-minute soaks passed at admitted counts of 3/7/11 with that
same long fixture and explicit 1/2/4 GiB and 2/4/8 CPU quotas. The harness
interruption did not stop their containers; log collection was reattached.
All intended guilds delivered; all three had zero missed 60 ms deadlines, worker
failures and retained temporary files, and goroutines returned to one. Maximum
gaps were 23.218/23.834/24.806 ms; peak anonymous memory was approximately
90.66/204.29/289.20 MiB. Maximum interactive waits were 39.89/83.47/124.43 ms.
Their image predates final diagnostic fingerprints, benchmark reporting and
the increase from 1 to 4 MiB retained-guild headroom. Stream counts, pacing and
media path are identical. Fresh final-image short and high-bitrate checks are
recorded separately; the older soak is not mislabeled as the final image.

On the final implementation image, all three two-minute high-bitrate runtime
checks passed with all 3/7/11 guilds delivering. Maximum gaps were
22.173/40.239/23.606 ms, interactive work completed 3,635/1,553/1,006 times,
and there were zero worker failures, retained temporary files or missed 60 ms
deadlines. These supplement, rather than relabel, the earlier 15-minute soaks.
The final short 1/4/24-guild stress checks passed; the 12-guild timing failure
above remains unresolved for that stress workload. All required Docker gates
passed, but not every deliberately higher-count stress timing check passed.

The final benchmark explicitly requires every intended guild to deliver and,
in runtime mode, interactive work to complete. Canceled interactive waits count
toward the maximum wait, and first-frame averages use started guilds. Earlier
stress reports omit the interactive-completion count and their first-frame
average includes unstarted guilds in the denominator; those values are not a
valid latency comparison. Stubbed process-work counters are not total real
extractor counts. Sampled cgroup memory includes tmpfs/file-cache charges; it is
not RSS. Measurements omit real provider/DAVE costs and establish no production
capacity, audible quality or statistical performance improvement.

## Remaining live gates and boundaries

Still unverified on this candidate: real DAVE reconnect; shutdown during active
encrypted audio; UI rendering and permissions; 30-minute two-guild listening;
two isolated cold NF *HOPE* album passes and a full warm replay. Record actual
audible interruptions and per-track terminal outcomes. The pinned SDK regression
suites address the former absent-join-API blocker; they do not clear these live
gates. The [acceptance checklist](acceptance-checklist.md) remains pending.

No deployment, publication, service restart, existing local-bot action,
credential change, infrastructure/network change or real-cache deletion was
performed. No live authorization is needed to finish this offline work; a future
concrete live acceptance action needs separate authorization.
