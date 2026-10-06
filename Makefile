.PHONY: help docker-test docker-lint docker-build docker-bench docker-profile-check docker-run docker-run-medium docker-run-large docker-run-secrets docker-prod-run docker-prod-run-secrets docker-stop docker-logs docker-smoke clean

DOCKER ?= docker
COMPOSE ?= $(DOCKER) compose
DOCKER_IMAGE ?= gobard:local
LOCAL_COMPOSE = -f docker-compose.yml -f docker-compose.local.yml
MEDIUM_COMPOSE = -f docker-compose.yml -f docker-compose.medium.yml
LARGE_COMPOSE = -f docker-compose.yml -f docker-compose.large.yml

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Docker is the supported build and test environment because it includes libdave.'
	@echo ''
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

docker-test: ## Run the race-enabled Go test stage in Docker
	$(DOCKER) build --target test --progress=plain .

docker-lint: ## Run read-only formatting and vet checks in Docker
	$(DOCKER) build --target lint --progress=plain .

docker-build: ## Build the hardened linux/amd64 runtime image locally
	$(DOCKER) build --target runtime --platform linux/amd64 -t $(DOCKER_IMAGE) .

docker-bench: ## Run microbenchmarks and 15-minute capacity profiles in Docker
	$(DOCKER) build --target bench --progress=plain .
	./scripts/docker-bench.sh

docker-profile-check: ## Validate rendered small, medium, and large Compose resource profiles
	sh ./scripts/check-compose-profiles.sh $(COMPOSE)

docker-run: ## Build the checkout and start it with the local Compose override
	GOBARD_IMAGE=$(DOCKER_IMAGE) $(COMPOSE) $(LOCAL_COMPOSE) up -d --build

docker-run-medium: ## Build and start the checkout with the medium resource profile
	GOBARD_IMAGE=$(DOCKER_IMAGE) $(COMPOSE) $(LOCAL_COMPOSE) -f docker-compose.medium.yml up -d --build

docker-run-large: ## Build and start the checkout with the large resource profile
	GOBARD_IMAGE=$(DOCKER_IMAGE) $(COMPOSE) $(LOCAL_COMPOSE) -f docker-compose.large.yml up -d --build

docker-run-secrets: ## Start the checkout using DISCORD_TOKEN_FILE_HOST and a Compose secret
	GOBARD_IMAGE=$(DOCKER_IMAGE) $(COMPOSE) $(LOCAL_COMPOSE) -f docker-compose.secrets.yml up -d --build

docker-prod-run: ## Start the configured GHCR image without building locally
	$(COMPOSE) up -d

docker-prod-run-secrets: ## Start the GHCR image using DISCORD_TOKEN_FILE_HOST and a Compose secret
	$(COMPOSE) -f docker-compose.yml -f docker-compose.secrets.yml up -d

docker-stop: ## Stop the local Compose stack without deleting the cache
	GOBARD_IMAGE=$(DOCKER_IMAGE) $(COMPOSE) $(LOCAL_COMPOSE) down

docker-logs: ## Follow local Compose logs
	GOBARD_IMAGE=$(DOCKER_IMAGE) $(COMPOSE) $(LOCAL_COMPOSE) logs -f

docker-smoke: docker-build ## Verify the final image's runtime tools and permissions
	$(DOCKER) run --rm --read-only --tmpfs /tmp:rw,noexec,nosuid,size=128m --entrypoint /bin/sh $(DOCKER_IMAGE) -ec 'command -v ffmpeg; command -v yt-dlp; yt-dlp --version >/dev/null; command -v deno; deno eval --no-config --no-npm "0"; test ! -w /usr/local/bin/deno; python3 -c "import sys; sys.path.insert(0, \"/usr/local/bin/yt-dlp\"); import yt_dlp_ejs"; test -x /app/gobard; test ! -w /app/gobard; ! command -v curl; ! command -v pgrep; ldd /app/gobard | grep -q libdave'

clean: ## Remove Go build cache only; never delete the persisted audio cache
	@echo 'No project files were removed. Docker build caches are managed by Docker; ./cache is intentionally preserved.'
