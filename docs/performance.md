# Performance and budgets

## November 1 review-fix evidence

The new [quality-fix report](rewrite/quality-fixes-2026-11-01.md) preserves the
baseline regressions, short-profile comparisons, a failed long-source stress
probe and admitted-capacity high-bitrate runs. Runtime admission now uses visible
memory and extractor capacity; historical 4/12/24-guild fixture counts below are
stress workloads, not the runtime's admission promise. The benchmark supports
`GOBARD_BENCH_ADMISSION=runtime`, `GOBARD_BENCH_INPUT_KBPS=256` and
`GOBARD_BENCH_MEDIA_DURATION=20m` to exercise cold backpressure with a fixture
larger than the 8 MiB ring. Its default `stress` mode retains fixed profile counts
for like-for-like historical checks. Both modes use real acquisition/codec/pacing
and explicitly fake provider/voice endpoints.
## Historical release-completion capacity — 2026-10-27

The October 27 candidate derived admission from the effective cgroup v1/v2 or host memory limit,
with an optional `PLAYBACK_MEMORY_LIMIT` operator override. It reserves the
greater of 256 MiB or 25% of that limit (`PLAYBACK_MEMORY_HEADROOM` can override
it), then budgets 48 MiB per retained guild. The effective admission limit is the
smaller of this result and `MAX_GUILD_SESSIONS`; idle and disconnected sessions
retain their reservations. The 48 MiB value is deliberately above the highest
previous 33.32 MiB incremental acquisition measurement, not a claim that every
provider/process workload has a fixed per-stream cost. Changes require restart.

A 30-second concurrent four-profile run used the same i9-12900K/24-logical-CPU
host and Linux 6.18.57, with unrelated host workloads present. The reduced profile
derived five guilds under 512 MiB; all profiles reported zero gaps over 60 ms,
worker failures and retained temporary files, with goroutines returning from two
to one.

| Profile | Guilds | Memory limit | CPU / quota | Peak anonymous MiB | Added anonymous MiB/guild | Maximum gap ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Reduced | 5 | 512 MiB | 11.41% | 62.44 | 12.17 | 21.82 |
| Small | 4 | 1 GiB | 9.21% | 50.89 | 12.33 | 21.74 |
| Medium | 12 | 2 GiB | 13.21% | 143.28 | 11.81 | 22.49 |
| Large | 24 | 4 GiB | 12.65% | 282.09 | 11.69 | 21.20 |

The workload includes the real acquisition ring, local extractor process tree,
FFmpeg/libopus, cache pressure and paced fake voice. This is short offline
evidence, not live-provider, cold-YouTube, DAVE or listener-quality proof. See
[raw release-short results](rewrite/capacity-release-short-2026-10-27.json).

`docker build --no-cache --target bench --progress=plain .` then passed on the
same host before the long soak. Single-run samples measured limiter acquisition
at 72.78 ns, 32 B/two allocations; cache acquire at 607.4 ns, 352 B/three
allocations; a synthetic 1,000-frame attempt at 86,974 ns, 4,184 B/four
allocations; and real 1,000-frame media at 219,035,683 ns, 17.53 MB/s, 118,835
Go B/92 allocations. These are reference samples, not controlled optimization
comparisons or total-process memory measurements.

`sh ./scripts/docker-bench.sh` then ran all four profiles concurrently for 900
seconds and exited zero. No GoBard bot or other GoBard build overlapped the
profile measurements; unrelated host workloads remained active. Every profile
reported zero missed 60 ms deadlines, worker failures and retained temporary
files, with goroutines returning from two to one.

| Profile | Guilds | Frames | CPU / quota | Peak cgroup MiB | Peak anonymous MiB | Added anonymous MiB/guild | Maximum gap ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Reduced | 5 | 224,961 | 16.18% | 160.36 | 140.93 | 27.87 | 51.72 |
| Small | 4 | 179,992 | 12.48% | 143.73 | 113.75 | 28.04 | 24.61 |
| Medium | 12 | 539,953 | 17.40% | 314.73 | 288.09 | 23.87 | 25.55 |
| Large | 24 | 1,079,805 | 17.17% | 565.52 | 525.85 | 21.84 | 51.00 |

