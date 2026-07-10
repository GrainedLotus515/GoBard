# Contributing to GoBard

Thanks for contributing. GoBard is a YouTube-only Discord bot; please do not add Spotify, SponsorBlock, generic-media URL handling, or unreviewed network fetch paths without an explicit design and security review.

## Development workflow

Docker is required for the supported contributor workflow because it includes libdave:

```bash
git clone https://github.com/GrainedLotus515/GoBard.git
cd GoBard
make docker-test
make docker-lint
make docker-build
make docker-smoke
```

Use `make docker-run` only for a local Discord-token smoke test. Copy `.env.example` to `.env`, add a test token, and run `chmod 600 .env`. Never commit `.env`, tokens, cache contents, generated media, or private signing keys.

Before submitting a pull request:

1. Add focused tests beside changed Go code.
2. Run `make docker-test` and `make docker-lint` without modifying generated output.
3. Update operator documentation and `.env.example` for any configuration or behavior change.
4. Keep application and media subprocess work context-bound; test cancellation, shutdown, and concurrent access.
5. Explain any security boundary, cache, queue-transition, or container-contract change in the pull request.

## Code expectations

- Preserve guild-controller ownership of playback state. Do not mutate queue state from detached playback goroutines.
- Treat all user-provided media input and metadata as untrusted. Canonicalize permitted YouTube URLs and never weaken the egress or URL-validation boundary.
- Maintain cache leases and transactional writes; active or partial media must not be evicted or committed incorrectly.
- Keep runtime log output free of Discord tokens, signed stream URLs, and other credentials.
- Commands are guild-only. Mutating playback actions require active-channel access; configuration mutations require `Manage Guild`.

## CI and releases

Gitea runs Docker quality gates. GitHub pull requests run the same Docker checks, reachable-vulnerability analysis, image scanning, and SBOM generation. Only successful pushes to `main` or version tags publish to GHCR; CI does not deploy hosts.

Do not change workflow action references to mutable tags. Keep all action references pinned to full commit IDs, and update pinned binary checksums when changing a release dependency.

## Reporting issues

Include the GoBard image digest or source commit, Docker/Compose version, redacted logs, exact commands, and a minimal reproduction. For security reports, avoid publishing tokens, signed URLs, internal addresses, or exploit details in public issues.
