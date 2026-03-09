#!/bin/sh
set -eu

VERSION="${1:-v1.1.0}"

if ! command -v go >/dev/null 2>&1; then
	echo "go is required to locate the GoDave installer" >&2
	exit 1
fi

go mod download github.com/disgoorg/godave >/dev/null

GODAVE_VERSION="$(go list -m -f '{{.Version}}' github.com/disgoorg/godave)"
GOMODCACHE="$(go env GOMODCACHE)"
GODAVE_DIR="${GOMODCACHE}/github.com/disgoorg/godave@${GODAVE_VERSION}"
if [ -z "${GODAVE_VERSION}" ] || [ ! -d "${GODAVE_DIR}" ]; then
	echo "github.com/disgoorg/godave is not available in the module cache" >&2
	exit 1
fi

SHELL="${SHELL:-/bin/sh}" NON_INTERACTIVE=1 sh "${GODAVE_DIR}/scripts/libdave_install.sh" "${VERSION}"

echo
echo "libdave installed."
echo "Set PKG_CONFIG_PATH=\"\$HOME/.local/lib/pkgconfig:\$PKG_CONFIG_PATH\" before building."
echo "Set LD_LIBRARY_PATH=\"\$HOME/.local/lib:\$LD_LIBRARY_PATH\" when running tests or binaries outside Docker."
