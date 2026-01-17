# syntax=docker/dockerfile:1

ARG ALPINE_VERSION=3.21
ARG GO_VERSION=1.25

# Build stage
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# Install build dependencies
RUN apk add --no-cache \
    git \
    opus-dev \
    opusfile-dev \
    libsodium-dev \
    gcc \
    musl-dev \
    pkgconfig

WORKDIR /app

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./

# Download and verify dependencies with cache mount
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download && go mod verify

# Copy source code
COPY . .

# Build with all optimizations
# -buildvcs=false: skip git info embedding (faster + security)
# -trimpath: remove file paths from binary (reproducibility + security)
# -ldflags="-s -w": strip debug info and symbol table (smaller binary)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build \
    -buildvcs=false \
    -ldflags="-s -w" \
    -trimpath \
    -o gobard ./cmd/gobard

# Runtime stage
FROM alpine:${ALPINE_VERSION}

LABEL org.opencontainers.image.title="GoBard" \
      org.opencontainers.image.description="Discord music bot written in Go" \
      org.opencontainers.image.source="https://github.com/GrainedLotus515/gobard"

# Install runtime deps + security hardening in single layer
RUN apk add --no-cache \
        dumb-init \
        ffmpeg \
        yt-dlp \
        ca-certificates \
        opus \
        opusfile \
        libsodium && \
    # Create non-root user with high UID (avoid host collision)
    # -H: no home directory, -s /sbin/nologin: no shell access
    addgroup -g 10001 gobard && \
    adduser -D -u 10001 -G gobard -H -s /sbin/nologin gobard && \
    # Setup app directory
    mkdir -p /app/cache && \
    chown -R gobard:gobard /app && \
    # Security: remove setuid/setgid binaries to prevent privilege escalation
    find / -perm /6000 -type f -exec chmod a-s {} \; 2>/dev/null || true

WORKDIR /app

# Copy binary from builder
COPY --from=builder --chown=gobard:gobard /app/gobard .

# Switch to non-root user
USER gobard:gobard

# Environment defaults
# GOTRACEBACK=single: limit stack trace exposure in production
ENV CACHE_DIR=/app/cache \
    GOTRACEBACK=single

# Use dumb-init for proper signal handling (PID 1 zombie reaping)
ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["./gobard"]