Maximum first-frame latencies were 199.52/42.22/112.09/208.98 ms and maximum
session shutdowns were 5.02/10.41/19.17/9.62 ms in profile order. The reduced
and large maxima remain below the threshold but show that tail timing varies on
this non-idle host; the earlier failed soak remains relevant evidence. The
benchmark image was
`sha256:c780e53c330b01d28fc2da4157bed938fa302355a394d6f67f4f214b6a022a7b`.
See [raw final soak results](rewrite/capacity-release-soak-2026-10-27.json) for
all counters, durations, limits and caveats. This is sustained offline evidence,
not provider, Discord/DAVE, listening-quality or general streams-per-GB proof.

## Recovery-audit regression — 2026-10-26

The October 26 audit tree was measured with the pinned Go 1.27.3
linux/amd64 toolchain on the same i9-12900K host (24 logical CPUs, 33,388,384,256
bytes RAM, Linux 6.18.57). Microbenchmarks completed first; the 30-second 4/12/24
profiles then ran concurrently with their configured quotas. No GoBard bot or
quality build overlapped the profiles; unrelated host containers and interactive
desktop/game workloads remained active, so this was not a dedicated idle host.
The workload and limitations are the same as the acquisition soak below, with the
obsolete synthetic process-priority pressure removed. Cache-pressure work and the
shared process budget still execute, and provider retrieval uses a real local
extractor subprocess rather than YouTube.

| Guilds | Frames | CPU quota | CPU / quota | Peak anonymous MiB | Added anonymous MiB/guild | Maximum gap ms | Maximum session shutdown ms |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 | 5,992 | 2 | 11.07% | 50.87 | 12.35 | 21.81 | 4.28 |
| 12 | 17,960 | 4 | 15.81% | 142.92 | 11.78 | 22.91 | 25.44 |
| 24 | 35,858 | 8 | 15.58% | 281.42 | 11.66 | 50.27 | 28.23 |

All three profiles reported zero gaps over 60 ms, worker failures and retained
temporary files; goroutines returned from two to one. Maximum first-frame
latencies were 52.27/96.88/158.14 ms. The large profile's 50.27 ms maximum gap
stayed below the failure threshold but is not evidence of listener-perceived
quality. See [raw short results](rewrite/capacity-short-2026-10-26.json).

Single-run microbenchmarks measured limiter acquisition at 76.53 ns, 32 B/two
allocations; cache acquire at 604.1 ns, 352 B/three allocations; and a synthetic
1,000-frame attempt at 89,096 ns, 4,168 B/four allocations. Real 1,000-frame media
throughput was 198,146,996 ns, 19.38 MB/s, 118,712 Go B/92 allocations. These are
current reference samples, not controlled before/after optimization claims.

The first 15-minute repeat used the same host and concurrent-profile conditions
with no overlapping GoBard build or bot. It **failed the pacing gate**: medium
reported one gap over 60 ms and large reported two. Small passed. CPU, memory,
worker, cleanup and temporary-file checks passed in all profiles, and goroutines
returned from two to one.

| Guilds | Frames | CPU / quota | Peak anonymous MiB | Gaps over 60 ms | Maximum gap ms | p99 gap ms |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 | 179,992 | 11.53% | 113.17 | 0 | 24.17 | 20 |
| 12 | 539,815 | 15.19% | 287.12 | 1 | 60.17 | 21 |
| 24 | 1,079,850 | 15.11% | 523.69 | 2 | 63.06 | 21 |

The threshold exceedances are marginal and rare, but their cause is unattributed;
ordinary p99 values do not erase a failed tail-latency assertion. Unrelated host
containers and interactive desktop/game workloads remained active, so this was
not an idle host and the run cannot distinguish host scheduling from an
application regression. The failed evidence is retained in
[raw soak results](rewrite/capacity-soak-2026-10-26.json), and no capacity pass
or listener-quality claim is made from it.

An unchanged second 15-minute run then exited zero. Every profile reported zero
missed deadlines, worker failures and retained temporary files, with goroutines
returning from two to one.

| Guilds | Frames | CPU / quota | Peak anonymous MiB | Gaps over 60 ms | Maximum gap ms | p99 gap ms |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 | 179,992 | 10.54% | 113.38 | 0 | 22.22 | 21 |
| 12 | 539,964 | 14.27% | 287.41 | 0 | 23.99 | 20 |
| 24 | 1,079,849 | 13.21% | 522.49 | 0 | 58.97 | 20 |

