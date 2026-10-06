# syntax=docker/dockerfile:1.27.1@sha256:7b32114e84ca21aeb2d6f871a0b62b3a0762193f75d9afd048ee4cbe889f3148

# GoBard releases are intentionally linux/amd64 only.  Pin the exact platform
# manifests rather than mutable image tags so a rebuild has a reviewable base.
ARG BUILD_IMAGE=golang:1.27.1-trixie@sha256:7bffdb405cd12940d2980daa49a86ef575ed4525a17ee7d0c9562547357ab46a
ARG RUNTIME_IMAGE=debian:trixie-slim@sha256:918311b7b6c4c6f68b232ba516584925f6c78ad82b6fd534b98979df6438e483

FROM --platform=linux/amd64 ${BUILD_IMAGE} AS build-base

RUN apt-get update && apt-get install -y --no-install-recommends \
	build-essential \
	ca-certificates \
	curl \
	libopus-dev \
	libopusfile-dev \
	libsodium-dev \
	pkg-config \
	unzip && \
	rm -rf /var/lib/apt/lists/*

# libdave v1.2.1/cpp, Linux X64 BoringSSL release asset. The checksum is the
# digest published by GitHub for release asset 582439092.
ARG LIBDAVE_VERSION=v1.2.1/cpp
ARG LIBDAVE_SHA256=7fdf10a7df894406291463130a322c04d7de58085b602500866a11874e8907b4
RUN set -eux; \
	curl --fail --location --silent --show-error \
		"https://github.com/discord/libdave/releases/download/${LIBDAVE_VERSION}/libdave-Linux-X64-boringssl.zip" \
		-o /tmp/libdave.zip; \
	echo "${LIBDAVE_SHA256}  /tmp/libdave.zip" | sha256sum --check --status; \
	install -d /opt/libdave/include /opt/libdave/lib/pkgconfig; \
	unzip -j /tmp/libdave.zip include/dave/dave.h -d /opt/libdave/include; \
	unzip -j /tmp/libdave.zip lib/libdave.so -d /opt/libdave/lib; \
	printf '%s\n' \
		'prefix=/opt/libdave' \
		'exec_prefix=${prefix}' \
		'libdir=${exec_prefix}/lib' \
		'includedir=${prefix}/include' \
		'' \
		'Name: dave' \
		'Description: Discord Audio & Video End-to-End Encryption (DAVE) Protocol' \
		'Version: 1.2.1' \
		'Libs: -L${libdir} -ldave -Wl,-rpath,${libdir}' \
		'Cflags: -I${includedir}' \
		> /opt/libdave/lib/pkgconfig/dave.pc; \
	rm -f /tmp/libdave.zip

ENV PKG_CONFIG_PATH=/opt/libdave/lib/pkgconfig
ENV LD_LIBRARY_PATH=/opt/libdave/lib

FROM build-base AS dependencies

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
	go mod download

FROM dependencies AS test

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	go test -race ./...

FROM dependencies AS bench

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	go test -run '^$' -bench . -benchmem \
		./internal/player ./internal/cache ./internal/processlimit ./internal/bot ./internal/youtube

FROM dependencies AS capacity-bench-builder

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -o /out/gobard-capacity-bench ./cmd/capacitybench

FROM dependencies AS vulncheck

ARG GOVULNCHECK_VERSION=v1.8.0
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION} && \
	/go/bin/govulncheck ./...

FROM build-base AS lint-tools

ARG GOLANGCI_LINT_VERSION=2.14.0
ARG GOLANGCI_LINT_SHA256=ab90aeb7b066f92a33415b638a50fe5344bbb75a0d32ad30cc248d88f81032ab
RUN set -eux; \
	curl --fail --location --silent --show-error \
		"https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64.tar.gz" \
		-o /tmp/golangci-lint.tar.gz; \
	echo "${GOLANGCI_LINT_SHA256}  /tmp/golangci-lint.tar.gz" | sha256sum --check --status; \
	tar -xzf /tmp/golangci-lint.tar.gz -C /tmp; \
	install -m 0555 "/tmp/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64/golangci-lint" /usr/local/bin/golangci-lint; \
	rm -rf /tmp/golangci-lint.tar.gz "/tmp/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64"

# Fetch the portable Python zipapp in a curl-equipped build stage. Do not use
# the PyInstaller yt-dlp_linux asset: it extracts shared libraries to /tmp and
# cannot run with the production noexec tmpfs hardening.
FROM build-base AS ytdlp

ARG YTDLP_VERSION=2026.08.19
ARG YTDLP_SHA256=1fa6733c37ea6fb51c99ad8fe785e7b7e5f3246c9b980230329d4fb72ed8d4d6

RUN set -eux; \
	install -d /out; \
	curl --fail --location --silent --show-error \
		"https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp" \
		-o /out/yt-dlp; \
	echo "${YTDLP_SHA256}  /out/yt-dlp" | sha256sum --check --status; \
	chmod 0555 /out/yt-dlp

# YouTube's EJS challenges require a JavaScript runtime. The yt-dlp zipapp
# bundles the solver scripts and discovers Deno on PATH without remote downloads.
FROM build-base AS deno

ARG DENO_VERSION=2.9.7
ARG DENO_SHA256=c6527f24f4b16031d3ae4fa9f658d5f11534c8d84ce7dc8502420280919c3490
RUN set -eux; \
	install -d /out; \
	curl --fail --location --silent --show-error \
		"https://github.com/denoland/deno/releases/download/v${DENO_VERSION}/deno-x86_64-unknown-linux-gnu.zip" \
		-o /tmp/deno.zip; \
	echo "${DENO_SHA256}  /tmp/deno.zip" | sha256sum --check --status; \
	unzip -j /tmp/deno.zip deno -d /out; \
	chmod 0555 /out/deno; \
	rm -f /tmp/deno.zip

FROM dependencies AS lint

COPY --from=lint-tools /usr/local/bin/golangci-lint /usr/local/bin/golangci-lint
COPY . .
RUN set -eux; \
	test -z "$(gofmt -l $(find . -name '*.go' -not -path './vendor/*'))"; \
	go vet ./...; \
	golangci-lint run --timeout=5m

FROM dependencies AS builder

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags='-s -w' -o /out/gobard ./cmd/gobard

FROM --platform=linux/amd64 ${RUNTIME_IMAGE} AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
	ca-certificates \
	ffmpeg \
	libopus0 \
	libopusfile0 \
	libsodium23 \
	python3 && \
	rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 1000 gobard && \
	useradd --uid 1000 --gid gobard --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin gobard && \
	install -d --owner=gobard --group=gobard --mode=0750 /app/cache && \
	ldconfig

WORKDIR /app
COPY --from=builder --chown=root:root /out/gobard /app/gobard
COPY --from=build-base --chown=root:root /opt/libdave/lib/libdave.so /usr/local/lib/libdave.so
COPY --from=ytdlp --chown=root:root /out/yt-dlp /usr/local/bin/yt-dlp
COPY --from=deno --chown=root:root /out/deno /usr/local/bin/deno

RUN chown root:root /app /app/gobard /usr/local/lib/libdave.so /usr/local/bin/yt-dlp /usr/local/bin/deno && \
	chmod 0555 /app /app/gobard /usr/local/lib/libdave.so /usr/local/bin/yt-dlp /usr/local/bin/deno && \
	ldconfig

# Keep tool caches and other transient state out of the read-only application
# filesystem. Compose mounts /tmp as a bounded noexec tmpfs.
ENV HOME=/tmp \
	XDG_CACHE_HOME=/tmp \
	DENO_DIR=/tmp/deno-cache \
	DENO_NO_UPDATE_CHECK=1 \
	LD_LIBRARY_PATH=/usr/local/lib \
	HEALTH_LISTEN_ADDR=127.0.0.1:8080

USER 1000:1000

# The healthcheck is implemented by cmd/gobard. It probes /ready without
# requiring curl or pgrep in the production image.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
	CMD ["/bin/sh", "-ec", "exec /app/gobard healthcheck --url \"http://${HEALTH_LISTEN_ADDR}/ready\""]

ENTRYPOINT ["/app/gobard"]

FROM runtime AS capacity-bench

COPY --from=capacity-bench-builder --chown=root:root --chmod=0555 /out/gobard-capacity-bench /app/gobard-capacity-bench
ENTRYPOINT ["/app/gobard-capacity-bench"]

# Keep the hardened bot runtime as the default Dockerfile result. The capacity
# runner is build-only and selected explicitly by docker-compose.bench.yml.
FROM runtime AS final
