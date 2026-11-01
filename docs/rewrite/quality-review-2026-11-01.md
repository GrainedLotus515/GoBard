# Independent quality review — 2026-11-01 UTC

Reviewed revision: `25a8161` on `t3code/gobard-v2-rewrite-recovery`.
Starting working tree: clean. `v2-rewrite` also points to this revision.
The review request arrived October 31; execution logs crossed into November 1 UTC.

**Verdict: substantial, well-structured rewrite, but not ready to declare the
original mission complete.** Existing gates pass. Three additional regression
probes fail, and memory-dependent admission and general failure diagnostics fall
short of the requested contracts. Live acceptance is still explicitly pending.

## Review method and scope

1. Record revision, status, branches and local reflog; read instructions and
   compare the implementation with its status, behavior inventory and contracts.
2. Review session transitions, recovery, source/process ownership, cache,
   adapter pacing, authorization, error propagation and capacity configuration.
3. Run supported Docker validation and isolate additional regression probes in
   an archive of the starting revision, without changing application code.
4. Preserve evidence and prepare a separate implementation-thread brief.

No checkout/reset, existing-branch change, production access, bot restart,
credential access or persisted-cache operation was performed. Test media and
reproduction files live under `/tmp/gobard-review-20261031`. Review documents and
the reproduction patch are the only working-tree additions. No fixes or commits
are claimed by this review.

The repository has no Nix development environment. Host `make` was absent;
`nix-shell -p gnumake --run 'make …'` supplied it temporarily. All Go checks and
runtime probes ran in Docker, as required by AGENTS.md.

## Confirmed defects

### R1 — P1: automatic voice recovery replays delivered audio

Location: `internal/session/session.go:445` in `coordinator.complete`, followed
by `coordinator.start`. The disconnect/transport-failure branch retains the
request but never advances its offset by the failed attempt's delivered frames.
After reconnect, the next attempt uses the original offset (normally zero).
Source recovery already accounts for delivered position; voice recovery does not.

The isolated test delivered a frame, injected `VoiceLost`, and inspected the
real coordinator's next request. It failed with:

```text
TestReviewTransportRecoveryPreservesDeliveredPosition
voice recovery replays delivered audio: offset=0s, want >=20ms
```

The consequence scales with playback progress: a transport failure late in a
song can replay the whole delivered prefix. Current recovery copy says the
connection is being restored and the queue retained; it does not disclose this
rewind. The lifecycle document mentions replay but defines no offset/replay
policy. This misses the mission's prohibition on silently replaying already
delivered audio.

Fix acceptance: specify automatic recovery and intentional disconnect separately;
preserve delivered position for seekable finite media or explicitly stop when
safe resumption is unavailable. Define live/unseekable behavior. Cover failure
after a nonzero initial seek, repeated failures, completion boundaries and
skip/stop races without replenishing the existing recovery budget.

### R2 — P2: slow successful writes defeat the no-burst pacing contract

Location: `internal/discordvoice/output.go:101`. The next deadline is calculated
from a timestamp taken **before** the potentially blocking transport `Write`.
If that write succeeds after a stall, the next deadline is already in the past
and the following packet can be accepted immediately after it.

A fake writer delayed its first successful write by 80 ms (within the adapter's
250 ms deadline), then accepted the second normally. The actual Output adapter
produced acceptance gaps of 9.834 microseconds and 22.749 microseconds in two
runs. The added test requires only 9 ms, matching the existing pacing test's
tolerance, and fails. Existing tests cover delayed scheduling and failed stalled
writes, but not successful stalled writes.

Fix acceptance: preserve normal media-clock phase without cumulative drift,
while rebasing after a blocking successful write so delivery does not catch up
in a burst. Keep cancellation, short-write and concurrent-close guarantees.
The fake test proves a pacing-contract defect; it does not establish the cause
of the historical user-reported Discord stutter.

### R3 — P2: metadata failure classification is lost in user feedback