The passing repeat used the same non-idle host conditions and workload. Its
large-profile maximum remained close to the threshold. One run failed and one
passed; the marginal timing variability remains unattributed, so these runs are
not a statistically controlled capacity guarantee. See
[raw repeat results](rewrite/capacity-soak-repeat-2026-10-26.json) for exact
values and image identity. Controlled profiling on an idle host is the next
diagnostic step if tighter tail-latency evidence is required.

## Acquisition soak — 2026-10-16

The updated `make docker-bench` passed all three 900-second profiles. The actual
YouTube opener consumes a benchmark-only local extractor into its 8 MiB ring,
then real FFmpeg/libopus and the paced fake voice output. Generated input is
64 kbit/s constant-rate stereo Opus; output remains 128 kbit/s. No provider or
Discord connection is made. The runtime image retains real yt-dlp; only the
capacity image replaces it with the fixed local fixture script.

| Guilds | CPU quota | CPU / quota | Peak anonymous MiB | Added anonymous MiB/guild | Maximum gap ms | Maximum session shutdown ms |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 | 2 | 7.73% | 139.96 | 33.32 | 27.69 | 13.05 |
| 12 | 4 | 10.02% | 383.15 | 31.37 | 29.92 | 32.35 |
| 24 | 8 | 9.63% | 702.27 | 28.98 | 39.13 | 20.01 |

All had zero gaps over 60 ms, worker failures and retained temporary files;
goroutines returned from two to one. Maximum first-frame latencies were
89.60/78.91/101.61 ms. This local acquisition timing is not cold YouTube startup.
The i9-12900K host ran the profiles concurrently, with overlapping quality/SDK
builds and an idle local bot. See [raw results](rewrite/capacity-acquisition-2026-10-16.json).

Memory is materially higher than the earlier direct-file soak. This workload
includes the new compressed buffer, copy workers and their retention; input
bitrate also changed, so the difference is not a controlled attribution solely
to the ring. The 15-minute fixture fits the ring. Longer/higher-bitrate media
backpressures acquisition and retains extractor slots: simultaneous cold starts
can hit the startup/admission deadline. The full-ring cancellation regression
tests that saturation path, but this soak does not establish 24 simultaneous
long-source acquisitions or unrestricted production capacity.

The final repeat after removing the unused cache-creator API also passed all
three 900-second profiles. Cache pressure now uses the production transaction
API. Same host/workload and overlapping local validation; no controlled speedup
claim is made. [Raw final results](rewrite/capacity-transactions-2026-10-16.json)
identify the image and all counters.

| Guilds | CPU / quota | Peak anonymous MiB | Maximum gap ms | Maximum session shutdown ms |
| ---: | ---: | ---: | ---: | ---: |
| 4 | 7.00% | 139.94 | 26.53 | 15.77 |
| 12 | 9.19% | 380.82 | 30.32 | 24.38 |
| 24 | 8.71% | 685.98 | 29.80 | 22.03 |

All had zero missed 60 ms deadlines, worker failures and retained temporary
files; goroutines returned from two to one. Maximum first-frame latencies were
76.57/90.15/106.93 ms. The microbenchmark gate measured synthetic 1,000-frame
attempts at 101,196 ns, 4,169 B/four allocations; real 1,000-frame attempts at
304,511,891 ns, 118,576 Go B/92 allocations. Cache acquire measured 750.5 ns,
352 B/three allocations, and limiter admission 86.48 ns, 64 B/two allocations.
The real-media sample is slower than the October 13 sample and overlapped other
work; these measurements do not isolate a cause or establish an optimization.

The original runtime had five buffered packets plus a producer packet and
unbounded queue/guild admission. V2 replaces those paths with the budgets below.
Measurements are workload evidence, not a general production capacity guarantee.

Reference slice target budgets:

