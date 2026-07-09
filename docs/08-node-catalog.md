# pinctl — Node Catalog (MVP)

See [00-design-brief.md](./00-design-brief.md) for the fixed decisions this
doc builds on (node set, edge value types, tick-based engine), and
[07-pipeline-engine.md](./07-pipeline-engine.md) for how nodes are scheduled
and evaluated tick-by-tick. [09-data-model.md](./09-data-model.md) defines the
on-disk JSON shape of a node inside `pipeline.json` (`{id, type, config,
position}`) and [10-api.md](./10-api.md) covers the REST/WebSocket surface
used to create, edit, and observe nodes at runtime.

## Conventions used below

- **Ports** are named; a node with a single input/output still names it
  (`in`, `out`) so edges (`from: nodeId.port` / `to: nodeId.port`, see
  [09-data-model.md](./09-data-model.md)) are unambiguous.
- **Types** on ports are `bool` or `float64`, per the brief. Analog reads are
  raw `0–1023` `float64` unless passed through `scale`.
- **Tick** refers to the pipeline's evaluation tick (default 20 ms, see
  [07-pipeline-engine.md](./07-pipeline-engine.md)). Nodes with internal
  timing (`timer`, `delay`, `debounce`) measure elapsed wall-clock time but
  only ever produce/consume values at tick boundaries.
- **Unconnected inputs**: unless stated otherwise, a node with a required
  input that has no incoming edge is a **pipeline validation error** at
  save/enable time (see [07-pipeline-engine.md](./07-pipeline-engine.md)) —
  it does not silently default. Where a node tolerates a missing input, that
  is called out explicitly.
- **Config defaults** are applied when the field is omitted from
  `node.config` in `pipeline.json`.

---

## Sources

### `device-read`

Reads the current value of a bound device on every tick.

- **Inputs**: none.
- **Outputs**: `out` — `bool` (if the device role is `digital-in`) or
  `float64` (if the device role is `analog-in`, raw `0–1023`).
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `deviceId` | string | *(required)* | Must reference an existing device with role `digital-in` or `analog-in`. |
- **Behavior / edge cases**:
  - Output type is derived from the referenced device's role at pipeline
    load time, not stored redundantly — if the device's role changes to a
    non-readable role (`digital-out`, `pwm-out`) after the pipeline was
    saved, the pipeline fails validation (see referential integrity rules in
    [09-data-model.md](./09-data-model.md)).
  - If the device's board is disconnected/unreachable, `device-read` holds
    its **last known value** and the pipeline is flagged degraded in the UI
    (see [07-pipeline-engine.md](./07-pipeline-engine.md) for the stale-read
    policy); it does not emit `0`/`false` as a false reading.
  - Referencing a deleted device is impossible — device deletion is blocked
    while referenced (see [09-data-model.md](./09-data-model.md)).
- **Example**:
  ```json
  {
    "id": "n1",
    "type": "device-read",
    "config": { "deviceId": "dev_9zq3v7ab4c" },
    "position": { "x": 40, "y": 120 }
  }
  ```

### `constant`

Emits a fixed, user-configured value every tick. Used for thresholds,
enable flags, or literal setpoints without wiring a separate config UI into
every consuming node.

- **Inputs**: none.
- **Outputs**: `out` — `bool` or `float64`, per `config.kind`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `kind` | `"bool"` \| `"float"` | `"float"` | Fixes the output port's type. |
  | `value` | bool or number | `0` (float) / `false` (bool) | Must match `kind`. |
- **Behavior / edge cases**: purely static; value only changes when a user
  edits the node's config and saves. No smoothing/ramping.
- **Example**:
  ```json
  {
    "id": "n2",
    "type": "constant",
    "config": { "kind": "float", "value": 300 },
    "position": { "x": 40, "y": 240 }
  }
  ```

### `timer`

Emits a one-tick `bool` pulse (`true` for exactly one tick, `false`
otherwise) at a fixed period. Used to drive periodic actions (e.g. a
heartbeat log, a periodic re-check) without wiring an external clock.