Locations: `internal/command/play.go` wraps resolver errors with
`failure.Wrap("metadata", failure.Source, err)`;
`internal/bot/feedback.go:41` takes the outermost `failure.Error`.
Production command logging walks to the inner classification, but the Discord
response does not. Authentication, throttling and unavailable-content failures
therefore all become the generic "track couldn't be loaded" message.

`TestReviewWrappedMetadataFailureKeepsActionableGuidance` reproduced all three
cases with the exact error shape returned by the command service. Each expected
specific guidance and received the same generic response.

Fix acceptance: preserve one authoritative classification through wrapping and
use it consistently for user responses and production logs, retaining underlying
causes. Add a command-to-response regression test, including nested wrappers and
redaction. Do not expose raw stderr or transport errors to Discord users.

## Unfinished mission requirements

### G1 — memory-dependent capacity is not implemented in application admission

`internal/config/config.go:93` loads a fixed `MAX_GUILD_SESSIONS` default of 16
with a range up to 1024. `internal/session/manager.go` admits by registry size;
`internal/app/app.go` passes that value directly. No production code discovers
the effective memory budget or derives an active-stream limit from it.
Cgroup memory sampling exists in the benchmark command, not runtime admission.

Compose memory quotas and finite buffers are useful protections, but the memory
limit can be reduced independently of allowed concurrent streams. Connected
guilds, retained session objects and active streams are not separate admission
budgets. The measured fixture results do not provide this missing policy.
This is an implementation gap against mission section 11, not a demonstrated
OOM in this review.

Implement tested effective-limit detection (including applicable cgroup limits),
explicit operator overrides, conservative headroom for child processes and
other application memory, and separate active-stream admission. Document
unsupported/unmeasured profiles instead of inventing streams-per-GB promises.

### G2 — unknown failures are still opaque in normal production logs

`internal/failure/failure.go:65` recognizes a small allowlist of provider
diagnostics. Other failures carry a category, exit status and fingerprint;
there is no sanitized explanation field. `playback.terminal` does not log the
wrapped cause. `session.transportComplete` also logs connection failure without
a safe cause. Distinct FFmpeg errors or filesystem failures cannot generally be
diagnosed from a hash and category alone.

The redaction boundary is conservative and useful; do not fix this by blindly
logging causes. Add bounded, tested safe reasons for actionable OS/network/codec
failures and a safe explanation strategy for remaining diagnostics. Include
failure context without secrets, signed URLs, cookies or arbitrary untrusted
text. Verify actual production log records, not just `Error()` strings.

### G3 — live/native acceptance remains unverified

The latest repository report already says strict completion is blocked by live
acceptance. This review did not log into Discord or call YouTube. Real DAVE
reconnect, shutdown during active encrypted audio, concurrent listening,
client rendering and full cold/warm album acceptance remain separate gates.
Passing fake-transport tests cannot clear them.

`docs/rewrite/status.md` also retains an older "SDK exposes no strict join"
blocker below its newer published-SDK-fix evidence. Reconcile those sections
with the current candidate instead of presenting both as current status.

## Positive findings and verification limits

- Application composition uses the new engine; the queue/session relationship
  has an explicit owner, cancellation and finite recovery policies.
- Core packages are separated from Discord SDK types by a passing architecture
  check. The real adapter is wired, not just an isolated core prototype.
- Bounded queues, metadata, extraction concurrency, acquisition rings, packet
  sizes and cache transactions are concrete mechanisms with meaningful tests.
- Existing race tests exercise cancellation, queue controls, lifecycle cleanup,
  source failures, cache ownership, UI interactions and the pinned SDK suites.
- URL canonicalization, pipe-only FFmpeg input, private command feedback and
  mention suppression are present. This was not an exhaustive security audit.
- Documentation distinguishes packet acceptance from audibility and historical
  provider/live evidence from offline benchmarks. Those claims were inspected,
  not accepted as fresh execution evidence.

## Fresh validation

All commands used the reviewed source unless explicitly marked reproduction.
Full raw logs are in `/tmp/gobard-review-20261031/`.

