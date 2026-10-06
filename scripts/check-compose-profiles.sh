#!/bin/sh
set -eu

# Supply a disposable project directory so all Compose versions can resolve
# env_file without requiring or reading a contributor's private .env file.
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
profile_dir=$(mktemp -d)
trap 'rm -rf "$profile_dir"' EXIT
trap 'exit 1' HUP INT TERM
: > "$profile_dir/.env"

if [ "$#" -eq 0 ]; then
	set -- docker compose
fi

unset GOBARD_CPUS GOBARD_MEMORY_LIMIT GOBARD_PIDS_LIMIT
unset GOBARD_PROFILE_CACHE_LIMIT GOBARD_PROFILE_YTDLP_MAX_CONCURRENCY
unset CACHE_LIMIT YTDLP_MAX_CONCURRENCY
GOBARD_IMAGE=ghcr.io/grainedlotus515/gobard@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export GOBARD_IMAGE

for profile in small medium large; do
	case "$profile" in
		small) cpus=2; memory=1073741824; cache=2GB; concurrency=4 ;;
		medium) cpus=4; memory=2147483648; cache=10GB; concurrency=8 ;;
		large) cpus=8; memory=4294967296; cache=25GB; concurrency=12 ;;
	esac
	if [ "$profile" = small ]; then
		"$@" --project-directory "$profile_dir" --env-file "$profile_dir/.env" \
			-f "$repo_dir/docker-compose.yml" config > "$profile_dir/rendered.yml"
	else
		"$@" --project-directory "$profile_dir" --env-file "$profile_dir/.env" \
			-f "$repo_dir/docker-compose.yml" -f "$repo_dir/docker-compose.$profile.yml" \
			config > "$profile_dir/rendered.yml"
	fi
	grep -Eq "cpus: $cpus$" "$profile_dir/rendered.yml"
	grep -Eq "mem_limit: \"?$memory\"?$" "$profile_dir/rendered.yml"
	grep -Eq "CACHE_LIMIT: $cache$" "$profile_dir/rendered.yml"
	grep -Eq "YTDLP_MAX_CONCURRENCY: \"$concurrency\"$" "$profile_dir/rendered.yml"
	grep -Eq 'pids_limit: 256$' "$profile_dir/rendered.yml"
	test "$(grep -Fc "image: $GOBARD_IMAGE" "$profile_dir/rendered.yml")" -eq 2
	printf '%s profile passed\n' "$profile"
done

"$@" --project-directory "$profile_dir" --env-file "$profile_dir/.env" \
	-f "$repo_dir/docker-compose.bench.yml" config --quiet

if env -u GOBARD_IMAGE "$@" --project-directory "$profile_dir" --env-file "$profile_dir/.env" \
	-f "$repo_dir/docker-compose.yml" config > "$profile_dir/missing-image.log" 2>&1; then
	echo 'Compose must reject a missing GOBARD_IMAGE' >&2
	exit 1
fi
grep -q 'GOBARD_IMAGE' "$profile_dir/missing-image.log"
echo 'Missing image rejection passed'