- **Inputs**: none.
- **Outputs**: `out` — `bool`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `periodMs` | integer | `1000` | Must be `>= tickMs` of the owning pipeline; validation error otherwise. |
- **Behavior / edge cases**:
  - The internal countdown starts when the pipeline is enabled (not at
    process boot), so periods are relative to enable time and restart from
    zero each time the pipeline is (re-)enabled.
  - If `periodMs` is not an exact multiple of the pipeline's `tickMs`, the
    timer fires on the first tick at or after each period boundary (no
    fractional-tick pulses); actual period therefore has up to one tick of
    jitter.
  - Disabling and re-enabling the pipeline resets the timer's phase.
- **Example**:
  ```json
  {
    "id": "n3",
    "type": "timer",
    "config": { "periodMs": 5000 },
    "position": { "x": 40, "y": 360 }
  }
  ```

### `clock`

Emits `true` while the current time-of-day falls within a configured window,
`false` otherwise. Used for schedule-based automation (e.g. "only at
night").

- **Inputs**: none.
- **Outputs**: `out` — `bool`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `start` | string `"HH:MM"` (24h, local time) | `"00:00"` | |
  | `end` | string `"HH:MM"` (24h, local time) | `"23:59"` | |
  | `days` | array of `"mon".."sun"` | all 7 days | Empty array is a validation error (window that never fires). |
- **Behavior / edge cases**:
  - Uses the Raspberry Pi's local system time and timezone; pinctl does not
    manage NTP/timezone config itself.
  - **Overnight windows are supported**: if `end < start` (e.g.
    `start: "22:00"`, `end: "06:00"`), the window is treated as wrapping
    past midnight, and the day-of-week check applies to the **start** day.
  - Boundaries are inclusive of `start`, exclusive of `end` (`[start, end)`)
    to avoid a window re-triggering exactly at its own boundary on
    re-evaluation.
  - Re-evaluated every tick; no internal state carried between ticks, so a
    runtime clock change (e.g. NTP correction) is picked up immediately.
- **Example**:
  ```json
  {
    "id": "n4",
    "type": "clock",
    "config": { "start": "20:00", "end": "06:00", "days": ["mon","tue","wed","thu","fri","sat","sun"] },
    "position": { "x": 40, "y": 480 }
  }
  ```

---

## Transforms

### `compare`

Compares an input value against either a second input or a configured
constant threshold, producing a `bool`.

- **Inputs**:
  - `in` — `float64` (required).
  - `threshold` — `float64` (optional; if connected, overrides
    `config.threshold`).
- **Outputs**: `out` — `bool`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `op` | `"gt"` \| `"lt"` \| `"eq"` \| `"gte"` \| `"lte"` | `"gt"` | |
  | `threshold` | `float64` | `0` | Used only when the `threshold` input port is unconnected. |
- **Behavior / edge cases**:
  - `eq` compares with an implementation-defined small epsilon (`1e-9`) to
    avoid float equality flakiness — exposed as a fixed constant, not
    user-configurable in the MVP.
  - If both the `threshold` input **and** `config.threshold` are absent
    (input unconnected, config field omitted), the default `0` is used —
    this is the one source node input allowed to fall back to a config
    default rather than error, since `threshold` is optional by design.
  - Output has no hysteresis — a value oscillating around the threshold
    produces `out` chatter every tick; use `threshold-hysteresis` when that
    is undesired.
- **Example** (the README's "compare with threshold"):
  ```json
  {
    "id": "n5",
    "type": "compare",
    "config": { "op": "lt", "threshold": 300 },
    "position": { "x": 240, "y": 120 }
  }
  ```

### `threshold-hysteresis`

Schmitt-trigger style comparator: switches `true` when the input rises above
an "on" level and back to `false` only when it falls below a lower "off"
level, avoiding chatter near a single threshold.

- **Inputs**: `in` — `float64` (required).
- **Outputs**: `out` — `bool`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `onLevel` | `float64` | `600` | Level above which output turns/stays `true`. |
  | `offLevel` | `float64` | `400` | Level below which output turns/stays `false`. Must be `<= onLevel`; validation error otherwise. |
  | `initial` | `bool` | `false` | Output state before the first tick's input is evaluated. |
- **Behavior / edge cases**:
  - Between `offLevel` and `onLevel` (inclusive), output **holds its
    previous state** (this is the point of hysteresis).
  - `onLevel == offLevel` degenerates to a plain `>=` comparator; allowed,
    but the UI shows a warning since it defeats the purpose of the node.
  - State is internal and persists across ticks for the lifetime of the
    pipeline being enabled; disabling/re-enabling resets to `initial`.
- **Example**:
  ```json
  {
    "id": "n6",
    "type": "threshold-hysteresis",
    "config": { "onLevel": 650, "offLevel": 450, "initial": false },
    "position": { "x": 240, "y": 240 }
  }
  ```

### `scale`

Linearly maps an input value from one range to another (e.g. raw analog
`0–1023` to a normalized `0–1`, or to engineering units).

- **Inputs**: `in` — `float64` (required).
- **Outputs**: `out` — `float64`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `inMin` | `float64` | `0` | |
  | `inMax` | `float64` | `1023` | Must differ from `inMin`; validation error if equal (division by zero). |
  | `outMin` | `float64` | `0` | |
  | `outMax` | `float64` | `1` | |
  | `clamp` | `bool` | `true` | If `true`, output is clamped to `[outMin, outMax]` (or the reversed bounds if `outMin > outMax`); if `false`, values outside `[inMin, inMax]` extrapolate linearly. |
- **Behavior / edge cases**:
  - Formula: `out = outMin + (in - inMin) * (outMax - outMin) / (inMax - inMin)`.
  - `outMin > outMax` is allowed (inverting the mapping, e.g. for an
    inverted sensor curve).
  - With `clamp: true` and `inMin > inMax` (an intentionally reversed input
    range), clamping still uses the min/max of the two output bounds
    regardless of order.
- **Example**:
  ```json
  {
    "id": "n7",
    "type": "scale",
    "config": { "inMin": 0, "inMax": 1023, "outMin": 0, "outMax": 1, "clamp": true },
    "position": { "x": 240, "y": 360 }
  }
  ```

### `and`

Boolean AND over two or more inputs.

- **Inputs**: `in1`, `in2`, … `inN` — `bool` (at least 2 required; the
  editor allows adding more input ports on a single `and` node rather than
  chaining multiple binary nodes).
- **Outputs**: `out` — `bool`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `inputCount` | integer | `2` | Number of input ports rendered/validated; `>= 2`. |
- **Behavior / edge cases**: `out = true` only if all connected inputs are
  `true` on that tick; any unconnected declared input port is a validation
  error (no implicit `true` padding).
- **Example**:
  ```json
  {
    "id": "n8",
    "type": "and",
    "config": { "inputCount": 2 },
    "position": { "x": 440, "y": 120 }
  }
  ```

### `or`

Boolean OR over two or more inputs. Same shape as `and`.

- **Inputs**: `in1`, `in2`, … `inN` — `bool` (`>= 2`, via `inputCount`).
- **Outputs**: `out` — `bool`.
- **Config**: `inputCount` (integer, default `2`, same rule as `and`).
- **Behavior / edge cases**: `out = true` if any connected input is `true`.
- **Example**:
  ```json
  {
    "id": "n9",
    "type": "or",
    "config": { "inputCount": 2 },
    "position": { "x": 440, "y": 240 }
  }
  ```

### `not`

Boolean negation.

- **Inputs**: `in` — `bool` (required).
- **Outputs**: `out` — `bool`.
- **Config**: none.
- **Behavior / edge cases**: `out = !in`, evaluated every tick; no state.
- **Example** (the README's "otherwise → turn LED off" branch):
  ```json
  {
    "id": "n10",
    "type": "not",
    "config": {},
    "position": { "x": 440, "y": 360 }
  }
  ```

### `delay`

Propagates a value to its output after a fixed delay, holding the previous
output during the delay window. Works on either value type.

- **Inputs**: `in` — `bool` or `float64` (required; type is fixed by
  `config.kind`).
- **Outputs**: `out` — same type as `in`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `kind` | `"bool"` \| `"float"` | `"bool"` | Must match the connected input's type; mismatch is a validation error. |
  | `delayMs` | integer | `1000` | Rounded down to the nearest multiple of the pipeline's `tickMs` internally. |
  | `initial` | bool or number | `false` / `0` | Output value held before the first delayed value arrives. |
- **Behavior / edge cases**:
  - Implemented as a tick-indexed queue: each tick's input value is enqueued
    with its target output tick; `out` on a given tick is whatever was
    enqueued `delayMs` (rounded to ticks) earlier.
  - Every input change is delayed independently and in order — this node
    delays a signal, it does not debounce/filter it (see `debounce` for
    that). A fast toggle within the delay window still produces a fast
    toggle on the output, just shifted in time.
  - `delayMs` of `0` (or less than one tick) makes the node a same-tick
    passthrough.
- **Example**:
  ```json
  {
    "id": "n11",
    "type": "delay",
    "config": { "kind": "bool", "delayMs": 2000, "initial": false },
    "position": { "x": 640, "y": 120 }
  }
  ```

### `debounce`

Passes a value through only after it has been stable for a minimum
duration, suppressing short-lived flicker (typically on a `digital-in`
signal from a mechanical switch/PIR sensor).

- **Inputs**: `in` — `bool` or `float64` (required; type fixed by
  `config.kind`).
- **Outputs**: `out` — same type as `in`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `kind` | `"bool"` \| `"float"` | `"bool"` | |
  | `stableMs` | integer | `50` | Minimum duration the input must hold a new value before it is reflected on `out`. |
  | `initial` | bool or number | `false` / `0` | Output before the first stable value is observed. |
- **Behavior / edge cases**:
  - Any change in `in` restarts the stability timer; if `in` changes again
    before `stableMs` elapses, the candidate value is discarded and the
    timer restarts against the newest value — `out` only updates once a
    single value has been held continuously for the full window.
  - For `kind: "float"`, "the same value" means exact equality on the
    sampled tick values (no built-in tolerance band); pair with `scale`/
    `compare` upstream if a tolerance is needed.
  - `stableMs` shorter than the pipeline's `tickMs` effectively debounces
    over a single tick (near passthrough, still one tick of latency).
- **Example**:
  ```json
  {
    "id": "n12",
    "type": "debounce",
    "config": { "kind": "bool", "stableMs": 100, "initial": false },
    "position": { "x": 640, "y": 240 }
  }
  ```

### `edge-detect`

Emits a one-tick `true` pulse when a boolean input transitions, otherwise
`false`. Turns a level signal into an event pulse.

- **Inputs**: `in` — `bool` (required).
- **Outputs**: `out` — `bool`.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `edge` | `"rising"` \| `"falling"` \| `"both"` | `"rising"` | |
- **Behavior / edge cases**:
  - `rising`: pulse on `false → true`. `falling`: pulse on `true → false`.
    `both`: pulse on either transition.
  - On the very first tick after the pipeline is enabled, the node has no
    prior value to compare against; it is initialized as if the previous
    value equaled the first observed value (i.e. **no pulse fires on tick
    one**, regardless of what the input is).
  - The output pulse is exactly one tick wide even if the input holds its
    new value for many ticks; sustained-high logic should read the
    upstream level signal directly instead.
- **Example**:
  ```json
  {
    "id": "n13",
    "type": "edge-detect",
    "config": { "edge": "rising" },
    "position": { "x": 640, "y": 360 }
  }
  ```

---

## Sinks

### `device-write`

Writes a value to a bound device on every tick.

- **Inputs**: `in` — `bool` (if device role is `digital-out`) or `float64`
  (if device role is `pwm-out`, expected range `0–255` matching Arduino PWM
  duty resolution).
- **Outputs**: none.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `deviceId` | string | *(required)* | Must reference a device with role `digital-out` or `pwm-out`. |
- **Behavior / edge cases**:
  - Writes happen every tick regardless of whether the value changed
    (idempotent write), so a device disconnect/reconnect mid-pipeline
    converges to the correct state on the next tick without extra logic.
  - For `pwm-out`, values are clamped to `0–255` before being sent to the
    board; out-of-range input values are clamped, not rejected.
  - Two `device-write` nodes (in the same or different pipelines) targeting
    the same `deviceId` is allowed at save time but flagged as a warning in
    the UI (last-write-wins per tick, order is the pipeline's node
    evaluation order — see [07-pipeline-engine.md](./07-pipeline-engine.md));
    it is not a hard validation error since a user may intend one pipeline
    to override another while both are enabled/disabled at different times.
  - If the target device's board is unreachable, the write is dropped for
    that tick and the pipeline is flagged degraded (mirrors `device-read`'s
    stale-input policy); writes are not queued/retried.
- **Example** (the README's "turn LED on/off"):
  ```json
  {
    "id": "n14",
    "type": "device-write",
    "config": { "deviceId": "dev_5kw8m2xr7t" },
    "position": { "x": 840, "y": 120 }
  }
  ```

### `log`

Writes the current input value to the pipeline's log stream, visible live in
the web UI. Entries appear on the `pipeline:{id}` WebSocket debug topic (see
[10-api.md](./10-api.md#websocket-apiv1ws)) alongside the per-tick node
snapshot for the same pipeline. Purely observational — has no effect on
pipeline behavior.

- **Inputs**: `in` — `bool` or `float64` (required; accepts either type,
  logs whichever arrives).
- **Outputs**: none.
- **Config**:
  | Field | Type | Default | Notes |
  |---|---|---|---|
  | `label` | string | node's `id` | Human-readable tag shown alongside each logged value in the UI. |
  | `onChangeOnly` | `bool` | `true` | If `true`, only logs when the value differs from the previously logged value (reduces log volume for steady signals); if `false`, logs every tick. |
- **Behavior / edge cases**:
  - Log entries are transient (in-memory ring buffer per pipeline,
    streamed over WebSocket) — the MVP does not persist log history to
    disk (no database, per the brief); reconnecting clients only see new
    entries going forward plus whatever the runtime's in-memory buffer
    still holds.
  - `onChangeOnly: true` still logs the very first value observed after the
    pipeline is enabled, even though there is no "previous" value to differ
    from.
  - Does not throttle by time, only by value-change — a rapidly toggling
    input with `onChangeOnly: true` can still produce one log line per tick.
- **Example**:
  ```json
  {
    "id": "n15",
    "type": "log",
    "config": { "label": "ldr-raw", "onChangeOnly": true },
    "position": { "x": 840, "y": 240 }
  }
  ```

---

## Summary table

| Name | Category | Inputs (type) | Outputs (type) |
|---|---|---|---|
| `device-read` | source | — | `out` (bool or float64) |
| `constant` | source | — | `out` (bool or float64) |
| `timer` | source | — | `out` (bool) |
| `clock` | source | — | `out` (bool) |
| `compare` | transform | `in` (float64), `threshold` (float64, optional) | `out` (bool) |
| `threshold-hysteresis` | transform | `in` (float64) | `out` (bool) |
| `scale` | transform | `in` (float64) | `out` (float64) |
| `and` | transform | `in1..inN` (bool, N≥2) | `out` (bool) |
| `or` | transform | `in1..inN` (bool, N≥2) | `out` (bool) |
| `not` | transform | `in` (bool) | `out` (bool) |
| `delay` | transform | `in` (bool or float64) | `out` (same type as `in`) |
| `debounce` | transform | `in` (bool or float64) | `out` (same type as `in`) |
| `edge-detect` | transform | `in` (bool) | `out` (bool) |
| `device-write` | sink | `in` (bool or float64) | — |
| `log` | sink | `in` (bool or float64) | — |

---

## Roadmap nodes

Not specced in this document; tracked as v2+ candidates only (see
[00-design-brief.md](./00-design-brief.md) non-goals).

- **`expression`** — user-written formula/scripting node over one or more inputs; deferred until a scripting sandbox/language is chosen.
- **`PID`** — closed-loop proportional-integral-derivative controller node for analog output control.
- **`counter`** — accumulates a count on pulse/edge inputs, with reset input/config.
- **`latch` / `SR`** — set-reset flip-flop node for stateful bistable logic beyond `threshold-hysteresis`.
- **`sample-hold`** — captures and holds an input value on a trigger pulse, until the next trigger.
- **`MQTT-out`** — publishes a value to an external MQTT broker; depends on the roadmap MQTT integration (non-goal for v0.x).
