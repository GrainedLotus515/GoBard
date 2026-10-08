# Performance and budgets

No performance improvement is claimed before measurements. Existing frame
capacity is five buffered packets plus a producer packet, each with a 4,000-byte
buffer. Existing queue/guild admission is unbounded.

Reference slice target budgets:

| Resource | Bound / saturation policy |
| --- | --- |
| Opus packet | 4,000 bytes; reject larger/empty packets |
| PCM frame | 960 stereo samples (3,840 bytes); partial final frame zero padded |
| Audio queue | Synchronous pull/send; one packet in flight; backpressure |
| Stderr | 16 KiB retained; drain excess; report classification/fingerprint |
| Metadata | Maximum 500 tracks/playlist; bounded total response and field sizes |
| Session mailbox | 32 commands; reject saturation |
| Queue | 500 items/session; reject additions exceeding budget |
| Guilds | Explicit finite configured admission limit; no silent eviction |
| Extraction | Existing configured 1–16 shared process limit |
| Decoder threads | One FFmpeg decoder/filter/filter-complex thread per attempt |
| Retry | No retry inside attempt/provider; session policy only |

Additional implemented bounds: metadata subprocess stdout is limited to 8 MiB
(the standard bytes.Buffer can retain up to twice that in capacity); stderr
remains 16 KiB; metadata calls have a 30-second deadline. Cache warming has one
worker/eight hints and drops hints on saturation. Downloads enforce
min(cache limit, 256 MiB) while writing, with a five-minute deadline; this scratch
allowance is additional to the committed cache budget. Guild manager construction
requires explicit session and queue limits; it rejects saturation and retains a
slot through shutdown. The current production bot has not yet adopted these
replacement admission budgets.

Cache storage has a 65,536-entry index cap, 256-entry directory scan batches,
16 distinct concurrent creators, and one filesystem mutation at a time. The
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
