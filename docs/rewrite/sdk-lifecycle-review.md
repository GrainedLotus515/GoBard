# SDK lifecycle review — updated 2026-10-16

## October 16 focused fixes (published and pinned)

Separate local repositories are preserved under
`/home/lotus/Gitea/GoBard-SDK-Fixes`; no library implementation was copied into
GoBard. Each contains a `LIFECYCLE.md` contract and regression tests.

| Dependency | Published patch revision | Scope |
| --- | --- | --- |
| [disgo](https://github.com/GrainedLotus515/gobard-disgo) | `7a61d29e59893a2258a906d2094de335d0554fc6` | terminal Gateway/connection joins, opening ownership, copied state, readiness, finite reconnect policy, generation join, canceled UDP discovery, manager map cleanup, bounded participant/SSRC state |
| [discordgo](https://github.com/GrainedLotus515/gobard-discordgo) | `3d1129bd8482c1071aca2f73e5587a1923d6e551` | OpenContext, terminal Shutdown, worker/callback joins, canceled handshake/backoff, finite reconnect attempts |
| [libdave Go bindings](https://github.com/GrainedLotus515/gobard-libdave) | `26460929a20e4c94719b8c2d7ff1ff54a34656eb` | explicit native handle/result/ratchet cleanup and owned C callback storage |
| [golibdave](https://github.com/GrainedLotus515/gobard-golibdave) | `7783ee7cd557ab9aa23ad02249a156a41201a13c` | native session cleanup, user replacement/removal, serialized frame state, corrected passthrough copy |

The native audit used v1.2.1/cpp source at
`8de72b1f8a2ac3c5a5270755bb8091a62e3c6169`, matching the release used by the
pinned build image. Its C API copies ratchets when installing them, allowing
explicit release of caller-owned handles. The optional pairwise-fingerprint API
starts a detached native thread; GoBard does not consume it. Native calls remain
cooperative rather than context-preemptible. Neither an elapsed timeout nor Go's
race detector proves a hard preemption or native data-race guarantee.

Executed evidence: full disgo and discordgo race suites; combined workspace race
tests for the modified voice/Gateway/native packages and a disposable GoBard
checkout; go vet on those dependency packages. Tests include canceled opening,
duplicate readiness, missing heartbeat acknowledgment with reconnect disabled,
old/new generation separation, UDP cancellation, concurrent map removal, blocked
callbacks, native key-package creation/passthrough and repeated explicit cleanup.
Voice participant state is capped at 1,024; duplicate announcements are idempotent,
SSRC replacement removes the stale mapping, and participant overflow closes the
Gateway. SSRC callbacks execute outside the map lock, with a reentrant inspection
regression. This matches the existing GoBard membership budget. The combined race
suite and vet were rerun after this correction.
Native passthrough microbenchmark: 56.20 ns/frame, 8 B/frame, one allocation/frame
for 320-byte frames on the i9-12900K, linux/amd64; this is not established-MLS
encryption or a before/after improvement measurement.

The user authorized publication on October 16. All four repositories are public
under `GrainedLotus515`, with inherited Actions disabled before the first push.
They retain upstream licenses and exact baseline snapshots; they are focused
maintenance repositories, not the future general-purpose Discord library.
`go.mod` now replaces the upstream module paths with their published immutable
pseudo-versions, and `go.sum` records verified module checksums. Native libdave
v1.2.1/cpp and its asset checksum remain unchanged.

The GoBard adapter now uses OpenContext/Shutdown, SDK manager cleanup and the
incoming voice dial context. No local workspace replacement is required. The
Docker race gate also runs the pinned Gateway, voice and native-wrapper regression
suites, so release checks exercise the dependency fixes themselves. The final
Docker race/helper, lint and hardened smoke gates passed against these published
pins; `go mod verify` also passed.

The first authorized local replacement uses the validated acquisition/health
image, before these pins; see [local evidence](local-2026-10-16.md). SDK runtime
activation and final gates are recorded there and in status.md. **Strict completion
remains blocked until real DAVE reconnect and active-playback shutdown are
verified.** Optional SDK audio sender/receiver and legacy discordgo voice helpers
are outside the consumed path and outside these claims.

## Original blocker and containment

This is a strict completion blocker, not a user playback-test gate.
The engineering standard requires every goroutine to be cancellable and joined.
The original pinned libraries did not expose enough public lifecycle API to prove that
property for their internal workers. The user explicitly declined a temporary
exception: “Keep strict completion blocked until the library is fixed.”

## Exact dependencies and source findings

Before the October 16 fixes, `github.com/bwmarrin/discordgo` resolved through go.mod to
`github.com/ozraru/discordgo v0.26.2-0.20251101193404-5cce117199f7`.
Its `wsapi.go` launches heartbeat/listen workers around lines 212–213.
`CloseWithCode` (around line 928) closes the listening channel and WebSocket but
does not wait on worker completion. An elapsed sleep is not a join guarantee.

`github.com/disgoorg/disgo v0.19.6/voice` has further limits:

- `conn.go:203` launches Gateway opening from a background five-second context.
- `gateway.go:193` starts a listener with an unbuffered ready-result send;
  caller cancellation can leave that send without a receiver.
- `gateway.go:214` cancels heartbeat and closes the socket but exposes no join.
- `gateway.go:384,409` launch heartbeat-triggered reconnects without checking
  AutoReconnect. `doReconnect` uses a background context and retry loop.
- `manager.go:134` iterates/replaces the connection map without retaining its
  mutex, which can conflict with internal asynchronous removal.
- Native DAVE calls do not expose Go-context preemption.

These original findings were checked against source exported from the pinned Docker
module cache; the SDK source was not copied into GoBard. Later fixes live in the separate
repositories listed above.

## Historical adapter containment (before fixed SDK integration)

The voice adapter disables the public AutoReconnect option (which controls
listener-error reconnects, not all heartbeat paths). Both SDK WebSocket
transports now use the same owned socket wrapper: five-second writes, root
cancellation closes the socket, and Close joins the wrapper's cancellation
callback. Voice dialing has a five-second budget and rejects a closed manager.
Manager cleanup snapshots the SDK's locked connection iterator, then closes
outside the lock instead of invoking its unsafe map-wide Close implementation.
GoBard admission/callbacks/attempts/observers are canceled and joined separately.

These mitigations do **not** turn inaccessible SDK workers into joined workers
or prove that heartbeat reconnects cannot outlive a session. In particular, a
manager-rooted dial guard stops new network connections after manager shutdown
but cannot interrupt a private background retry timer. Process exit provides
OS cleanup; it is not a substitute for the in-process lifecycle contract.

## Remaining strict completion decision

The user requires an upstream/separate-library fix with public cancellation/join
APIs and tests. The existing adapter mitigations do not satisfy this gate.
Reimplementing/forking general Discord Gateway/voice internals inside GoBard is
outside the approved architecture. A successful user acceptance run cannot
resolve or waive this blocker. The rest of the implementation and offline
verification can be completed independently.

The separate fixes are now pinned and integrated. Before clearing strict
completion, retain the deterministic evidence for these contracts and complete
the remaining real reconnect/active-shutdown gate:

- Gateway and voice terminal Shutdown cancel and join all owned workers, including opening,
  heartbeat, listener and asynchronous event work, under concurrent cancellation.
- Every reconnect path obeys the configured policy, finite budgets and lifecycle
  cancellation; no private background retry survives session or manager close.
- Canceled opening cannot strand a result send; connection-map cleanup is race
  safe and repeated shutdown is safe.
- Native DAVE work has a verified bounded completion/cleanup contract compatible
  with shutdown; a Go context alone is not evidence of preemption.
- Library lifecycle/fault-injection/race tests and GoBard adapter tests pass,
  followed by authorized live reconnect and active-playback shutdown checks.

Record exact dependency revisions and test evidence here. Neither process exit,
a sleep after Close, nor an audible song proves that this gate has been met.
