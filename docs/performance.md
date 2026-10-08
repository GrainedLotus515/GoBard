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
Go allocation counts exclude native Opus memory and FFmpeg RSS; these numbers
cannot be used as total memory/session or production concurrency limits.