| Resource | Bound / saturation policy |
| --- | --- |
| Opus packet | 4,000 bytes; reject larger/empty packets |
| PCM frame | 960 stereo samples (3,840 bytes); partial final frame zero padded |
| Audio queue | Synchronous pull/send; one packet in flight; backpressure |
| Uncached acquisition | 8 MiB compressed ring/attempt, two 32 KiB copy blocks, one 4,000-byte primed packet; backpressure, no drops |
| Preparation | 256 KiB, successful EOF or 2 seconds after first bytes; 30 seconds through first encoded packet, including admission |
| Stderr | 16 KiB retained; drain excess; report classification/fingerprint |
| Metadata | Maximum 500 tracks/playlist; bounded total response and field sizes |
| Session mailbox | 32 commands; reject saturation |
| Queue | 500 items/session; reject additions exceeding budget |
| Guilds | Configured retained-session bound; separate memory/extractor-derived active-stream cap; no silent eviction |
| Extraction | Existing configured 1–16 shared process limit |
| Decoder threads | One FFmpeg decoder/filter/filter-complex thread per attempt |
| Retry | No retry inside attempt/provider; session policy only |

Additional implemented bounds: metadata subprocess stdout is limited to 8 MiB
(the standard bytes.Buffer can retain up to twice that in capacity); stderr
remains 16 KiB; metadata calls have a 30-second deadline. One optional cache writer
taps acquired bytes, with nonblocking admission and min(cache limit, 256 MiB)
scratch storage additional to the committed cache budget. Overflow aborts only
that write; playback continues. No background warming downloads remain. Long and
live playback has no new duration cutoff; cancellation joins acquisition and the
decoder feeder. Guild manager construction
requires explicit session and queue limits; it rejects saturation and retains a
slot through shutdown. The v2 runtime now uses these admission budgets.

Gateway authorization state retains at most 1,024 guild IDs, 1,024 voice members
per guild and 16,384 voice records in total. Oversized guild snapshots fail
authorization closed until a valid resnapshot; new over-budget voice records
are rejected. Full SDK guild/member/channel/message caching is disabled.
Metadata playlist rows are decoded incrementally and rejected at the configured
track bound before object expansion; nested playlists are unavailable.

Cache storage has a 65,536-entry index cap, 256-entry directory scan batches,
one bounded transaction writer, and one filesystem mutation at a time. The
index cap fails startup without deleting completed media. Snapshot sorting and
unlink/rename occur outside the metadata lock. External processes modifying the
same directory are outside the cache's byte-accounting guarantee.

Measure allocations/frame, codec CPU, memory/session, stalled-consumer behavior,
shutdown latency and concurrent guild workloads independently of live provider
latency. Report machine, image/tool versions, frame count and concurrency with
results. Microbenchmarks do not establish a production guild-capacity claim.

## First checkpoint measurements

`docker build --target bench --progress=plain .`, 2026-10-07, linux/amd64,
pinned Go 1.27.1 image, Intel i9-12900K, 24 logical CPUs visible, no explicit
container CPU quota. Single benchmark samples, not statistically established
improvements:

| Workload | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| New attempt, 1,000 synthetic packets, immediate fake output, discarded log | 8,182 | 4,168 | 4 |
| Existing frame pool borrow/return | 51.68 | 0 | 0 |
| Existing stereo PCM volume, 20 ms | 1,009 | 0 | 0 |
| Existing cache acquire | 1,125 | 352 | 3 |
| Existing extractor limiter acquire | 78.09 | 64 | 2 |
| Existing fake registration, 1,000 guilds | 355,079 | 73,947 | 1,010 |

The synthetic attempt has four allocations per attempt, not per packet
(amortized 4.168 bytes/packet for this workload). It excludes codec, subprocess,
network, pacing and audible output. Existing workloads are reference baselines,
not direct comparisons with the new attempt. Real-codec and concurrent-session
measurements remain separate gates.

Real-media follow-up on the same host/image: `BenchmarkRealMedia` processes a
20-second stereo WAV (1,000 packets), launches FFmpeg, encodes libopus and
decodes each packet in the fake voice sink. Five measured attempts averaged
225,065,114 ns/attempt, 17.06 MB/s input, 117,993 Go B/attempt and 91 Go
allocations/attempt. There is no real-time pacing in this throughput workload.
The pinned hraban binding allocates its Opus state on the Go heap, so that state
is included in Go allocation counts. Those counts exclude FFmpeg RSS and other
native-library overhead; they are not total memory/session or production
concurrency limits. Later checks explicitly configure 128 kbit/s, preserving the
old encoder's bitrate; the initial throughput measurement used libopus auto.

