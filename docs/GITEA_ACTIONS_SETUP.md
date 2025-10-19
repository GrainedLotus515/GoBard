# Gitea Actions Setup Guide

This guide will help you set up Gitea Actions CI/CD for the GoBard project.

## Prerequisites

- Gitea instance with Actions enabled
- A Gitea Actions runner configured and running
- Docker registry credentials (for pushing container images)
- Repository access with admin/maintainer permissions

## Step 1: Enable Gitea Actions on Runners

### Check Runner Status

1. Go to your Gitea instance admin panel
2. Navigate to "Site Administration" → "Actions"
3. Verify the runner is online and idle

### Configure Runner

If you don't have a runner, follow the Gitea documentation to set one up:

```bash
# On your runner machine
# Download and run the Gitea runner
wget https://gitea.com/gitea/act_runner/releases/download/v0.x.x/act_runner-linux-amd64
chmod +x act_runner-linux-amd64

# Register runner
./act_runner-linux-amd64 register --gitea-url https://git.grainedlotus.com --instance-name runner1

# Run the runner
./act_runner-linux-amd64 daemon
```

## Step 2: Set Repository Secrets

Secrets are used to store sensitive credentials like registry credentials.

### Add Registry Credentials

1. Go to your repository on Gitea
2. Click **Settings** → **Secrets** (or **Actions Secrets**)
3. Click **Add Secret**

**Add these secrets:**

#### Secret 1: REGISTRY_USERNAME
```
Key: REGISTRY_USERNAME
Value: your_gitea_username
```

#### Secret 2: REGISTRY_PASSWORD
```
Key: REGISTRY_PASSWORD
Value: your_gitea_token_or_password
```

**To create a Gitea Personal Access Token:**

1. Click your **Avatar** (top-right)
2. Go to **Settings** → **Applications** → **Personal Access Tokens**
3. Click **Generate Token**
4. Give it a name like "Docker Registry"
5. Select scopes: `write:package`, `read:package`
6. Click **Generate Token**
7. Copy the token and paste into the secret

### Verify Secrets

```bash
# Check secrets are set (in CI logs, they'll be masked)
# You should see [REDACTED] in logs when secrets are used
```

## Step 3: Verify Workflow Files

Check that workflow files are present:

```bash
# List workflows
ls -la .gitea/workflows/

# Expected files:
# - docker-build.yml
# - go-test.yml
```

## Step 4: Test the Pipeline

### Trigger First Workflow Run

```bash
# Make a small change and push
git add .
git commit -m "test: trigger CI/CD pipeline"
git push origin main
```

### Monitor in Gitea UI

1. Go to repository
2. Click **Actions** tab
3. You should see your workflow run
4. Click on the run to view details
5. Click on a job to see logs

### Expected First Run

**go-test.yml should:**
- ✅ Checkout code
- ✅ Run tests (may fail if dependencies missing)
- ✅ Run linting
- ✅ Build binary

**docker-build.yml should:**
- ✅ Checkout code
- ✅ Build Docker image
- ✅ Run security scan
- ⏭️ Skip push (needs credentials first)

## Step 5: Configure Branch Protection

Require CI checks before merging:

1. Go to repository **Settings** → **Protected Branches**
2. Click **Add Branch** (select `main`)
3. Enable:
   - ✅ Protect this branch
   - ✅ Require pull request reviews
   - ✅ Require successful CI checks
4. Select required checks:
   - go-test (all versions)
   - docker-build

## Step 6: Troubleshooting

### Runner Shows as "Offline"

**Problem:** Actions don't run because runner is offline

**Solution:**
```bash
# SSH into runner machine
ssh runner-machine

# Check if runner is running
ps aux | grep act_runner

# Restart runner if needed
systemctl restart act_runner
# or
./act_runner-linux-amd64 daemon
```

### Secrets Not Working

**Problem:** Image push fails with "unauthorized"

**Solution:**
1. Verify secrets are set in repository settings
2. Check token hasn't expired
3. Verify username matches token owner
4. Try regenerating token

