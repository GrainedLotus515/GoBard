# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies including voice libraries
RUN apk add --no-cache \
    git \
    opus-dev \
    opusfile-dev \
    libsodium-dev \
    gcc \
    musl-dev \
    pkgconfig

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled for opus support
RUN CGO_ENABLED=1 GOOS=linux go build -o gobard ./cmd/gobard

# Runtime stage
FROM alpine:latest

# Install runtime dependencies including voice libraries and yt-dlp
RUN apk add --no-cache \
    ffmpeg \
    yt-dlp \
    ca-certificates \
    opus \
    opusfile \
    libsodium

# Create non-root user
RUN addgroup -g 1000 gobard && \
    adduser -D -u 1000 -G gobard gobard

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/gobard .

# Create cache directory
RUN mkdir -p /app/cache && chown -R gobard:gobard /app

# Switch to non-root user
USER gobard

# Expose any necessary ports (Discord bot doesn't need exposed ports)

CMD ["./gobard"]
