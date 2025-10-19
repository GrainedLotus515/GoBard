# CI/CD Pipeline - GoBard

This document describes the continuous integration and continuous deployment (CI/CD) pipeline for GoBard using Gitea Actions.

## Overview

The GoBard project uses Gitea Actions to automatically:
- Build and test the Go application
- Run linting and code quality checks
- Build Docker container images
- Publish images to the container registry
- Perform security vulnerability scanning

## Workflows

### 1. Go Tests and Linting (`go-test.yml`)

**Triggers:** Push to `main`/`develop` branches, Pull Requests

**Jobs:**

#### Test
- Runs tests on multiple Go versions (1.24, 1.25)
- Executes with race detection enabled
- Generates code coverage reports
- Uploads coverage to Codecov

**Requirements:**
- Go 1.24+
- No external dependencies needed for testing

#### Lint
- Runs `golangci-lint` for code quality checks
- Verifies code formatting with `go fmt`
- Runs `go vet` for static analysis

**Failure Conditions:**
- Test failures on any Go version
- Lint warnings or errors
- Code formatting issues
- Vet issues

#### Build
- Depends on successful test and lint jobs
- Compiles the final binary
- Uploads binary as artifact for 5 days

---

### 2. Docker Build and Publish (`docker-build.yml`)

**Triggers:**
- Push to `main`/`develop` branches
- Tagged releases (`v*`)
- Pull Requests (build only, no push)

**Jobs:**

#### Build
- Sets up Docker Buildx for multi-platform builds
- Authenticates with Gitea Registry
- Builds Docker image from Dockerfile
- Pushes to registry (except for PR)
- Uses GitHub Actions cache for build layers

**Registry Configuration:**
- Registry: `git.grainedlotus.com`
- Image: `grainedlotus515/gobard`

**Image Tags:**
- `branch-name` - Current branch name
- `vX.Y.Z` - Semantic versioned releases
- `latest` - Latest stable release (main branch)
- `testing` - Test images from any push
- `<branch>-<commit-sha>` - Commit-specific images

**Example Tags:**
```
git.grainedlotus.com/grainedlotus515/gobard:main
git.grainedlotus.com/grainedlotus515/gobard:v1.0.0
git.grainedlotus.com/grainedlotus515/gobard:latest
git.grainedlotus.com/grainedlotus515/gobard:main-a1b2c3d4
git.grainedlotus.com/grainedlotus515/gobard:testing
```

#### Security Scan
- Runs Trivy vulnerability scanner on filesystem
- Scans for known vulnerabilities in dependencies
- Generates SARIF report
- Uploads results to Gitea Security tab
- Does not block build on warnings

**Requires:**
- `go mod` dependencies to be downloaded

#### Notify
- Final status report
- Shows image locations and tags
- Indicates security scan results

---

## Setup Requirements

### 1. Gitea Actions Runner

Ensure a Gitea Actions runner is configured and running:

```bash
# Check runner status in Gitea UI:
# Repository → Settings → Actions → Runners
```

**Requirements for runner:**
- Docker daemon running and accessible
- At least 10GB disk space
- Go 1.25+ installed (optional, for local testing)

### 2. Registry Credentials

Add registry credentials as repository secrets:

**In Gitea UI:**
1. Go to Repository → Settings → Secrets
2. Add the following secrets:

- `REGISTRY_USERNAME` - Docker registry username
- `REGISTRY_PASSWORD` - Docker registry token/password

**For Gitea Container Registry:**
```bash
# Generate token in Gitea user settings:
# User Settings → Security → Access Tokens → Generate New Token
# Scopes needed: write:package, read:package
```

### 3. Dockerfile

Ensure `Dockerfile` exists in repository root. The workflow uses it to build images.

**Current Dockerfile location:** `./Dockerfile`

---

## Usage

### Automatic Triggers

**Push to main/develop branches:**
```bash
git push origin main
# → Automatically triggers both workflows
# → Docker image built and tagged with branch name
```

**Create a release tag:**
```bash
git tag v1.0.0
git push origin v1.0.0
# → Docker image built and tagged as v1.0.0
# → All artifacts finalized
```

**Pull Request:**
```bash
# Create pull request to main/develop
# → Tests run to verify changes
# → Docker image built (but not pushed)
# → Linting and code quality checks run
```

### Manual Workflow Trigger

In Gitea UI:
1. Go to Repository → Actions
2. Click workflow name
3. Click "Run workflow"

---

## Pipeline Status Checks

### Required Checks (PR Merge Blocker)

