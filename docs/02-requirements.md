# pinctl — Requirements Specification

Builds on [00-design-brief.md](./00-design-brief.md) (fixed decisions — this
doc must not contradict it) and [01-overview.md](./01-overview.md) (problem,
vision, glossary). Requirement IDs are referenced from commits, issues, and
tests as the project is implemented.

## Functional requirements

### Board management (`FR-BOARD-*`)

| ID | Requirement |
|---|---|
| FR-BOARD-1 | The runtime shall discover and connect to Arduino-class boards over USB CDC serial at 57600 baud using `go.bug.st/serial`. |
| FR-BOARD-2 | The runtime shall support multiple boards simultaneously, one serial port per board. |
| FR-BOARD-3 | The runtime shall establish a StandardFirmata session per board and query its reported pin capabilities on connect. |
| FR-BOARD-4 | The runtime shall restrict connections to serial ports matching a configured allowlist (`/etc/pinctl/config.yaml`). |
| FR-BOARD-5 | The runtime shall expose board state (connected/disconnected, port, capabilities) via the REST API. |
| FR-BOARD-6 | The runtime shall detect board disconnection and reflect it in board state without crashing or requiring a restart. |
| FR-BOARD-7 | The web UI shall list connected boards and their pin capabilities. |

### Device management (`FR-DEVICE-*`)

| ID | Requirement |
|---|---|
| FR-DEVICE-1 | Users shall be able to create a device: a named binding of a board pin to a role (`digital-in`, `digital-out`, `analog-in`, `pwm-out`). |
| FR-DEVICE-2 | The runtime shall validate that a pin's role is compatible with the board's reported pin capabilities before accepting the device. |
| FR-DEVICE-3 | Users shall be able to rename, edit, and delete devices via REST API and web UI. |
| FR-DEVICE-4 | The runtime shall prevent binding the same board pin to more than one device at a time. |
| FR-DEVICE-5 | The web UI shall list all devices grouped by board, showing role and current live value. |
| FR-DEVICE-6 | Deleting a device that is referenced by an enabled pipeline shall be rejected or require explicit confirmation (exact behavior is an implementation decision, but data loss without warning is disallowed). |

### Pipeline editing (`FR-PIPE-EDIT-*`)

| ID | Requirement |
|---|---|
| FR-PIPE-EDIT-1 | Users shall be able to create, rename, and delete pipelines, each with its own enabled/disabled state and tick rate. |
| FR-PIPE-EDIT-2 | The web UI shall provide a node-based visual editor (Vue Flow) for authoring a pipeline's nodes and edges. |
| FR-PIPE-EDIT-3 | The runtime shall support the node catalog's source nodes (device read, constant, timer/clock), transform nodes (compare, threshold with hysteresis, scale/map, AND/OR/NOT, delay, debounce, edge-detect), and sink nodes (device write, log) as defined in the node catalog doc. |
| FR-PIPE-EDIT-4 | The runtime shall validate a pipeline graph before save: no dangling edges, no type mismatches between connected node ports (`bool` vs `float64`), no reference to a deleted device. |
| FR-PIPE-EDIT-5 | The runtime shall support multiple independent pipelines per board and across boards. |
| FR-PIPE-EDIT-6 | Pipeline definitions shall be edited and saved without requiring a runtime restart. |

### Pipeline execution (`FR-PIPE-EXEC-*`)

| ID | Requirement |
|---|---|
| FR-PIPE-EXEC-1 | The runtime shall evaluate each enabled pipeline's DAG on a fixed tick, defaulting to 20 ms, configurable per pipeline. |
| FR-PIPE-EXEC-2 | Node evaluation order within a tick shall respect the DAG's data dependencies (topological order). |
| FR-PIPE-EXEC-3 | Enabling/disabling a pipeline shall take effect without restarting the runtime or affecting other pipelines. |
| FR-PIPE-EXEC-4 | Edge values shall be typed as `bool` or `float64`; analog reads shall be normalized as raw 0–1023 values, with scale/map nodes responsible for range conversion. |
| FR-PIPE-EXEC-5 | A fault in one pipeline (e.g. a node error) shall not stop evaluation of other pipelines. |
| FR-PIPE-EXEC-6 | Device writes (sinks) shall be deduplicated/coalesced per tick so a pin is not written more than once per tick per pipeline. |

### Monitoring / debugging (`FR-MON-*`)

