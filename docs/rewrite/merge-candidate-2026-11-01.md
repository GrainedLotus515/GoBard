# Consolidated v2 merge candidate — 2026-11-01

Implementation: `626839d755e4518a2296e05d676dec0ab283b5e4`, based on main
`44a47e8c6e2e04ac3b5f6db1a6f464baac666903`. The merge includes recovery checkpoint
`585bca6` and both quality-fix commits through `cbf04af` without rewriting them.
The old Discord experiment targets removed legacy code and is not included.

## Integration decisions

- Retain the quality-fix active-stream admission policy and separate retained
  guild budget. Reject obsolete pre-release `PLAYBACK_MEMORY_*` settings with
  migration guidance; see [deployment](../deployment.md#runtime-memory-admission).
- Preserve healthy Stop's voice connection and clear the queue. An already
  established attempt failure remains authoritative if Stop races its cleanup.
  A regression verifies that healthy Stop releases capacity for another guild.
- Preserve delivered-position recovery, slow-write pacing, specific wrapped
  failure classifications, safe diagnostic guidance and registration retries.
- Keep the single shared process limiter and remove synthetic priority pressure.
  The benchmark's interactive probe measures slot availability, not metadata
  provider latency. Runtime and historical stress workloads remain distinct.
- Retain pinned Nix development tooling and Docker release gates. Machine-local
  assistant permissions, credentials and persisted cache are excluded.

## Verification

These commands passed for the consolidated implementation (all project tooling
runs through `nix develop`; `nix flake check` validates the environment itself):

```sh
nix flake check --print-build-logs
nix develop -c go test -race -count=1 ./...
nix develop -c go vet ./...
nix develop -c go test -race -count=10 ./internal/session
nix develop -c go mod verify
nix develop -c python3 -B -m unittest discover -s scripts -p 'test_*.py'
nix develop -c make docker-test
nix develop -c make docker-lint
nix develop -c make docker-smoke DOCKER_IMAGE=gobard:merge-candidate
nix develop -c make docker-profile-check
nix develop -c docker build --target vulncheck --progress=plain .
nix develop -c docker build --target bench --progress=plain .
```

Formatting and whitespace checks passed. The Docker test gate includes pinned
Discord/DAVE dependency race tests and six local-helper tests. Lint reported
zero issues. Vulnerability analysis found zero reachable vulnerabilities and
one finding in a required module outside the reachable code. Runtime smoke
verified UID/GID 1000, the root-owned non-writable executable, media tools and
libdave under a read-only filesystem and bounded tmpfs. No bot was started.

Single-run microbenchmark samples: cache acquire 790.9 ns, 352 B/3 allocations;
limiter acquire 75.15 ns, 32 B/2 allocations; synthetic attempt 87,774 ns,
4,184 B/4 allocations; real-media benchmark 211,711,196 ns, 119,640 B/93
allocations. These are reference samples, not a measured improvement.

All four concurrent two-minute runtime-admission probes passed at 1/3/7/11
streams under 512 MiB/1/2/4 GiB limits. Each used a 20-minute 256 kbit/s fixture
(38,613,206 bytes, exceeding the 8 MiB acquisition ring), a network-disabled
container, read-only root and 128 MiB tmpfs. Every admitted guild delivered;
there were zero missed 60 ms deadlines, worker failures or retained temporary
files, and goroutines returned to one. Maximum gaps were 22.06/21.78/22.40/56.99
ms; the large maximum is close to the deadline and warrants continued stress
validation. These short offline runs are not a new soak or live-audio proof.
Exact commands, results, image IDs and log hashes are in
[merge evidence](merge-candidate-evidence-2026-11-01.json).

Raw local gate logs are retained in `/tmp/gobard-merge-validation/`.

## Remaining acceptance

GitHub's PR workflow must still validate its exact head, including the image
scan and SBOM steps. Historical live successes do not verify this consolidated
candidate. DAVE reconnect, shutdown during active encrypted audio, client
rendering/permissions, a 30-minute two-guild listening session and two isolated
cold plus one warm full-album passes remain in the [acceptance checklist](acceptance-checklist.md).
Higher-count stress timing failures recorded by the quality-fix review remain
limitations; runtime admission does not erase those observations.

Opening this PR does not deploy or publish an image. A later merge to main
triggers the existing GHCR publishing workflow; production rollout still needs
explicit authorization. No live infrastructure, credentials, existing local bot
or real cache was modified.