Pull requests require successful completion of:
- ✅ Go Tests (all versions)
- ✅ Linting (golangci-lint)
- ✅ Code formatting (go fmt)
- ✅ Static analysis (go vet)
- ✅ Docker build

### Optional Checks (Warning Only)

- ⚠️ Security scan (Trivy)
- ⚠️ Code coverage thresholds

---

## Monitoring and Logs

### View Workflow Runs

In Gitea UI:
1. Go to Repository → Actions
2. Click on workflow run
3. Click on job to expand
4. View individual step logs

### Common Issues

#### Build Fails with "Docker not found"
- Ensure Docker daemon is running on runner
- Check runner configuration in Settings → Actions → Runners

#### Registry Authentication Failed
- Verify `REGISTRY_USERNAME` and `REGISTRY_PASSWORD` secrets are set
- Check credentials have appropriate scopes
- Ensure token is not expired

#### Go Tests Timeout
- Increase timeout in workflow (default: 5m per job)
- Check for hanging tests
- Verify test coverage is not too comprehensive

#### Lint Warnings Block Merge
- Run `golangci-lint run ./...` locally
- Fix issues before pushing
- Update `.golangci.yml` for custom rules

---

## Container Image Usage

### Pull Latest Image

```bash
docker pull git.grainedlotus.com/grainedlotus515/gobard:latest
```

### Run Container

```bash
# With environment file
docker run -d \
  --name gobard \
  --env-file .env \
  git.grainedlotus.com/grainedlotus515/gobard:latest

# Or with individual env vars
docker run -d \
  --name gobard \
  -e DISCORD_TOKEN=your_token \
  -e YOUTUBE_API_KEY=your_key \
  git.grainedlotus.com/grainedlotus515/gobard:latest
```

### Use in Docker Compose

```yaml
version: '3.8'

services:
  gobard:
    image: git.grainedlotus.com/grainedlotus515/gobard:latest
    container_name: gobard
    restart: unless-stopped
    env_file: .env
    volumes:
      - ./cache:/app/cache
```

---

## Performance Optimization

### Cache Strategy

The workflows use GitHub Actions cache to speed up builds:

- **Go modules cache**: Cached between runs
- **Docker layer cache**: Persisted via GitHub Actions cache backend
- **Build time**: ~2-3 minutes for Docker image

### Parallel Jobs

Jobs run in parallel:
- Test and Lint jobs run simultaneously
- Build depends on both completing
- Security Scan runs after Build
- Notify runs last

### Optimization Tips

1. **Reduce test scope**: Mark integration tests with `// +build integration`
2. **Prune dependencies**: Run `go mod tidy` regularly
3. **Multi-stage Dockerfile**: Reduce final image size
4. **Cache warming**: Pre-pull base images on runner

---

## Security Considerations

### Secrets Management

- Registry credentials stored as encrypted secrets
- Never commit `.env` files or tokens
- Rotate tokens regularly (quarterly recommended)
- Audit secret access in Gitea logs

### Image Scanning

- Trivy scans all build artifacts
- Results available in Security tab
- Automatic CVE detection and reporting

### Access Control

Set branch protection rules:
1. Require all checks to pass
2. Require pull request review
3. Require conversation resolution

---

## Troubleshooting

### Debug Workflow

Enable debug logging in Gitea Actions:
1. Set secret: `ACTIONS_STEP_DEBUG=true`
2. Re-run workflow
3. View expanded logs in UI

### Local Testing

Run tests locally before pushing:

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run linting
golangci-lint run ./...

# Build Docker image
docker build -t gobard:test .
```

### Common Solutions

| Issue | Solution |
|-------|----------|
| Tests fail locally but pass in CI | Clear cache: `go clean -modcache` |
| Docker build takes too long | Check base image size, enable cache |
| Registry push fails | Verify credentials, check network access |
| Lint warnings inconsistent | Run locally with `golangci-lint` version from workflow |

---

## Next Steps

1. **Set up runner**: Configure Gitea Actions runner
2. **Add secrets**: Set registry credentials
3. **Test workflow**: Make a commit to trigger pipeline
4. **Monitor results**: Check Actions tab for status
5. **Iterate**: Adjust workflow based on results

---

## Resources

- [Gitea Actions Documentation](https://docs.gitea.com/usage/actions/overview)
- [GitHub Actions Syntax](https://docs.github.com/en/actions/using-workflows/workflow-syntax-for-github-actions)
- [Docker Build Action](https://github.com/docker/build-push-action)
- [Trivy Security Scanner](https://github.com/aquasecurity/trivy-action)