## Real v2 concurrent workloads

2026-10-08, same i9-12900K host, pinned Go 1.27.1 linux/amd64 image, each
profile run sequentially for 30 seconds with its own CPU/memory quota. Generated
31-second Opus fixture → real session/queue → FFmpeg stereo PCM → libopus at
128 kbit/s → real Discord pacing adapter → discard packet writer. No network,
Discord login, DAVE encryption or real provider retrieval. Cache pressure and
bounded simulated extractor work run concurrently. These are short baselines,
not the default 15-minute soak or a production capacity guarantee.

| Guilds | CPU quota | CPU (% quota) | Peak anon MiB | Added anon MiB/guild | Max first frame ms | p99 gap ms | Max session shutdown ms |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 2 | 2.82% | 31.50 | 26.77 | 38.44 | 20 | 2.91 |
| 4 | 2 | 7.42% | 73.08 | 16.59 | 65.77 | 20 | 7.43 |
| 12 | 4 | 8.30% | 197.55 | 15.90 | 76.38 | 20 | 23.73 |
| 24 | 8 | 11.62% | 363.68 | 14.87 | 68.97 | 20 | 13.99 |

All four profiles passed their CPU/memory/deadline/resource checks: no gaps over
60 ms, no worker failures, no retained temporary files, and goroutines returned
from 2 before the workload to 1 afterward. Gap percentiles use 1 ms histogram
bins (floor), sampled after packet acceptance. Shutdown is the maximum observed
session join time after cancellation; it excludes Discord SDK shutdown.

Anonymous memory is sampled from cgroup v2 `memory.stat` every 100 ms. Added
memory/guild is (peak anonymous memory minus pre-workload anonymous memory)
divided by guild count. It includes shared runtime/cache work and FFmpeg, and
does not isolate each session. Total cgroup memory includes reclaimable file
cache and is recorded separately; it must not be called RSS. Sampled peaks may
miss short transients. [Raw reports](rewrite/capacity-2026-10-08.json) retain all
values, image digest and quotas.

The first five-second-loop trial recorded a 77.67 ms restart gap and failed its
60 ms deadline gate. The sustained fixture avoids mixing track startup gaps into
steady delivery; this does not establish gapless transitions. An earlier pacing
scheme yielded 1,449 packets in 30 seconds for one guild because wakeup lateness
accumulated. The media clock now preserves phase during ordinary jitter and
resets after longer stalls; the final run delivered 1,499 packets after startup.
These single runs demonstrate the timing defect and correction, not a statistical
CPU or memory improvement.

Reproduce a profile without credentials or persisted-cache mounts:

```sh
docker build --target capacity-bench -t gobard-capacity-bench:local .
docker run --rm --network none --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=128m --cpus 2 --memory 1g --pids-limit 256 \
  -e GOBARD_BENCH_PROFILE=small -e GOBARD_BENCH_DURATION=30s \
  gobard-capacity-bench:local
```

Profiles single/small/medium/large use 1/4/12/24 guilds; use CPU quotas 2/2/4/8
and memory limits 1/1/2/4 GiB respectively. Duration accepts 1 second–1 hour;
the unchanged benchmark Compose workflow defaults to 15 minutes.

The final Docker microbenchmark gate also passed. Its sample reported
11,161 ns/1,000-packet synthetic attempt (4,168 B, 4 allocations) and
276,614,478 ns/real 1,000-packet attempt (118,710 Go B, 93 allocations,
13.88 MB/s). These are slower samples than the initial measurements; bitrate
is now explicitly 128 kbit/s and the runtime-image build overlapped this gate.
They are not a controlled before/after CPU comparison. The sustained quota
measurements above ran sequentially without concurrent quality builds.

## Live-diagnostic timing overhead (2026-10-09)

Added four monotonic clock samples per valid audio packet and fixed-size
per-attempt maxima/counters; no per-frame logging, allocation or additional
worker. `GOBARD_BENCH_DURATION=30s make docker-bench` passed on the same
i9-12900K host. Synthetic 1,000-packet attempts measured 117,495 ns, 4,169 B and
four allocations; real 1,000-frame decoding measured 206,612,358 ns, 118,282 B
and 90 allocations. The synthetic sample is slower than the previous 8,917 ns
sample by about 109 ns/frame, with the same allocation count. These were not
paired isolated runs; the real-media sample does not establish an improvement.

