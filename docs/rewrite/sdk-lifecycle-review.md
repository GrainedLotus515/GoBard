# Temporary SDK lifecycle exception — 2026-10-13

This is an unresolved engineering exception, not a user playback-test gate.
The engineering standard requires every goroutine to be cancellable and joined.
The pinned libraries do not expose enough public lifecycle API to prove that
property for their internal workers. No exception has been approved implicitly.

## Exact dependencies and source findings

`github.com/bwmarrin/discordgo` resolves through go.mod to
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

These findings were checked against source exported from the pinned Docker
module cache; the SDK source was not copied into the repository or modified.

## Adapter containment implemented

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

## Decision needed before claiming strict rewrite completion

Either explicitly accept these temporary SDK limitations for the current bot,
with the planned independent library responsible for closing them, or require
an upstream/separate-library fix with public cancellation/join APIs and tests.
Reimplementing/forking general Discord Gateway/voice internals inside GoBard is
outside the approved architecture. A successful user acceptance run cannot
resolve or waive this exception. The rest of the implementation and offline
verification can be completed independently.
