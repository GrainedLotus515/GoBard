# syntax=docker/dockerfile:1.7

# GoBard releases are intentionally linux/amd64 only.  Pin the exact platform
# manifests rather than mutable image tags so a rebuild has a reviewable base.
ARG BUILD_IMAGE=golang:1.25.12-trixie@sha256:09c6d487ccb96cac78767ef217cef33d15e9ee8c7569edbc9a3b00e3aef505d5
ARG RUNTIME_IMAGE=debian:trixie-slim@sha256:a617c1cdde36a7e0194b2f07dff669e1753c03c3205356b94f9f350b0f9a57d1

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

# libdave v1.1.0/cpp, Linux X64 BoringSSL release asset. The checksum is the
# digest published by GitHub for release asset 341735449.
ARG LIBDAVE_VERSION=v1.1.0/cpp
ARG LIBDAVE_SHA256=33157b8cbadcdd3c6cb4df3be18e8bbe7b86e3c65d57d0f1bc9dadc62a768d6a
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
		'Version: 1.1.0' \
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

FROM dependencies AS vulncheck

ARG GOVULNCHECK_VERSION=v1.1.4
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION} && \
	/go/bin/govulncheck ./...

FROM build-base AS lint-tools

ARG GOLANGCI_LINT_VERSION=2.12.2
ARG GOLANGCI_LINT_SHA256=8df580d2670fed8fa984aac0507099af8df275e665215f5c7a2ae3943893a553
RUN set -eux; \
	curl --fail --location --silent --show-error \
		"https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64.tar.gz" \
		-o /tmp/golangci-lint.tar.gz; \
	echo "${GOLANGCI_LINT_SHA256}  /tmp/golangci-lint.tar.gz" | sha256sum --check --status; \
	tar -xzf /tmp/golangci-lint.tar.gz -C /tmp; \
	install -m 0555 "/tmp/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64/golangci-lint" /usr/local/bin/golangci-lint; \
	rm -rf /tmp/golangci-lint.tar.gz "/tmp/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64"

# Fetch the extractor in a curl-equipped build stage. The runtime image stays
# smaller and does not retain a network transfer client solely for updates.
FROM build-base AS ytdlp

ARG YTDLP_VERSION=2026.07.04
ARG YTDLP_SHA256=6bbb3d314cde4febe36e5fa1d55462e29c974f63444e707871834f6d8cc210ae

RUN set -eux; \
	install -d /out; \
	curl --fail --location --silent --show-error \
		"https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp_linux" \
		-o /out/yt-dlp; \
	echo "${YTDLP_SHA256}  /out/yt-dlp" | sha256sum --check --status; \
	chmod 0555 /out/yt-dlp

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
	libsodium23 && \
	rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 1000 gobard && \
	useradd --uid 1000 --gid gobard --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin gobard && \
	install -d --owner=gobard --group=gobard --mode=0750 /app/cache && \
	ldconfig

WORKDIR /app
COPY --from=builder --chown=root:root /out/gobard /app/gobard
COPY --from=build-base --chown=root:root /opt/libdave/lib/libdave.so /usr/local/lib/libdave.so
COPY --from=ytdlp --chown=root:root /out/yt-dlp /usr/local/bin/yt-dlp

RUN chown root:root /app /app/gobard /usr/local/lib/libdave.so /usr/local/bin/yt-dlp && \
	chmod 0555 /app /app/gobard /usr/local/lib/libdave.so /usr/local/bin/yt-dlp && \
	ldconfig

# Keep tool caches and other transient state out of the read-only application
# filesystem. Compose mounts /tmp as a bounded noexec tmpfs.
ENV HOME=/tmp \
	XDG_CACHE_HOME=/tmp \
	LD_LIBRARY_PATH=/usr/local/lib \
	HEALTH_LISTEN_ADDR=127.0.0.1:8080

USER 1000:1000

# The healthcheck is implemented by cmd/gobard. It probes /ready without
# requiring curl or pgrep in the production image.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
	CMD ["/bin/sh", "-ec", "exec /app/gobard healthcheck --url \"http://${HEALTH_LISTEN_ADDR}/ready\""]

ENTRYPOINT ["/app/gobard"]
