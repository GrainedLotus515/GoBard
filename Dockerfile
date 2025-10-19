# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies including voice libraries
RUN apk add --no-cache \
    git \
    opus-dev \
    libsodium-dev \
    gcc \
    musl-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o gobard ./cmd/gobard

# Runtime stage
FROM alpine:latest

# Install runtime dependencies including voice libraries
RUN apk add --no-cache \
    ffmpeg \
    python3 \
    py3-pip \
    ca-certificates \
    opus \
    libsodium

# Install yt-dlp
RUN pip3 install --no-cache-dir yt-dlp

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
