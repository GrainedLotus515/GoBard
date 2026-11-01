# GoBard — Agent Engineering Standard

> This document governs ongoing work on GoBard v2. It defines engineering invariants, not a fixed implementation blueprint. The rewrite mission and scope are defined separately in `REWRITE_PROMPT.md`. If a requirement is uncertain, consult the existing behavior, tests, project documentation, and design decisions; do not silently invent product requirements.

## 1. Engineering philosophy

Build **rigorous, high-performance, idiomatic Go**. Every resource has an owner; every operation has a lifecycle; every state transition has a reason; every failure is explainable; every abstraction earns its existence.

- Favor explicit, comprehensible control flow and cohesive packages over cleverness, frameworks, generic plumbing, or speculative abstractions.
- Optimize system design and hot-path behavior proactively, but justify nontrivial micro-optimizations with representative measurements.
- Correctness, security, bounded resource use, and truthful observable behavior are non-negotiable. Do not trade them away to improve benchmark numbers.
- Do not preserve obsolete architecture, naming, APIs, or workarounds solely because they exist. Do preserve intentional user-facing functionality unless a change is explicitly authorized and documented.
- Prefer removing an obsolete implementation over keeping layered compatibility shims without a demonstrated migration need.
- Before introducing a new abstraction, explain the boundary it protects and why a direct concrete implementation is insufficient.

## 2. Code and API conventions

- Run `gofmt`; use idiomatic exported names, short receiver names, descriptive local names, and domain-specific terminology consistently.
- Keep interfaces small and normally declare them at the point of consumption. Concrete implementations are the default until polymorphism is warranted by a real boundary or test need.
- Keep mutable implementation fields unexported. Avoid global mutable state and hidden initialization behavior.
- Constructors validate inputs and establish usable invariants. Document any limitations of interface-nil checks. Reject invalid configuration at startup rather than recovering later from partially initialized objects.
- Contracts specify preconditions, postconditions, error semantics, thread safety, ownership of buffers/streams, and shutdown/cancellation behavior. Enforce critical contracts in types, constructors, state machines, and tests instead of depending on comments alone.
- Validate untrusted input at trust boundaries (Discord interactions, URLs, external metadata, configuration, process output). Do not repeat expensive validation in proven internal hot paths without a reason.
- Use standard-library facilities and mature dependencies where appropriate. Never build bespoke logging, scheduling, memory allocation, or protocol frameworks without a concrete, demonstrated requirement.
- Comments explain rationale, invariants, and non-obvious behavior. Keep architecture and operational documentation synchronized with code.

## 3. Package and subsystem boundaries

Use a **modular monolith**, not microservices. Separate at least these conceptual responsibilities, though exact packages are an implementation decision:

- Application startup/shutdown and composition root.
- Discord API and interaction adapter.
- Guild session coordination and queue policy.
- Playback operation lifecycle and audio pipeline.
- Media source resolution, authentication boundary, and retrieval.
- Voice connection and output transport.
- Persistence/cache and configuration.
- Logging, metrics, diagnostic classification, and health.

A media source is not necessarily a playback engine. Metadata resolution, audio acquisition, official-player control, and audio delivery are distinct capabilities. Model them independently rather than claiming every provider can feed Discord voice.

Do not allow the queue to implicitly own low-level audio transport or the transport to own music-selection policy. Avoid import cycles and facade layers that merely forward calls.

## 4. Concurrency, resource ownership, and lifecycle

- Use **hybrid synchronization**: single-owner event loops for mutable guild/session state; mutexes for brief access to genuinely shared structures; atomics for independent counters/flags where they simplify correctness.
- Do not let unrelated goroutines mutate session state directly. Commands, callbacks, and worker results cross an explicit coordination boundary.
- All spawned goroutines and subprocesses have a named owner, cancellation mechanism, terminal result, and join/cleanup path. Background work must be stopped and joined before its owner is considered closed.
- Never hold a shared mutex across disk, network, subprocess, or other potentially blocking I/O.
- A command being submitted, accepted, started, and completed are different facts. Define cancellation races and acknowledgements precisely; a caller timeout does not imply an already accepted operation was rolled back.
- Make shutdown idempotent where applicable, bounded under cooperative dependencies, and safe under concurrent stop/skip/failure. Define behavior for noncooperative subprocesses and stalled I/O.
- Contexts propagate cancellation/deadlines through operations. Do not keep request-scoped contexts indefinitely in long-lived objects; derive owned lifecycle contexts where appropriate.
- Avoid panics for recoverable runtime failures. Use panics only for genuinely impossible programmer invariant violations, with explicit justification.

## 5. Bounded memory and performance

