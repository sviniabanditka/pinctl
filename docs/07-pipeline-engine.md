# pinctl — Pipeline Engine

This document is the design spec for `internal/engine`: the tick-based DAG
evaluator described in [00-design-brief.md](./00-design-brief.md) and
introduced structurally in [03-architecture.md](./03-architecture.md) and
[06-runtime.md](./06-runtime.md). If anything here appears to contradict the
brief, the brief wins.

Related docs: [08-node-catalog.md](./08-node-catalog.md) (every node type this
engine executes), [09-data-model.md](./09-data-model.md) (on-disk JSON shape
of a pipeline), [10-api.md](./10-api.md) (how the editor talks to the engine),
[13-testing.md](./13-testing.md) (simulation harness built on the hooks
defined here).

## Table of contents

- [Execution model](#execution-model)
- [Graph model](#graph-model)
- [Topological sort](#topological-sort)
- [Node lifecycle and interface](#node-lifecycle-and-interface)
- [Input snapshotting and output writes](#input-snapshotting-and-output-writes)
- [Validation](#validation)
- [Hot-reload](#hot-reload)
- [Runtime error handling](#runtime-error-handling)
- [Debug and observability hooks](#debug-and-observability-hooks)
- [Determinism and testing hooks](#determinism-and-testing-hooks)
- [One tick, in full](#one-tick-in-full)

## Execution model

Per the brief (ADR-1 in [03-architecture.md](./03-architecture.md)), the
engine is **tick-based, not event-driven**: each enabled pipeline is
evaluated on a fixed interval, reading and writing whatever the graph
touches every tick, rather than reacting to individual pin-change events.

- **Default tick rate: 20 ms.** Configurable per pipeline via `tickMs` in
  `pipeline.json` (see [09-data-model.md](./09-data-model.md)). There is no
  global tick rate — each pipeline owns its own `time.Ticker`.
- **One goroutine per enabled pipeline.** A disabled pipeline has no
  goroutine and consumes no CPU; enabling one starts its scheduler, disabling
  stops it at the next tick boundary (never mid-tick). This mirrors the
  per-board goroutine isolation in `internal/board` and is why one pipeline's
  slow node or stuck board never affects another pipeline's tick rate.
- **A tick is synchronous and single-threaded** with respect to that
  pipeline's own graph: no two nodes of the same pipeline ever evaluate
  concurrently, so node authors never need locks around per-node state.
  Concurrency exists *across* pipelines (separate goroutines), never within
  one.

### What happens within one tick

Every tick, in order:

1. **Snapshot inputs.** For every `device-read` node (and any other node
   that reads external state), fetch the current value from the board's
   cached pin state ([`Session.PinState`](./03-architecture.md#internal
   board)) once, at the start of the tick. This snapshot is frozen for the
   rest of the tick — a value that changes mid-tick on the wire is picked up
   on the *next* tick, never within the current one.
2. **Evaluate nodes in topological order.** Walk the precomputed `order`
   (see [Topological sort](#topological-sort)), calling each node's `Eval`
   with the current values on its input ports, producing values on its
   output ports that downstream nodes will read later in the same walk.
3. **Apply sink writes.** `device-write` nodes queue their tick's output
   value to the target board session. Writes are collected during step 2 (a
   sink node still executes as one step in topological order, the same as
   any other node) and are fire-and-forget from the tick's perspective —
   the tick does not block waiting for a serial ack (see
   [Input snapshotting and output writes](#input-snapshotting-and-output-writes)).
4. **Publish a `TickSnapshot`** (per-node last values, tick number, duration)
   to any live `Subscribe` listeners, non-blocking (see
   [Debug and observability hooks](#debug-and-observability-hooks)).

Because step 1 always reads a cache and step 3 always writes fire-and-forget,
**a tick never blocks on the serial port**, regardless of board latency or a
disconnected board.

## Graph model

A pipeline compiles to an in-memory `graph`:

| Concept | Definition |
|---|---|
| **Node** | A typed unit of evaluation with zero or more named input ports and zero or more named output ports (see [08-node-catalog.md](./08-node-catalog.md) for the full catalog and each node's port list). |
| **Port** | A named, typed slot on a node: `bool` or `float64`, per the brief. Every port — even a node's single input/output — is named (`in`, `out`, `in1`, `threshold`, ...) so edges are unambiguous, matching the node catalog's convention. |
| **Edge** | A directed connection `from: nodeId.port` to `to: nodeId.port`, carrying a single value of a fixed type each tick. An edge has exactly one source port and exactly one destination port; a source port may fan out to multiple destination edges, a destination port accepts at most one incoming edge. |

### Value types on edges

Only two value types exist, per the brief: `bool` and `float64`. Analog
reads are raw `0–1023` `float64` unless passed through `scale`. There is no
`int`, `string`, or composite type in the MVP — this keeps type-checking
trivial and every node's port typing static and inspectable from its catalog
entry.

### Type checking on edge connect

Enforced both in the editor (immediate UX feedback) and authoritatively in
`internal/store` at `SavePipeline` time (the engine trusts what it's given,
per [03-architecture.md](./03-architecture.md)):

- The source port's declared output type must exactly match the destination
  port's declared input type (`bool` → `bool`, `float64` → `float64`). There
  is **no implicit coercion** (e.g. no auto `float64 → bool` via nonzero
  check) — nodes that need that conversion exist explicitly (`compare`,
  `threshold-hysteresis`).
  - `delay` and `debounce` are polymorphic over `kind` (`bool` or `float`);
    their *declared* port type for a given node instance is fixed by
    `config.kind`, and the edge type check applies against that resolved
    type, not against "any."
  - `device-read`/`device-write` port types are resolved from the bound
    device's role at validation time (`digital-*` → `bool`,
    `analog-in`/`pwm-out` → `float64`); see
    [08-node-catalog.md](./08-node-catalog.md).
- A destination port with a required input must have exactly one incoming
  edge; zero is a validation error (no silent default — see
  [Validation](#validation)), and more than one is structurally impossible
  since a destination port accepts a single edge by construction (fan-*in*
  isn't a port feature — nodes like `and`/`or` instead declare multiple
  named input ports for that purpose).
- A source port may fan out to any number of destination edges with no
  special handling — evaluating a node once and reading its output value
  multiple times downstream is exactly what topological order gives us for
  free.

## Topological sort

The graph's evaluation order is computed **once, at load/validate time** —
on `Engine.Load`, `Engine.Reload`, and identically inside
`store.SavePipeline`'s validation pass (so the editor gets the same
cycle/order errors the engine would hit, before anything is persisted). It
is never recomputed mid-run; the compiled `graph.order` is treated as
immutable for the pipeline's lifetime until the next reload.

- **Algorithm**: Kahn's algorithm (repeatedly remove nodes with in-degree
  zero) — chosen over DFS-based sort because it detects cycles as a natural
  side effect (nodes left over with nonzero in-degree once the queue empties
  are exactly the cycle) and gives an easy, deterministic tie-break point
  (below), rather than needing a separate cycle-detection pass.
- **Cycle detection and rejection**: a pipeline containing a cycle (any node
  reachable from itself via directed edges) **fails validation** — cycles
  are rejected outright, not resolved via ordering tricks. A dataflow graph
  is not a mutually-recursive system in this design; nodes that need
  feedback over time (e.g. "previous tick's output feeds back in") use a
  stateful node with an explicit `initial` value (`threshold-hysteresis`,
  `delay`, `debounce`) rather than a literal graph cycle, keeping the
  topological sort total and tick evaluation trivially terminating.
- **Deterministic ordering for equal-rank nodes**: when Kahn's algorithm has
  more than one zero-in-degree candidate available at a given step (nodes
  with no ordering constraint between them), ties are broken by **node
  `id`, ascending, lexicographic** (the same `id` string used in
  `pipeline.json`). This makes the evaluation order — and therefore the
  exact `TickSnapshot` produced for a given input — fully reproducible
  across runs and across machines for the same pipeline definition, which
  [13-testing.md](./13-testing.md) relies on for golden-output tests. It
  also gives the documented last-write-wins semantics for the rare
  intentional case of two `device-write` nodes targeting the same device
  (see [Validation](#validation) below and the node catalog's
  `device-write` entry).

## Node lifecycle and interface

Every node type implements a small interface; `internal/engine` provides the
scheduler and graph plumbing, node authors only implement pure evaluation
logic plus, for stateful nodes, an explicit state struct.

```go
package engine

// Value is either a bool or a float64; exactly one of the two fields is
// meaningful, selected by Type.
type Value struct {
    Type  PortType // TypeBool | TypeFloat64
    Bool  bool
    Float float64
}

// NodeType is implemented once per catalog entry (device-read, compare,
// delay, ...) and registered in a package-level type registry keyed by the
// JSON "type" string, so unknown types are caught at load time (see
// Validation).
type NodeType interface {
    // Name is the JSON "type" discriminator, e.g. "threshold-hysteresis".
    Name() string

    // Ports describes this node's input/output ports and their static
    // types, given its decoded config (some nodes, e.g. "and"/"or"/"delay",
    // have config-dependent port shape: inputCount, kind).
    Ports(cfg NodeConfig) (PortSpec, error)

    // NewInstance decodes raw JSON config into a typed config struct and
    // returns a fresh, stateless-or-stateful Node ready to evaluate. Called
    // once per node per graph compile (Load/Reload), never per tick.
    NewInstance(rawConfig json.RawMessage, deps NodeDeps) (Node, error)
}

// Node is the per-instance evaluator, one allocated per node per compiled
// graph. Stateful nodes hold their state as private fields on the concrete
// type implementing Node — there is no separate "state store"; state lives
// exactly where the node instance lives, and dies with it on reload.
type Node interface {
    // Init is called once, right after NewInstance, before the first tick
    // (e.g. to prime a hysteresis node's output to its `initial` config, or
    // start a timer's phase clock). Receives the injectable Clock (see
    // Determinism and testing hooks) so timing-based nodes never call
    // time.Now() directly.
    Init(clk Clock) error

    // Eval runs once per tick, in topological order. `in` holds one Value
    // per declared input port (ordered per PortSpec), already snapshotted
    // for this tick; it returns one Value per declared output port. Eval
    // must be a pure function of `in` plus the node's own private state —
    // it must never touch the network, disk, or a board session directly
    // (device I/O is mediated by dedicated device-read/device-write nodes
    // via NodeDeps, not ad hoc calls from arbitrary node types).
    Eval(ctx TickContext, in []Value) ([]Value, error)

    // Close releases any resources on pipeline disable/reload/shutdown.
    // Most nodes no-op; present for symmetry and future node types that
    // might hold e.g. an open file handle.
    Close() error
}
```

- **Config unmarshaling** happens once in `NewInstance`, using each node
  type's own Go config struct (e.g. `thresholdHysteresisConfig{OnLevel,
  OffLevel, Initial float64...}`) via `encoding/json` with strict decoding
  (unknown fields rejected — mirrors the config-file strictness in
  [06-runtime.md](./06-runtime.md)), not re-parsed on every tick.
- **Stateful nodes** (`delay`, `debounce`, `edge-detect`,
  `threshold-hysteresis`, `timer`) hold their state — a ring buffer of
  delayed values, a stability timer, a previous input value, a countdown
  phase — as private fields on the struct returned by `NewInstance`. Because
  exactly one `Node` instance exists per node per compiled graph, and
  `Eval` for a pipeline's nodes never runs concurrently (single scheduler
  goroutine, see [Execution model](#execution-model)), no locking is needed
  inside a node's `Eval`.
- **`device-read`/`device-write`** are the only node types that touch
  `board.Session`; they receive it through `NodeDeps` (injected at
  `NewInstance` time, resolved from the node's `deviceId` config against the
  board manager), so every other node type is a pure function of its inputs
  and remains trivially unit-testable without any board at all.

## Input snapshotting and output writes

- **Reads never block mid-tick.** `device-read.Eval` calls
  `Session.PinState(pin)`, which — per
  [03-architecture.md](./03-architecture.md) — returns a value cached by the
  board's own read goroutine and never blocks on the serial port. The engine
  additionally treats this as a snapshot: even though `PinState` could in
  principle be called at any point during the tick, the design commits to
  calling it **once, at the point that node's turn comes up in topological
  order** (effectively "tick start" for source nodes, since sources have no
  upstream dependencies and are always early in topological order) — the
  value a `device-read` node returns is fixed for the remainder of that
  tick, even if downstream nodes fan out and re-read it indirectly through
  multiple paths.
- **Writes are batched at tick end, deduped by change.** Rather than every
  `device-write` node issuing a write to the board session the instant its
  `Eval` runs, the tick collects `(pin, value)` write intents from all sink
  nodes as they evaluate, then flushes them once evaluation order reaches
  the end of the tick. Two rules apply at flush:
  - **Only write on change.** If a device's value this tick equals the
    last value pinctl wrote to that device on a previous tick, no serial
    write is issued. This bounds the write rate to the actual rate of
    change in the pipeline's outputs rather than the tick rate, which
    matters on the 57600-baud Firmata link when many pipelines/devices are
    active. (Note: this is a wire-traffic optimization only — it does not
    change the node catalog's documented `device-write` semantics, where
    the *node* is described as writing "every tick, idempotent"; from the
    node's point of view every tick has a defined output value, the engine
    just elides the redundant wire write.)
  - **Last-write-wins per tick for a shared device.** If two `device-write`
    nodes (same or different pipelines) target the same `deviceId` in the
    same tick, the write that lands is whichever node evaluates last in
    topological order — which, thanks to the deterministic tie-break
    (node `id`, ascending), is reproducible run to run. This matches the
    node catalog's documented behavior exactly (see `device-write` in
    [08-node-catalog.md](./08-node-catalog.md)): allowed, flagged as a UI
    warning, not a hard validation error.
- Writes remain fire-and-forget from the tick's perspective: the flush
  queues each write to the target `board.Session` and does not wait for a
  serial ack before the tick is considered "done" and the scheduler moves
  to the next tick boundary; a slow/stuck board degrades only that write's
  eventual delivery (bounded by `Session`'s own short ack timeout), not the
  tick rate.

## Validation

Validation is **static** and runs once, before a pipeline is ever enabled or
handed to the engine — authoritatively in `internal/store.SavePipeline` (see
[03-architecture.md](./03-architecture.md), data flow (a)), and identically
on the engine side as a defensive re-check inside `Engine.Load`/`Reload` for
any pipeline it's asked to run (belt-and-suspenders against a store bug or a
hand-edited file, never expected to fire in normal operation). The engine
never validates *during* a tick — a pipeline that reaches the scheduler is
assumed structurally sound, and the scheduler's only job is evaluation.

Checks performed, all-or-nothing (any one failure rejects the whole save —
partial application of a pipeline edit is never allowed):

| Check | Rejected when | Notes |
|---|---|---|
| Unknown node type | `node.type` isn't in the registered `NodeType` registry | Catches typos and stale definitions from a newer pinctl version's node catalog (see schema/versioning notes in [06-runtime.md](./06-runtime.md)). |
| Dangling edge | An edge references a `nodeId.port` that doesn't exist on that node | Includes edges left over after a node's `inputCount`/`kind` config change invalidates a previously valid port name. |
| Type mismatch | Edge's source output type ≠ destination input type | See [Type checking on edge connect](#type-checking-on-edge-connect). |
| Missing/unbound device | `deviceId` on a `device-read`/`device-write` node doesn't resolve to an existing device, or resolves to a device whose role doesn't support the node (e.g. `device-read` on a `digital-out` device) | Device deletion while referenced is independently blocked in `internal/store`, per [08-node-catalog.md](./08-node-catalog.md), but a pipeline can still reference a device whose *role* changed after the edge was drawn. |
| Missing required input | A required input port has no incoming edge | Node catalog specifies which inputs are required vs. optional per node type (e.g. `compare.threshold` is the one documented exception that falls back to a config default). |
| Cycle | Any node reachable from itself via directed edges | See [Topological sort](#topological-sort); Kahn's algorithm's leftover set. |
| Multiple writers to one device | *(never rejected — see below)* | Policy: **allowed with a warning, not a hard error.** |

### Policy: multiple writers to the same device

**Decision: allow, don't reject** — last-write-wins by deterministic
evaluation order (see [Input snapshotting and output writes](#input-snapshotting-and-output-writes)),
surfaced as a non-blocking warning in the editor UI, matching the node
catalog's own documented policy for `device-write`.

**Why not reject:** a hard rejection would block a legitimate and common
pattern — a user building pipeline B as a replacement for pipeline A that
writes the same device, intending to enable exactly one of the two at a
time (A disabled, B enabled, or vice versa). Two *enabled* pipelines racing
to control the same physical pin at the same time is almost certainly a
user mistake, but it's a mistake that's cheap to make visible (a warning,
plus the write is fully deterministic and inspectable via `TickSnapshot`)
rather than one worth blocking a save over — especially since the "same
device, different pipeline, intentionally mutually exclusive enable state"
case is indistinguishable from the mistake case at save time (enable/disable
state can change independently of the save that introduced the second
writer). Rejecting would force the user into an awkward disable-old-enable-
new dance just to save a new pipeline at all.

**Why not silently pick one and hide the other:** the UI warning plus a
deterministic, documented tie-break (topological/id order) keeps the
behavior debuggable — anyone looking at the `TickSnapshot` or logs can see
exactly which node's write took effect.

## Hot-reload

Saving a pipeline in the editor triggers `internal/api` to call
`store.SavePipeline` (validate + persist), then `Engine.Reload(ctx,
pipeline)` on success (see [03-architecture.md](./03-architecture.md), data
flow (a)). Inside the engine:

1. **Compile off the hot path.** The new `pipeline.model.Pipeline` is
   compiled into a fresh `*graph` (nodes instantiated via `NewInstance`,
   `Init` called, topological order computed) entirely outside of, and
   without pausing, the currently-running scheduler goroutine for that
   pipeline (if one exists).
2. **Stop the old goroutine at a tick boundary.** If the pipeline was
   already enabled and running, its scheduler goroutine is signaled to stop
   *after* its current tick completes — never mid-tick, so no node ever
   observes a torn evaluation and no sink write is left half-applied. This
   is the same "finish current tick, then stop" discipline used for
   shutdown in [06-runtime.md](./06-runtime.md).
3. **Atomic swap.** The new `*graph` is published via a single-writer
   `atomic.Pointer[graph]` swap (matching the concurrency model in
   [03-architecture.md](./03-architecture.md)) — there is no window where a
   reader could see a half-constructed graph. If the pipeline is enabled, a
   new scheduler goroutine starts against the new graph immediately after
   the swap (or the existing scheduler loop simply re-reads the pointer at
   the top of its next tick, depending on implementation — either way the
   *next* tick after Reload uses only the new graph, never a mix of old and
   new).
4. **Node state: reset, not preserved.** Every stateful node (`delay`
   buffers, `debounce` timers, `threshold-hysteresis`'s last output,
   `timer`'s phase) starts fresh from `Init`/`config.initial` on the new
   graph — hot-reload does **not** attempt to migrate old node state onto
   the new graph.

   **Why reset, not preserve:** a reload can change the graph
   arbitrarily — different node count, different node IDs, different edges,
   different `initial` values — so there is no general, safe way to map "the
   `threshold-hysteresis` node's last output" from the old graph onto the
   new one; even a same-ID node's *meaning* may have changed (different
   `onLevel`/`offLevel`). Attempting best-effort state migration would trade
   a simple, always-correct rule ("reload = the pipeline restarts fresh,
   like a fresh `Enable`") for a subtly-wrong one ("reload sometimes carries
   state across, depending on what changed, in ways the user can't easily
   predict"). This is also consistent with the runtime's documented
   position in [06-runtime.md](./06-runtime.md) that pipeline runtime state
   is intentionally not persisted across restarts — a hot-reload is treated
   as a small restart of just that one pipeline, not a special case.
5. **Atomicity of the whole operation.** `Reload` either fully succeeds
   (new graph compiled, swapped, scheduler running against it) or fully
   fails (compile/validate error returned to the caller, **old graph keeps
   running untouched** — see the "keeps the previous good graph running"
   rule in [03-architecture.md](./03-architecture.md)'s failure modes
   section). There is no partial-reload state.
6. **Sink outputs during the swap.** Because the old scheduler finishes its
   current tick (including its sink flush) before stopping, and the new
   scheduler's first tick performs its own full evaluate-then-flush cycle,
   devices only ever see values written by one complete tick of one
   graph — never an interleaving of old-graph and new-graph writes. There
   is a brief gap (at most one tick period) between the old scheduler's
   last write and the new scheduler's first write, during which a device
   simply holds whatever it was last set to; this is not distinguishable
   from ordinary continuous operation from the device's point of view.
   Reload does **not** force devices to a safe/off state before swapping —
   see [Runtime error handling](#runtime-error-handling) for the distinct
   case of a board actually disconnecting.

## Runtime error handling

### Node evaluation errors

**Decision: skip the tick's remaining evaluation for that pipeline, and
disable the pipeline after 3 consecutive failed ticks.**

- If a node's `Eval` returns an error (or panics — recovered per the
  runtime's error philosophy in [06-runtime.md](./06-runtime.md)) during a
  tick, that tick is abandoned for this pipeline: no further nodes in the
  topological order are evaluated, and no sink writes queued so far in that
  tick are flushed (a half-evaluated tick never partially reaches devices).
  The error, the failing node's ID, and the tick number are recorded and
  published as a `pipeline state change` event (`state: error` detail) per
  the event bus described in [06-runtime.md](./06-runtime.md).
- The scheduler goroutine does **not** stop after a single failed tick — it
  waits for the next tick boundary and tries again, since a single failure
  is often transient (e.g. a division edge case in `scale` from a
  momentarily out-of-range read).
- After **3 consecutive** ticks fail, the pipeline is automatically
  **disabled** (scheduler goroutine stops, same as an explicit
  `Engine.Disable`) and surfaced to the UI as `state: error` with the
  triggering error retained in `Status`. Re-enabling requires an explicit
  user action (fix the pipeline or just re-enable if the underlying
  condition cleared) — the engine does not auto-retry-forever, matching the
  brief's "user configuration mistakes must never crash the process" while
  still avoiding an infinite silent-failure loop that burns CPU on a
  permanently broken pipeline. A successful tick resets the consecutive-
  failure counter to zero.
- This mirrors, at pipeline granularity, the "pipelines fail independently"
  principle in [06-runtime.md](./06-runtime.md): one pipeline's node bug
  never touches another pipeline, and the process itself never crashes from
  a node error.

### Board disconnected mid-run

- Per [03-architecture.md](./03-architecture.md)'s failure modes, a
  disconnected board does not stop the affected pipeline's scheduler — the
  pipeline keeps ticking, but `Session.PinState`/writes return
  `ErrBoardOffline` for that board's pins.
- **`device-read`** nodes hold their **last known value** (the node
  catalog's documented policy) rather than emitting a false `0`/`false`
  reading; the `TickSnapshot` marks the affected node's value as stale so
  the UI can distinguish "genuinely still this value" from "frozen because
  the board is gone."
- **`device-write`** nodes: the write for that tick is **dropped**, not
  queued or retried (also the node catalog's documented policy) — the
  engine does not attempt to buffer writes for a reconnecting board. What
  the physical device itself does while disconnected (hold last actuated
  state vs. some board-level fail-safe) is a property of the board/device,
  not the engine, and is out of scope here — see
  [09-data-model.md](./09-data-model.md) for any device-level
  "on-disconnect" behavior configuration.
- This does **not** count toward the node-evaluation-error consecutive
  failure counter above — a stale read or a dropped write is a degraded-
  but-successful tick (the node's `Eval` returns a value, just a stale
  one), not an evaluation error. The pipeline is instead flagged `degraded`
  in `Status`/`TickSnapshot`, distinct from the `error` state used for node
  eval failures.
- On board reconnect, the next tick automatically resumes normal reads and
  writes — no manual re-enable, per
  [03-architecture.md](./03-architecture.md).
- One ordering detail worth calling out: `internal/board` (the session
  layer, not the engine) applies each output device's `onDisconnect` policy
  — writing `safeValue` for devices configured `"safe-value"` — at
  session-ready time, i.e. before the engine's next tick runs against the
  reconnected board. By the time the engine's `device-write` nodes evaluate
  again, any safe-value reset has already landed, so the engine's normal
  last-write-wins-per-tick semantics simply take over from there without
  needing to know about the reconnect itself. See the `onDisconnect`/
  `safeValue` device options in
  [09-data-model.md](./09-data-model.md#device-object).

## Debug and observability hooks

Built for the editor's live-debugging panel (see
[11-web-ui.md](./11-web-ui.md)) over the WebSocket transport defined in
[10-api.md](./10-api.md).

- **Per-edge last-value capture.** Every tick, after evaluation, the engine
  records the value that was on each edge (equivalently: each node output
  port) that tick into a `TickSnapshot`. This is a plain map
  (`nodeId.port -> Value`) plus staleness/degraded flags where applicable,
  built as evaluation proceeds — no extra pass over the graph is needed
  since values are already at hand while walking topological order.
- **Delivery.** `TickSnapshot`s are published to a `Subscribe(ctx,
  pipelineID)` channel per the `Engine` interface in
  [03-architecture.md](./03-architecture.md); `internal/api`'s WS handler
  relays them to connected editor clients. Publication is **non-blocking**:
  a full subscriber channel drops the snapshot (drop-oldest) rather than
  stalling the tick — a slow browser tab must never throttle the scheduler.
  When no one is subscribed to a pipeline, the engine skips building the
  snapshot's serialized form entirely (still computes the map cheaply, but
  does not pay JSON-encoding cost) to keep the steady-state cost of
  debugging support near zero.
- **Tick duration metrics.** Each tick records wall-clock duration (from
  "read inputs" through "flush writes," using the injectable
  [Clock](#determinism-and-testing-hooks)) into a small rolling stats
  structure per pipeline (e.g. last, min/max/avg over a short window),
  exposed via `Engine.Status` per-pipeline (see the
  [runtime doc's resource budget section](./06-runtime.md#resource-budget-on-raspberry-pi),
  which references these stats as the way real overrun is observed in the
  field rather than guessed at).
- **Overrun handling.** If a tick's evaluation (plus flush) takes longer
  than the pipeline's `tickMs`, the scheduler does **not** run the next
  tick back-to-back to "catch up" — it **skips** the next scheduled tick
  boundary that has already passed and resumes on the following one
  (equivalent to `time.Ticker`'s natural behavior: ticks that fire while
  the receiver isn't ready are dropped, not queued). Each overrun is logged
  once at `warn` with the pipeline ID and observed duration (per the
  logging levels defined in [06-runtime.md](./06-runtime.md)) rather than
  logged every subsequent tick, to avoid log spam from a pipeline stuck in
  a persistent overrun state.

## Determinism and testing hooks

Two seams exist specifically so [13-testing.md](./13-testing.md) can drive
the engine deterministically without real hardware or real time:

- **Injectable clock.** All timing-dependent code (`timer`'s phase,
  `delay`'s tick-indexed queue, `debounce`'s stability window, `clock`'s
  time-of-day check, tick duration metrics) reads time exclusively through
  a small `Clock` interface (`Now() time.Time`) passed in at `Node.Init`
  and to the scheduler itself, rather than calling `time.Now()` directly
  anywhere in `internal/engine`. Production wiring uses a real-clock
  implementation; tests inject a fake clock they can advance tick-by-tick
  under full control, making even long `delayMs`/`stableMs`/`periodMs`
  windows instantly and deterministically testable.
- **Fake board interface.** Because `device-read`/`device-write` nodes
  depend only on the `board.Session` interface (never a concrete serial
  implementation) via `NodeDeps`, a test can supply a fake `Session` that
  returns scripted `PinState` values and records writes in memory — the
  entire engine, including a full multi-node pipeline with device I/O, is
  exercisable in-process with no serial port, no Arduino, and no timing
  flakiness.
- Combined with the deterministic topological tie-break (node `id`
  ordering, see [Topological sort](#topological-sort)), these two seams
  mean a given `(pipeline definition, clock schedule, board input script)`
  triple always produces the exact same sequence of `TickSnapshot`s — the
  basis for golden-output tests described in
  [13-testing.md](./13-testing.md).

## One tick, in full

```
┌──────────────────────────────────────────────────────────────────────┐
│ Pipeline scheduler goroutine — time.Ticker fires (every tickMs)      │
└──────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
   ┌───────────────────────────────────────────────────────────────┐
   │ Phase 1 — Snapshot inputs                                      │
   │   for each source node (device-read, constant, timer, clock):  │
   │     read board.Session.PinState(pin)   [cached, non-blocking]  │
   │     or evaluate pure source logic (timer phase, clock window)  │
   │   → fixed for the rest of this tick                            │
   └───────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
   ┌───────────────────────────────────────────────────────────────┐
   │ Phase 2 — Evaluate nodes in topological order                  │
   │   for each node in graph.order:                                │
   │     in  := outputs of upstream nodes (already computed)        │
   │     out, err := node.Eval(ctx, in)                              │
   │     record out into this tick's TickSnapshot                  │
   │     if err != nil → abandon tick for this pipeline (see         │
   │        Runtime error handling); do not flush partial writes    │
   │     if node is device-write → queue (pin, value) write intent  │
   └───────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
   ┌───────────────────────────────────────────────────────────────┐
   │ Phase 3 — Flush sink writes                                    │
   │   dedupe: only write if value changed since last write         │
   │   last-write-wins per device if multiple writers this tick     │
   │   for each surviving write intent:                             │
   │     board.Session.DigitalWrite/PWMWrite(pin, value)  [fire-    │
   │       and-forget, bounded ack timeout, does not block tick]    │
   │   if device unreachable → drop write, mark pipeline degraded   │
   └───────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
   ┌───────────────────────────────────────────────────────────────┐
   │ Phase 4 — Publish observability                                 │
   │   TickSnapshot{tick #, per-node/per-edge values, duration,     │
   │     degraded/error flags} → Subscribe() channel, non-blocking  │
   │   record tick duration into rolling stats (Status)             │
   │   if duration > tickMs → log warn once, skip next tick boundary │
   └───────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
                 wait for next Ticker fire (or ctx.Done → stop
                 cleanly at this boundary, e.g. Disable/Reload/shutdown)
```
