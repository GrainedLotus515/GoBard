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

Measure allocations/frame, codec CPU, memory/session, stalled-consumer behavior,
shutdown latency and concurrent guild workloads independently of live provider
latency. Report machine, image/tool versions, frame count and concurrency with
results. Microbenchmarks do not establish a production guild-capacity claim.