- Every producer/consumer boundary specifies capacity, maximum bytes/frame size, backpressure or drop policy, ownership transfer, saturation behavior, and shutdown semantics.
- Keep audio hot paths bounded, latency-conscious, and allocation-efficient. Avoid per-frame formatting, unnecessary reflection, uncontrolled goroutine creation, or unbounded intermediate queues.
- A channel's element capacity alone does not prove a byte bound. Budget underlying buffers, subprocess output, caches, and accumulated metadata.
- If pooling buffers, guarantee exclusive ownership until consumers finish; benchmark whether a bounded ring or preallocated buffer scheme is simpler or faster. `sync.Pool` is not a guaranteed-capacity allocator.
- Prefer straightforward code in non-hot paths. Do not impose zero-allocation targets without evidence. Correctness-preserving copies are allowed when needed.
- Profile startup latency, allocations per audio frame, CPU under concurrent guilds, memory per active session, tail latency, and shutdown behavior using representative workloads; retain baseline results and workload definitions.
- Record regression evidence for material hot-path changes. Do not claim optimization without measurements or make unsupported performance promises.

## 6. Fully modeled operations and failures

- Represent important operations with stable IDs, explicit lifecycle states, classified terminal outcomes, and causes. Distinguish a logical playback request from its individual source/retry attempts.
- Exactly one authoritative terminal result/event belongs to each accepted attempt. Terminal states never become running again; duplicate completion is a bug.
- Differentiate completion, user skip, user stop, shutdown cancellation, source failure, decoding/encoding failure, transport failure, and other necessary domain outcomes.
- A successful extractor launch, established Discord connection, or clean process exit does **not** prove audio played. Never report playback completion with zero audio frames; define whether completion means fully processed audio versus confirmed transport acceptance, and avoid claiming human audibility.
- Preserve original `error` causes through wrapping (`%w`) and classify actionable failures at trust boundaries. Technical cause, lifecycle outcome, and recovery decision are separate concepts.
- Retry/fallback decisions belong to an explicit policy owner, not buried independently in providers or transport. Budget attempts, elapsed time, backoff, and cancellation; prohibit retry storms and cyclic provider fallbacks.
- For a failure, the operation owner decides cleanup, state transition, user-facing message, and final log. Lower layers return contextual errors; do not log the same error repeatedly at every layer.
- Recovery must preserve queue integrity, release resources, and leave a well-defined state, even during concurrent stop, skip, disconnect, or shutdown.

## 7. Charm Log and production diagnosability

Keep `github.com/charmbracelet/log` unless a verified, documented limitation makes replacement necessary. Do **not** create a competing logging framework.

- Development: readable console logs with configurable debug detail. Production: concise structured records (prefer JSON) with the **same semantic event names and fields**.
- INFO: meaningful lifecycle and successful outcomes. WARN: recoverable degradation with the action/retry policy. ERROR: terminal failed operation with actionable, sanitized explanation. DEBUG: internal transitions, timings, and additional safe diagnostic detail.
- A playback attempt must be traceable from command through source resolution, decoding, voice transport, and cleanup using a stable correlation/attempt ID.
- Standardize event, stage, guild/session/attempt IDs when appropriate, provider, safe track ID, attempt number, elapsed time, outcome, failure category, retry decision, and delivered-frame metrics. Do not emit high-cardinality or sensitive values into metrics without justification.
- Production terminal errors must answer: **what failed, where, why, what recovery occurred or was declined, and what the user experienced**. Debug mode must not be required to learn the normal failure reason.
- Parse and classify bounded subprocess diagnostics. Never merely report `stderr bytes=N`; never dump untrusted stderr blindly. Use allowlisted patterns and a stable unknown-failure diagnostic with safe exit status, elapsed time, and fingerprint.
- Exclude Discord tokens, cookies, headers, signed media URLs, private credentials, and unnecessary user data at *every* level, including DEBUG. Favor allowlisted diagnostic fields over unreliable blanket redaction.
- Avoid per-frame INFO logs. Prefer metrics and sampled diagnostics for continuous observations. Keep health dimensions truthful and distinct: Discord connected, source retrieval functional, audio pipeline functional, actual delivery in progress.
- Don't add full OpenTelemetry infrastructure unless a concrete multi-process/integration requirement justifies it; preserve compatible correlation semantics.

## 8. Product behavior and external integrations