Concurrent 30-second profiles with 4/12/24 guilds used 8.14/11.03/10.44% of their
2/4/8-CPU quotas. Maximum frame gaps were 21.17/22.57/23.52 ms; all had zero gaps
over 60 ms, worker failures and retained temporary files. Goroutines returned
from two to one. [Raw reports](rewrite/capacity-timing-2026-10-09.json) retain the
image and measurements. Quality builds overlapped parts of this check and a
local Discord bot remained online. This is a short regression check, not another
15-minute soak or proof about client-side playback while gaming.

## Fifteen-minute soak (2026-10-08)

`make docker-bench` passed its microbenchmarks and all three 900-second profiles.
Same i9-12900K host; the profiles ran concurrently, with some Docker quality
builds and an idle local Discord bot on the host. This is sustained capacity
evidence under that workload, not a controlled before/after optimization result.

| Guilds | CPU quota | CPU / quota | Peak anonymous MiB | Frames | Maximum gap ms | Maximum session shutdown ms |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 | 2 | 8.49% | 80.21 | 179,994 | 25.88 | 23.51 |
| 12 | 4 | 11.11% | 178.78 | 539,969 | 31.90 | 14.94 |
| 24 | 8 | 10.94% | 399.98 | 1,079,932 | 29.19 | 30.90 |

Each run had zero missed 60 ms deadlines, worker failures and leftover temporary
files; goroutines returned from 2 to 1. The image inherited irrelevant idle bot
health probes; the benchmark stage now disables those probes. Raw metrics,
image identity and limitations are in
[capacity-soak-2026-10-08.json](rewrite/capacity-soak-2026-10-08.json).
Microbenchmark samples in this run: synthetic 1,000-frame attempt 8,917 ns,
4,168 B/4 allocations; real 1,000-frame decode/encode attempt 222,682,651 ns,
118,544 Go B/91 allocations. No statistical improvement claim is made.

Separately, an idle real Discord-connected local v2 container handled SIGTERM
and exited code 0 without OOM in 1.377 seconds. That includes container/SDK
shutdown overhead but does not prove active DAVE shutdown or a strict join of
unexposed SDK workers.

## Acceptance preparation regression (2026-10-13)

`GOBARD_BENCH_DURATION=30s make docker-bench` passed on the same i9-12900K.
The 4/12/24-guild profiles ran concurrently, overlapped quality builds, and used
7.76/10.72/10.23% of their CPU quotas. Maximum frame gaps were 21.96/21.82/22.91 ms;
all had zero gaps over 60 ms, worker failures or leftover temporary files.
Goroutines returned from two to one. Maximum session shutdowns were
11.11/29.40/27.20 ms. [Raw results](rewrite/capacity-acceptance-2026-10-13.json)
retain the image and resource measurements. This is a short regression run;
the earlier fifteen-minute soak remains the sustained evidence.

Microbenchmark samples: synthetic 1,000-frame attempt 109,676 ns, 4,168 B/four
allocations; real 1,000-frame decoding 207,591,867 ns, 118,480 Go B/91 allocations.
Cache acquire measured 873.8 ns, 352 B/three allocations; interactive limiter
95.85 ns, 64 B/two allocations. No controlled speedup is claimed. The workload
does not exercise Discord UI HTTP, real DAVE or provider networking.

Automatic player state adds one bounded panel per admitted guild, two IDs of at
most 32 bytes, one view value, a 32-byte digest and a capacity-one ownership gate.
There is no message history. As of October 16, each observer owns one editor
worker and a capacity-one hint channel, canceled and joined at session removal.
Slow edits cannot delay health sampling. Updates use this editor,
at most one automatic HTTP edit per guild per three seconds and fifteen-second
progress steps. Render payloads have bounded fields/pages; HTTP projects at
most 64 KiB of response data (plus ReadAll allocation growth), with eight-second
I/O deadlines. Failed edits stop after three attempts; deleted/forbidden messages
stop immediately. Interaction concurrency retains the existing 32-handler cap.
