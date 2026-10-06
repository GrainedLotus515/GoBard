#!/bin/sh
set -eu

project="gobard-capacity-bench"
compose_file="docker-compose.bench.yml"

cleanup() {
	docker compose -p "$project" -f "$compose_file" down --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker build --target capacity-bench -t gobard-capacity-bench:local .
docker compose -p "$project" -f "$compose_file" up --detach --force-recreate --no-build

status=0
for service in bench-small bench-medium bench-large; do
	container=$(docker compose -p "$project" -f "$compose_file" ps --all --quiet "$service")
	code=$(docker wait "$container")
	docker logs "$container"
	if [ "$code" -ne 0 ]; then
		status=1
	fi
done
exit "$status"