- Preserve intentional existing user-facing Discord commands, permission checks, queue behavior, and useful settings unless documented changes are approved. Inventory current behavior before rewriting; bugs and accidental limitations are not contracts.
- The current `AGENTS.md` ban on Spotify, SponsorBlock, YouTube APIs, arbitrary media URLs, and DMs is **not automatically binding on v2**. Evaluate each as a distinct proposal with scope, security, terms, and actual technical capability; do not assume authorization to add all of them.
- Design for alternative source and playback capabilities. Spotify metadata, official browser-embedded playback, and Discord voice audio are distinct. Do not represent metadata-to-YouTube lookup as an independent Spotify audio source or treat browser embedding as a lawful server-side rebroadcast path.
- External services such as YouTube may still reject retrieval. Make those failures diagnosable; do not bypass auth, rate limits, or provider restrictions via undocumented assumptions. Do not require personal YouTube cookies/accounts as the baseline.
- Keep independent Discord-library and official-player-wrapper work in **separate repositories/projects**. GoBard may isolate its integration adapters, but must not silently fork/rewrite those libraries inside this repository.
- **The Discord library currently used by GoBard is temporary.** A user-owned, general-purpose Go Discord library is planned as a future replacement; design the current integration for that swap without depending on the future library's unimplemented API.
- Third-party Discord types and library-specific Gateway, REST, interaction, voice, or DAVE details must remain inside Discord-facing adapter packages. Core session, queue, playback, and source packages must not import the current Discord SDK.
- Define small GoBard-owned contracts from *consumed capabilities* (command/event input, interaction responses, guild/voice lifecycle, encoded audio delivery), not a mirror of the Discord SDK. Keep transport-specific packet framing, encryption, pacing, and connection lifecycle in the voice adapter; do not invent a misleading generic audio interface that hides those contracts.
- Provide deterministic contract tests and fakes for the adapter boundary, covering command mapping, voice reconnect/shutdown, error classification, backpressure, and cancellation. Document a replacement checklist so a later swap changes adapters/composition wiring rather than domain logic. Do not add a speculative second adapter until the replacement exists.
- Keep the Discord bot and any future chaos-personality agent separate.

## 9. Security, configuration, and deployment

Preserve and verify these existing deployed constraints unless a documented migration explicitly changes them:

- `linux/amd64` target; container runs as UID/GID 1000; `/app/gobard` root-owned and non-writable; read-only root filesystem; `/app/cache` is persistent writable storage; `/tmp` is bounded tmpfs.
- Health routes `GET /live` and `GET /ready`, and `gobard healthcheck --url http://127.0.0.1:8080/ready`; the semantics must reflect actual readiness, not just successful Discord login.
- GHCR is the image publisher; Gitea runs quality gates but does not publish/deploy. Keep image/action/tool pinning and checksum verification; don't introduce mutable production tags.
- Keep existing SOPS/age-managed secrets and deployment configuration secure. Preserve supported settings where useful or provide an explicit migration. `DISCORD_TOKEN` and `DISCORD_TOKEN_FILE` are mutually exclusive. Reject invalid configuration rather than silently coercing it.
- Validate and canonicalize media URLs at source boundaries; guard against SSRF, redirects to local/private addresses, userinfo, local file access, and arbitrary URL bypasses. New providers require provider-specific policies and redirect validation, not broad URL allowlisting by default.
- Preserve voice-channel authorization for playback/queue mutations and `Manage Guild` for configuration changes, subject to documented product changes. Prevent untrusted Discord mentions in responses.
- Never delete, recursively re-own, or casually migrate `./cache`; preserve user data and unrelated working-tree changes.
- The existing VPS service was intentionally stopped. **Do not publish, deploy, start/restart the VPS service, rotate secrets, or modify live infrastructure without explicit authorization.** A pinned deployment might auto-start later; note that risk in deployment documentation.

## 10. Tests, quality gates, and change discipline

- Maintain fast native/local checks when possible, with reproducible Docker build/lint/test/smoke paths for dependencies such as libdave. Do not assume native libdave exists on all contributor hosts.
- Existing supported commands are `make docker-test`, `make docker-lint`, `make docker-build`, and `make docker-smoke`; retain or deliberately migrate them with documentation.
- Run Go formatting, static analysis, unit tests, race-detector suites where supported, integration tests, fault-injection tests, and representative benchmarks. Keep deterministic/offline tests separate from Discord/YouTube/provider-dependent tests.
- Test cancellations at extraction, encoding, transmission, and cleanup; skip/stop versus natural completion; reconnects; zero-frame failures; queue integrity; slow consumers; concurrent guilds; repeated shutdown; crash/restart and cache safety.
- Test required outcomes, not incidental internal call graphs. Keep fixtures realistic and no secrets in fixtures/log snapshots.
- For important changes provide: expected behavior/invariants, test evidence, observed measurements, compatibility effects, and unresolved risks. Never claim a gate passed unless executed and verified.
- Inspect the working tree before editing. Preserve unrelated user changes. Work on the designated rewrite branch; never reset, force-push, remove user data, or publish without explicit authorization.
- Review for architectural drift, duplicate mechanisms, obsolete code, and gratuitous complexity. A final simplification pass is required for each substantial subsystem.

## 11. Documentation and autonomous-agent conduct

Maintain architecture, component contracts, playback lifecycle/state transitions, provider capabilities, configuration/migration, deployment, troubleshooting, security assumptions, and performance benchmarks. Record major tradeoffs in concise decision notes.

Work autonomously within the approved scope. Make routine implementation decisions without repeatedly asking permission, but **stop or explicitly surface** high-impact decisions that change user-visible behavior, broaden the attack surface, risk data loss, require credentials, change production infrastructure, or bind the project to a materially different product direction. Do not fabricate successful external integration tests.

Prefer incremental, inspectable checkpoints with clear evidence, not an enormous last-minute patch. Address root causes rather than accumulating workarounds. If an external blocker exists, isolate it, continue independent work where possible, and report the limitation accurately.
