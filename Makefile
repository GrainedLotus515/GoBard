.PHONY: build run clean test docker-build docker-run help

# Binary name
BINARY_NAME=gobard
DOCKER_IMAGE=gobard

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the binary
	$(GOBUILD) -o $(BINARY_NAME) ./cmd/gobard

run: ## Run the application
	$(GOCMD) run ./cmd/gobard

clean: ## Remove binary and cache
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -rf cache/

test: ## Run tests
	$(GOTEST) -v ./...

deps: ## Download dependencies
	$(GOMOD) download
	$(GOMOD) tidy

fmt: ## Format code
	$(GOCMD) fmt ./...

vet: ## Run go vet
	$(GOCMD) vet ./...

lint: fmt vet ## Run linters

docker-build: ## Build Docker image
	docker build -t $(DOCKER_IMAGE) .

docker-run: ## Run Docker container
	docker-compose up -d

docker-stop: ## Stop Docker container
	docker-compose down

docker-logs: ## Show Docker logs
	docker-compose logs -f

install-tools: ## Install development tools
	@echo "Installing yt-dlp..."
	@command -v yt-dlp >/dev/null 2>&1 || pip3 install yt-dlp
	@echo "Checking FFmpeg..."
	@command -v ffmpeg >/dev/null 2>&1 || echo "Please install FFmpeg manually"

all: clean deps lint build ## Clean, download deps, lint, and build