| ID | Requirement |
|---|---|
| FR-MON-1 | The runtime shall expose live pin values over WebSocket. |
| FR-MON-2 | The runtime shall expose live pipeline execution state (per-node current value, last-tick timestamp) over WebSocket for the editor's debugging view. |
| FR-MON-3 | The web UI shall visually overlay live values on the pipeline graph while editing or viewing a pipeline. |
| FR-MON-4 | The runtime shall log board connect/disconnect events and pipeline evaluation errors to a retrievable log (file and/or structured stdout, suitable for `journalctl` under systemd). |
| FR-MON-5 | The REST/WebSocket API shall report the runtime's own health/uptime for basic operational visibility. |

### Persistence (`FR-PERSIST-*`)

| ID | Requirement |
|---|---|
| FR-PERSIST-1 | Board, device, and pipeline definitions shall be persisted as JSON files under `/var/lib/pinctl/` in production, `~/.pinctl/` in dev. |
| FR-PERSIST-2 | The runtime shall load all persisted definitions on startup and reconstruct board connections, devices, and pipelines. |
| FR-PERSIST-3 | Writes to persisted definitions shall be atomic (e.g. write-temp-then-rename) to avoid corrupting state on power loss, given the Pi's SD-card deployment target. |
| FR-PERSIST-4 | The runtime shall detect and reject (with a clear error) malformed or unreadable persisted JSON rather than silently discarding user data. |

### Deployment (`FR-DEPLOY-*`)

| ID | Requirement |
|---|---|
| FR-DEPLOY-1 | The runtime shall build as a single Go binary for linux/arm64 and linux/arm (armv7/armv6). |
| FR-DEPLOY-2 | The Vue 3 web UI shall build to static assets and be embedded into the Go binary via `embed.FS`, requiring no separate web server. |
| FR-DEPLOY-3 | The runtime shall be deployable as a systemd unit, with configuration read from `/etc/pinctl/config.yaml` (port, data dir, serial allowlist). |
| FR-DEPLOY-4 | The runtime shall require no external services (no database server, no message broker) to operate. |
| FR-DEPLOY-5 | Configuration changes (e.g. adding a serial port to the allowlist) shall be applied via config file edit and runtime restart (hot-reload of config is not required for MVP). |

## Non-functional requirements

### Performance (`NFR-PERF-*`)

| ID | Requirement |
|---|---|
| NFR-PERF-1 | Default pipeline tick period is 20 ms; the engine shall support tick periods configurable in the 10 ms – 1000 ms range per pipeline. |
| NFR-PERF-2 | Tick-to-tick jitter shall not exceed 20% of the configured tick period under normal load (a handful of pipelines, tens of nodes) on both target platforms. |
| NFR-PERF-3 | On a Raspberry Pi Zero 2 W, the runtime shall sustain at least 5 concurrent pipelines of up to 20 nodes each, each on a 20 ms tick, without missed ticks under normal load. |
| NFR-PERF-4 | On a Raspberry Pi 4, the runtime shall sustain at least 20 concurrent pipelines of up to 50 nodes each without missed ticks under normal load. |
| NFR-PERF-5 | Steady-state resident memory shall not exceed roughly 100 MB on a Pi Zero 2 W under the load described in NFR-PERF-3 (target, to be validated once implemented). |
| NFR-PERF-6 | Live pin/pipeline-state updates over WebSocket shall be delivered to connected editor clients within one tick period of the underlying change, under normal load. |

### Reliability (`NFR-REL-*`)

| ID | Requirement |
|---|---|
| NFR-REL-1 | On serial disconnect, the runtime shall attempt reconnection to a board automatically when the port reappears, without requiring a runtime restart. |
| NFR-REL-2 | A crash or panic in pipeline evaluation for one pipeline shall be recovered (e.g. via a per-pipeline recover/restart boundary) without taking down the whole runtime process. |
| NFR-REL-3 | On runtime restart (e.g. after a power loss or crash), the runtime shall recover to its last-saved configuration state without manual intervention. |
| NFR-REL-4 | The runtime shall not corrupt persisted JSON definitions on abrupt power loss (see FR-PERSIST-3). |
| NFR-REL-5 | Loss of connection to one board shall not affect pipelines/devices tied to other, still-connected boards. |

### Usability (`NFR-USE-*`)