### Docker Build Fails

**Problem:** "Docker not found" error

**Solution:**
1. SSH into runner machine
2. Verify Docker is installed: `docker --version`
3. Verify runner user can access Docker: `groups $(whoami)`
4. If needed, add user to docker group: `sudo usermod -aG docker $USER`

### Builds Take Too Long

**Problem:** First build takes 10+ minutes

**Solution:**
- First build downloads dependencies and builds cache
- Subsequent builds use cache (should be 2-3 minutes)
- Can be sped up by pre-pulling base images

### Registry Authentication Failed

**Problem:** Docker push fails with authentication error

**Solution:**
```bash
# Test credentials locally
docker login -u USERNAME -p TOKEN git.grainedlotus.com

# If that fails, check:
# 1. Token hasn't expired
# 2. User has write:package scope
# 3. Registry URL is correct
```

## Step 7: Customize Workflows (Optional)

### Add Slack Notifications

Add to `docker-build.yml`:

```yaml
- name: Notify Slack on success
  if: success()
  uses: 8398a7/action-slack@v3
  with:
    status: ${{ job.status }}
    text: 'Docker image built and pushed successfully!'
    webhook_url: ${{ secrets.SLACK_WEBHOOK }}

- name: Notify Slack on failure
  if: failure()
  uses: 8398a7/action-slack@v3
  with:
    status: ${{ job.status }}
    text: 'Docker build failed!'
    webhook_url: ${{ secrets.SLACK_WEBHOOK }}
```

Then add `SLACK_WEBHOOK` secret.

### Add Automated Releases

Create `.gitea/workflows/release.yml`:

```yaml
name: Create Release

on:
  push:
    tags:
      - 'v*'

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Create Release
        uses: actions/create-release@v1
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        with:
          tag_name: ${{ github.ref }}
          release_name: Release ${{ github.ref }}
          draft: false
          prerelease: false
```

### Modify Build Matrix

Change Go versions in `go-test.yml`:

```yaml
strategy:
  matrix:
    go-version: ['1.24', '1.25'] # Add more versions here
```

## Monitoring and Alerts

### View Workflow Runs

1. Repository → **Actions** tab
2. Click workflow name to filter
3. Click specific run for details
4. Click job for step-by-step logs

### Download Artifacts

1. Go to workflow run
2. Scroll to "Artifacts" section
3. Click to download (e.g., `gobard-binary`)

### View Build Status Badge

Add to README.md:

```markdown
[![Build Status](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions/workflows/docker-build.yml/badge.svg)](https://git.grainedlotus.com/GrainedLotus515/GoBard/actions)
```

## Advanced Configuration

### Run Tests in Parallel

Already configured! Multiple Go versions run simultaneously.

### Skip Workflow for Certain Commits

In commit message, add one of:
```
[skip ci]
[ci skip]
skip-actions
```

Example:
```bash
git commit -m "docs: update README [skip ci]"
```

### Matrix Builds for Multiple Configurations

Extend `go-test.yml` with more matrix variables:

```yaml
strategy:
  matrix:
    go-version: ['1.24', '1.25']
    os: [ubuntu-latest, macos-latest]
```

## Next Steps

1. ✅ Verify runner is online
2. ✅ Set registry credentials
3. ✅ Push test commit to trigger workflows
4. ✅ Monitor build in Actions tab
5. ✅ Fix any failures
6. ✅ Enable branch protection
7. ✅ Celebrate! 🎉

## Resources

- [Gitea Actions](https://docs.gitea.com/usage/actions/overview)
- [GitHub Actions (Compatible)](https://docs.github.com/en/actions)
- [Docker Build Action](https://github.com/docker/build-push-action)
- [GolangCI-Lint](https://golangci-lint.run/)

## Support

If you encounter issues:

1. Check workflow logs in Actions tab
2. Review this guide's troubleshooting section
3. Check Gitea instance logs: `journalctl -u gitea`
4. Ask in Gitea community forums

