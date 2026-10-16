#!/bin/sh
# Installed as yt-dlp only in the offline capacity-bench image. No networking,
# arguments or external URLs are interpreted. The benchmark owns this fixture.
set -eu
exec cat -- "${GOBARD_BENCH_MEDIA:?benchmark fixture required}"
