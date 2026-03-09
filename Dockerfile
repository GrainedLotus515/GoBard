# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25.2
ARG DEBIAN_VERSION=bookworm

# Build stage
FROM golang:${GO_VERSION}-${DEBIAN_VERSION} AS builder

# Install build dependencies and the libdave installer prerequisites.
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    ca-certificates \
    curl \
    git \
    libopus-dev \
    libsodium-dev \
    pkg-config \
    unzip && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Install libdave before compiling the binary.
RUN GODAVE_DIR=$(go list -m -f '{{.Dir}}' github.com/disgoorg/godave) && \
    sh "$GODAVE_DIR/scripts/libdave_install.sh" v1.1.0

ENV PKG_CONFIG_PATH=/root/.local/lib/pkgconfig
ENV LD_LIBRARY_PATH=/root/.local/lib

# Copy source code
COPY . .

# Build the application with CGO enabled for opus support
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o gobard ./cmd/gobard

# Runtime stage
FROM debian:${DEBIAN_VERSION}-slim

# Install runtime dependencies including voice libraries and yt-dlp
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    ffmpeg \
    libopus0 \
    libsodium23 \
    yt-dlp && \
    rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN groupadd -g 1000 gobard && \
    useradd -m -u 1000 -g gobard gobard

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/gobard /app/gobard
COPY --from=builder /root/.local/lib/libdave.so /root/.local/lib/libdave.so

# Create cache directory
RUN mkdir -p /app/cache && chown -R gobard:gobard /app

ENV LD_LIBRARY_PATH=/root/.local/lib

# Switch to non-root user
USER gobard

# Expose any necessary ports (Discord bot doesn't need exposed ports)

CMD ["./gobard"]