| Command | Result | Evidence |
| --- | --- | --- |
| `nix-shell -p gnumake --run 'make docker-test'` | Pass, exit 0 | Race suite for GoBard plus discordgo, disgo/voice, libdave and golibdave; local-helper tests. `docker-test.log`. |
| `nix-shell -p gnumake --run 'make docker-lint'` | Pass, exit 0 | Formatting, vet and golangci-lint: 0 issues. `docker-lint.log`. |
| `nix-shell -p gnumake --run 'make docker-smoke DOCKER_IMAGE=gobard:quality-review-25a8161'` | Pass, exit 0 | Includes runtime image build and hardened tool/permissions probe. `docker-smoke.log`. |
| `nix-shell -p gnumake --run 'make docker-profile-check'` | Pass, exit 0 | Small, medium, large and missing-image rejection checks. `docker-profile-check.log`. |
| `docker build --target bench --progress=plain .` | Pass, exit 0 | Microbenchmarks only. `docker-bench-micro.log`. |
| `docker build --target test --progress=plain /tmp/gobard-review-20261031/repro` | Expected failure, exit 1 | Three additional test functions fail, including all three metadata subcases. `repro-final.log`. |

Runtime image ID:
`sha256:76ceb3e033e1813c62a68e70e9fd317a10a696caa1c28c29f70ff6722d2b7504`.
BuildKit emits two advisory warnings about constant linux/amd64 FROM platforms;
this platform choice is an explicit project requirement, not a failed gate.

Microbenchmarks on Intel Core i9-12900K, 24 visible logical CPUs, Docker,
pinned Go 1.27.1, no explicit microbenchmark CPU/memory quota:

| Workload | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Cache acquire | 687.9 | 352 | 3 |
| Limiter admission | 67.82 | 64 | 2 |
| Synthetic 1,000-frame attempt | 91,374 | 4,169 | 4 |
| Real-media 1,000-frame attempt | 204,291,859 | 118,812 | 92 |

These are single invocations with other review builds potentially overlapping;
they do not establish a speedup or production capacity. The complete 15-minute
`make docker-bench` soak and external-provider tests were not rerun.

Four sequential 30-second capacity profiles also passed, exit 0 each. They
exercise real acquisition/FFmpeg/libopus with local fixture extraction and fake
voice delivery, not networking, DAVE or human listening. Each ran with no network,
a read-only root filesystem, 128 MiB noexec /tmp and a 256 PID limit. The
[raw results and exact docker run commands](quality-review-capacity-2026-11-01.json)
record image ID, quotas, timing, resource counters and exit codes.

| Guilds | CPU quota / RAM limit | CPU % of quota | Peak anonymous MiB | Maximum gap ms | Maximum session shutdown ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | 2 / 1 GiB | 3.46 | 18.56 | 23.04 | 1.83 |
| 4 | 2 / 1 GiB | 9.04 | 53.34 | 21.08 | 19.40 |
| 12 | 4 / 2 GiB | 14.83 | 148.50 | 28.45 | 21.75 |
| 24 | 8 / 4 GiB | 14.29 | 290.07 | 23.40 | 31.52 |

All reported zero missed 60 ms deadlines, zero worker failures and zero retained
temporary files, with goroutines returning from two to one. Raw results also
include total cgroup memory, including file-cache charges. These short fixtures
have a different memory footprint from the historical 15-minute workloads;
the lower anonymous memory here is not evidence of an optimization. They do not
exercise full-ring long-source pressure or native Discord encryption.

## Reproductions and handoff

[quality-review-regressions.patch](quality-review-regressions.patch) contains
the three additional tests. Apply it to an isolated worktree at the reviewed
revision, then run `make docker-test` through Nix as above. It intentionally fails
until the defects are fixed. Format the new Go tests in the supported Docker
environment when incorporating them into a permanent test suite.

The [fix-thread brief](quality-fix-thread-brief.md) gives the scope, boundaries
and acceptance criteria. No T3 thread was created: the available connector has
preview tools but no thread-creation capability, and its browser requires
pairing. The user selected a prepared brief instead of pairing.
