.PHONY: help docker-test docker-lint docker-build docker-run docker-run-secrets docker-prod-run docker-prod-run-secrets docker-stop docker-logs docker-smoke clean

DOCKER ?= docker
COMPOSE ?= $(DOCKER) compose
DOCKER_IMAGE ?= gobard:local
LOCAL_COMPOSE = -f docker-compose.yml -f docker-compose.local.yml

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

docker-run: ## Build the checkout and start it with the local Compose override
	$(COMPOSE) $(LOCAL_COMPOSE) up -d --build

docker-run-secrets: ## Start the checkout using DISCORD_TOKEN_FILE_HOST and a Compose secret
	$(COMPOSE) $(LOCAL_COMPOSE) -f docker-compose.secrets.yml up -d --build

docker-prod-run: ## Start the configured GHCR image without building locally
	$(COMPOSE) up -d

docker-prod-run-secrets: ## Start the GHCR image using DISCORD_TOKEN_FILE_HOST and a Compose secret
	$(COMPOSE) -f docker-compose.yml -f docker-compose.secrets.yml up -d

docker-stop: ## Stop the local Compose stack without deleting the cache
	$(COMPOSE) $(LOCAL_COMPOSE) down

docker-logs: ## Follow local Compose logs
	$(COMPOSE) $(LOCAL_COMPOSE) logs -f

docker-smoke: docker-build ## Verify the final image's runtime tools and permissions
	$(DOCKER) run --rm --read-only --tmpfs /tmp:rw,noexec,nosuid,size=128m --entrypoint /bin/sh $(DOCKER_IMAGE) -ec 'command -v ffmpeg; command -v yt-dlp; yt-dlp --version >/dev/null; test -x /app/gobard; test ! -w /app/gobard; ! command -v curl; ! command -v pgrep; ldd /app/gobard | grep -q libdave'

clean: ## Remove Go build cache only; never delete the persisted audio cache
	@echo 'No project files were removed. Docker build caches are managed by Docker; ./cache is intentionally preserved.'
