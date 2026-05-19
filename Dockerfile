# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25.2
ARG DEBIAN_VERSION=trixie

# Build stage
FROM golang:${GO_VERSION}-${DEBIAN_VERSION} AS builder

# Install build dependencies and the libdave installer prerequisites.
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    ca-certificates \
    curl \
    git \
    libopus-dev \
    libopusfile-dev \
    libsodium-dev \
    pkg-config \
    unzip && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
COPY scripts/install-libdave.sh ./scripts/install-libdave.sh
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Install libdave before compiling the binary.
RUN --mount=type=cache,target=/go/pkg/mod \
    sh ./scripts/install-libdave.sh v1.1.0

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

# Install runtime dependencies including voice libraries.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    ffmpeg \
    libopus0 \
    libopusfile0 \
    libsodium23 && \
    rm -rf /var/lib/apt/lists/*

# Use the upstream yt-dlp release instead of Debian's package. YouTube
# extractor fixes are time-sensitive and distro packages often lag.
RUN curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux \
    -o /usr/local/bin/yt-dlp && \
    chmod 0755 /usr/local/bin/yt-dlp

# Create non-root user
RUN groupadd -g 1000 gobard && \
    useradd -m -u 1000 -g gobard gobard

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/gobard /app/gobard
COPY --from=builder /root/.local/lib/libdave.so /usr/local/lib/libdave.so

# Create cache directory
RUN mkdir -p /app/cache && chown -R gobard:gobard /app

ENV LD_LIBRARY_PATH=/usr/local/lib

# Switch to non-root user
USER gobard

# Expose any necessary ports (Discord bot doesn't need exposed ports)

CMD ["./gobard"]