| ID | Requirement |
|---|---|
| NFR-USE-1 | Common actions (add device, create pipeline, wire two nodes, enable/disable a pipeline) shall be achievable through the web UI without editing files or restarting the runtime. |
| NFR-USE-2 | The web UI shall surface pipeline validation errors (FR-PIPE-EDIT-4) inline at the offending node/edge, not as an opaque save failure. |
| NFR-USE-3 | The web UI shall be usable on a single laptop/desktop browser screen without requiring a specific display size beyond a standard laptop viewport (no mobile-first requirement for MVP). |
| NFR-USE-4 | Getting from a freshly flashed StandardFirmata Arduino to a working "read sensor → light LED" pipeline shall require no more documentation than the README's example flow. |

### Portability (`NFR-PORT-*`)

| ID | Requirement |
|---|---|
| NFR-PORT-1 | The runtime shall run on linux/arm64 and linux/arm (Raspberry Pi OS) as the primary supported targets. |
| NFR-PORT-2 | The runtime shall also build and run on common development platforms (linux/amd64, darwin/arm64) for local development, even though only Pi targets are officially supported in production. |
| NFR-PORT-3 | Boards other than Arduino Uno/Nano (ATmega328P) that speak StandardFirmata should work without runtime code changes, though only Uno/Nano are tested in the MVP. |

## Constraints

- Single Go binary, no external runtime services (database, broker, cache) —
  per the brief's fixed decisions.
- Serial transport is fixed to USB CDC at 57600 baud via `go.bug.st/serial`;
  no alternative transport (e.g. Bluetooth, network-attached boards) in
  scope for v0.x.
- Agent protocol is StandardFirmata for the MVP; no firmware changes are in
  scope, and no custom protocol work happens unless a documented Firmata
  limitation is hit.
- Persistence is JSON files on local disk only; no database and no
  networked storage in the MVP.
- No authentication layer in the MVP; the runtime must not be exposed
  beyond a trusted LAN.
- Deployment target is Raspberry Pi hardware (Zero 2 W and 4 are the two
  reference targets for performance requirements); resource budgets are set
  accordingly.

## Assumptions

- The Raspberry Pi has a writable local filesystem for `/var/lib/pinctl/`
  (or `~/.pinctl/` in dev) and `/etc/pinctl/config.yaml` is present or
  defaulted.
- Users operate on a trusted local network (no hostile actors on the LAN,
  consistent with the no-auth MVP decision).
- Exactly one pinctl runtime instance manages a given set of boards; no
  multi-runtime coordination or clustering is assumed.
- Arduino boards are running unmodified StandardFirmata and are not shared
  with any other host/process while connected to pinctl.
- Users have physical access to plug in boards; there is no remote
  provisioning or zero-touch onboarding in the MVP.

## MVP scope

| Capability | In MVP | Out of MVP (roadmap / non-goal) |
|---|---|---|
| Arduino agent protocol | StandardFirmata | Custom `pinctl-agent` protocol (only if Firmata limits are hit) |
| Serial transport | USB CDC, 57600 baud | Bluetooth / network-attached boards |
| Boards supported | Arduino Uno/Nano (ATmega328P), multiple boards per Pi | Other Firmata-capable boards (should work, untested) |
| Device roles | `digital-in`, `digital-out`, `analog-in`, `pwm-out` | `servo`, `i2c` |
| Pipeline engine | Tick-based DAG evaluation, default 20 ms | Push/event-driven evaluation |
| Node catalog | Sources (device read, constant, timer/clock), transforms (compare, threshold+hysteresis, scale/map, AND/OR/NOT, delay, debounce, edge-detect, **expression** — shipped post-MVP in v0.5), sinks (device write, log) | — |
| Persistence | JSON files under `/var/lib/pinctl/` or `~/.pinctl/` | SQLite / database (only if history/metrics land) |
| API | REST (CRUD) + WebSocket (live values, pipeline state, editor debugging) | — |
| Web UI | Vue 3 + Vite + Pinia + Vue Flow, embedded static build | — |
| Auth | None (LAN tool) | Single-password + token auth |
| Integrations | None | MQTT, Home Assistant, cloud connectivity |
| Multi-user | No | Multi-user support |
| Firmware updates | None (agent never reflashed) | OTA flashing |
| Real-time guarantees | Tick-rate-bounded, best-effort | Guarantees below the tick rate |
| Deployment | Single Go binary, systemd unit, linux/arm64 + arm | — |

See [01-overview.md](./01-overview.md#non-goals) for the narrative version of
the non-goals this table encodes, and
[00-design-brief.md](./00-design-brief.md#non-goals-v0x) for the source
non-goals list.
