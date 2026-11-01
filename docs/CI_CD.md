# CI and image publishing

GoBard separates source validation from image publication. Gitea runs quality
checks only. GitHub builds, verifies, signs, and publishes the release image to
GHCR. Neither workflow deploys or restarts a bot.

## Gitea quality checks

`.gitea/workflows/go-test.yml` runs on pushes and pull requests to `main` and
`develop`:

- `docker build --target test --progress=plain .` runs the race-enabled Go and
  Discord SDK tests in the image containing libdave.
- `make docker-profile-check` validates the small, medium, and large Compose
  profiles and rejects a missing image.
- `docker build --target lint --progress=plain .` checks formatting, `go vet`,
  and the pinned golangci-lint configuration.

These checks require a Docker-capable runner. They do not use a matrix of Go
versions, upload coverage, build release binaries, or publish packages.

## GitHub verification and publication

`.github/workflows/docker-build.yml` runs on the same branch and pull-request
events, version tags, and manual dispatch. It repeats tests, lint, and profile
validation, then runs reachable Go vulnerability analysis with:

```sh
docker build --target vulncheck --progress=plain .
```

The workflow builds one linux/amd64 runtime image and performs all subsequent
checks on that local image:

1. Verify FFmpeg, yt-dlp and its JavaScript runtime, Deno, the executable, and
   filesystem hardening.
2. Fail on fixed HIGH or CRITICAL findings reported by Trivy.
3. Generate and upload an SPDX SBOM.

Pull requests stop after verification. Publication is restricted to pushes to
`main` and `v*` tags in `GrainedLotus515/GoBard`. On those events the workflow:

1. Confirms that `cosign.pub` matches the configured private signing key without
   printing secret material.
2. Pushes the verified image to a run-specific staging tag.
3. Signs the resulting immutable digest and attaches SBOM and SLSA provenance
   attestations with Cosign.
4. Promotes that same digest to commit-SHA, semantic-version, and (on the default
   branch) `latest` tags, verifying every resolved digest.

The staging tag transports image blobs; it is not a deployment or release
channel. Production configuration should pin a verified digest rather than
follow a mutable tag. GHCR authentication uses the workflow's `GITHUB_TOKEN`.
Cosign credentials are repository secrets and are not needed for local tests.

## Local equivalents

With Go, libdave, FFmpeg, yt-dlp, and their native dependencies installed, the
Go checks can run directly. Docker is the supported reproducible path:

```sh
make docker-test
make docker-lint
make docker-profile-check
make docker-build
make docker-smoke
docker build --target vulncheck --progress=plain .
```

`make docker-bench` runs offline benchmarks separately from provider and live
Discord verification. Building or scanning an image locally never publishes it.
See [deployment](deployment.md) for runtime constraints and
[rewrite status](rewrite/status.md) for checks actually run on the current tree.
