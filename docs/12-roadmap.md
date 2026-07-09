# pinctl — Roadmap

This document sequences the work described in
[02-requirements.md](./02-requirements.md) into milestones, and lists what is
deliberately deferred past MVP. It follows
[00-design-brief.md](./00-design-brief.md); nothing here adds a feature the
brief doesn't already allow for as a documented v2 option.

## Table of contents

- [Milestones](#milestones)
- [Post-MVP v1.x backlog](#post-mvp-v1x-backlog)
- [Non-goals (restated)](#non-goals-restated)
- [Risks](#risks)

## Milestones

Each milestone is a runnable increment, not a sprint — there is no fixed
calendar attached to these; they are ordered by dependency (you can't build
the engine before you can talk to a board, you can't build a UI before the
engine has something to visualize).

| Milestone | Goal |
|---|---|
| [v0.1](#v01--walking-skeleton) | Prove the serial/Firmata path works end to end, no engine, no UI. |
| [v0.2](#v02--engine--minimal-node-set) | Pipelines execute and persist, still no UI (API/CLI only). |
| [v0.3](#v03--web-ui-editor) | Vue Flow editor with live values — the product becomes usable by a non-developer. |
| [v0.4 — MVP](#v04--mvp) | Everything in [02-requirements.md](./02-requirements.md)'s MVP scope table, installable and documented. |

### v0.1 — walking skeleton

**Goal:** confirm the riskiest assumption first — that a Raspberry Pi can
reliably drive a StandardFirmata Arduino over serial from Go — before any
engine or UI work is invested.

Scope:

- `internal/board`: open one serial port with `go.bug.st/serial`, run a
  StandardFirmata handshake, query pin capabilities (FR-BOARD-1, FR-BOARD-3).
- Minimal REST endpoints to read a digital/analog pin and write a
  digital/PWM pin directly (no device model, no pipeline, no persistence).
- No web UI — `curl` or a REST client is the only "editor" at this stage.
- No config file beyond a hardcoded or flag-passed serial port.

Done when:

- A single Uno running stock StandardFirmata can be connected, and a pin can
  be read and written via REST from a fresh process start.
- Unplugging the board during a session doesn't crash the process (bare
  error return is acceptable here — the reconnect loop lands in v0.4).

### v0.2 — engine + minimal node set

**Goal:** the tick-based DAG engine exists and pipelines are persisted, still
without a UI — API and JSON files are the only interface.

Scope:

- `internal/engine`: graph compilation, topo sort, tick scheduler
  (ADR-1 in [03-architecture.md](./03-architecture.md)).
- Minimal node set: device read, constant, compare, device write (enough to
  build the README's LDR→LED example).
- `internal/store`: JSON persistence for devices and pipelines, atomic
  writes, schema validation on save (FR-PERSIST-1, FR-PERSIST-3).
- REST CRUD for devices/pipelines; enable/disable a pipeline.
- Board model gains multi-device binding (one board, several pins bound to
  devices), still single-board.

Done when:

- The LDR→LED example from the README runs end to end: hand-author the
  pipeline JSON (or POST it via REST), enable it, and watch the LED respond
  to covering the sensor — no restart required to pick up the change.
- Engine simulation tests (see [13-testing.md](./13-testing.md)) cover the
  four node types with a fake board.

### v0.3 — web UI editor

**Goal:** replace hand-authored JSON with the Vue Flow visual editor and make
pipeline state observable live, closing the loop for a non-developer user.

Scope:

- `web/`: Vue 3 + Vite + Pinia scaffold, Vue Flow canvas, node palette for
  the v0.2 node set.
- WebSocket: live pin values and per-node tick snapshots streamed to the
  editor (FR-MON-1, FR-MON-2, FR-MON-3).
- Board/device management screens (list boards, add/edit/delete devices).
- Still single-board; still the v0.2 node set (no hysteresis, debounce,
  timers yet — those land in v0.4 alongside the rest of the catalog).

Done when:

- A user can build the LDR→LED pipeline entirely by dragging nodes in the
  browser, save it, and see live values overlaid on the graph while it runs.
- Closing and reopening the browser tab reconnects the WebSocket and
  resumes live updates without a page-breaking error.

### v0.4 — MVP

**Goal:** everything promised in [02-requirements.md](./02-requirements.md)'s
MVP scope table is implemented, documented, and installable by someone who
isn't the author.

Scope:

- Full node catalog per [08-node-catalog.md](./08-node-catalog.md): timer/
  clock source, threshold+hysteresis, scale/map, AND/OR/NOT, delay,
  debounce, edge-detect, log sink.
- Pipeline validation surfaced inline in the editor (FR-PIPE-EDIT-4,
  NFR-USE-2), not just as a REST 400.
- Hot-reload: save-without-restart for pipeline edits (FR-PIPE-EDIT-6),
  already exercised informally since v0.2 but now covered by tests and
  documented behavior.
- Multi-board support: several boards connected simultaneously, board
  disconnect/reconnect handled per
  [03-architecture.md](./03-architecture.md#failure-modes--handling)
  without taking down other boards' pipelines.
- Serial allowlist enforcement from `config.yaml` (FR-BOARD-4).
- Install script and systemd unit per [14-deployment.md](./14-deployment.md).
- Docs complete (this set) and a README quickstart that matches
  NFR-USE-4 (no more documentation needed than the example flow).
- CI producing a release binary per [13-testing.md](./13-testing.md).

Done when:

- Every requirement in [02-requirements.md](./02-requirements.md)'s "In MVP"
  column is met, and the MVP scope table's "Out of MVP" column is what's
  left in the [backlog](#post-mvp-v1x-backlog) below.
- A fresh Raspberry Pi OS install can go from `curl | sh` to a working
  pipeline using only the README and [14-deployment.md](./14-deployment.md).

## Post-MVP v1.x backlog

Ordered roughly by expected demand, not commitment — none of these are
scheduled; each entry names what would trigger picking it up.

**Auth (single password + token).** What: a lightweight login gate —
one operator password, issuing a bearer token for the API/WS, no user
accounts. Why deferred: the brief scopes v0.x as a trusted-LAN tool with no
auth (explicit non-goal); building it early would add a stateful concern
(token storage, session handling) before there's any real exposure. Trigger:
someone wants pinctl reachable outside a fully trusted LAN segment (e.g.
a shared home network, a Pi exposed via Tailscale to multiple people).

**Expression node.** What: a node whose config is a small user-written
expression (arithmetic/boolean) evaluated against its inputs, instead of
composing primitive compare/scale/boolean nodes. Why deferred: the brief
explicitly excludes "a scripting language inside nodes" from v0.x — it's a
sandboxing and validation problem (what can the expression touch? how do
errors surface?) that isn't worth solving until the primitive node set
proves insufficient. Trigger: real pipelines in the wild routinely chain
4+ primitive transform nodes to express one piece of logic a one-line
expression would replace.

**Servo / I2C device roles.** What: extend the device role enum beyond
`digital-in/out`, `analog-in`, `pwm-out` to `servo` (angle-based PWM) and
`i2c` (addressable sensor/actuator buses). Why deferred: StandardFirmata
supports both, but the MVP node catalog and value model (`bool`/`float64` on
edges) were designed against the four simple roles first; I2C in particular
needs a per-device-type config schema (address, register map) the current
data model doesn't have room for yet. Trigger: MVP is stable and someone
wants to drive a servo or an I2C sensor (e.g. a BMP280) without hand-editing
Firmata calls outside pinctl.

**MQTT / Home Assistant bridge.** What: publish device values and accept
commands over MQTT, with a Home Assistant discovery integration, so pinctl
devices show up as HA entities. Why deferred: it's an external-integration
surface the brief calls out as a non-goal for v0.x ("no cloud, no MQTT/Home
Assistant"), and it implies a new dependency (an MQTT client) and a second
long-lived connection pattern alongside the WebSocket. Trigger: multiple
users ask for pinctl devices to appear in an existing home-automation setup
rather than standing alone.

**History / metrics with SQLite.** What: retain pin value and pipeline
tick history beyond the current in-memory snapshot, queryable for graphs
("show me the last 24h of this sensor"). Why deferred: ADR-3 in
[03-architecture.md](./03-architecture.md) fixes JSON files for
config precisely because there's no query need yet; history is the
one workload JSON files are a poor fit for. Trigger: users want a graph of a
value over time, not just its current reading — at that point SQLite lands
alongside (not replacing) the JSON config store.

**Custom `pinctl-agent` firmware protocol.** What: replace StandardFirmata
with a purpose-built serial protocol for boards, allowing bulk reads,
board-side debouncing, or other capabilities Firmata doesn't expose. Why
deferred: ADR-2 in [03-architecture.md](./03-architecture.md) is explicit
that this is gated on hitting a real Firmata limitation, not built
speculatively — it means firmware development and flashing tooling that
today's zero-firmware-work pitch deliberately avoids. Trigger: a specific,
documented Firmata limitation (message rate, missing capability, latency)
that blocks a use case StandardFirmata cannot serve.

**ESP32 support.** What: run the agent side on ESP32 boards (WiFi-capable,
more pins, faster clock) in addition to Uno/Nano. Why deferred: NFR-PORT-3
already expects other Firmata-capable boards to "work" untested, so this is
mostly a testing/documentation effort rather than new code — but ESP32's
WiFi capability also raises the question of a network transport, which is
explicitly out of scope (serial-only) for v0.x and would need its own
design pass. Trigger: ESP32 users report StandardFirmata-over-serial working
today and ask for it to be an officially tested target, or someone pushes
for network-attached boards as a follow-on.

## Non-goals (restated)

Carried forward from [00-design-brief.md](./00-design-brief.md#non-goals-v0x)
and [02-requirements.md](./02-requirements.md) — restated here because a
roadmap is exactly where scope creep tends to sneak back in:

- **No cloud.** pinctl is a LAN tool talking to hardware plugged into the
  same Raspberry Pi it runs on; there is no remote-access, multi-site, or
  hosted variant planned.
- **No multi-user.** One operator, one runtime, one set of boards. No
  accounts, roles, or per-user pipelines — even once [auth](#post-mvp-v1x-backlog)
  lands, it remains a single shared password, not user management.
- **No realtime guarantees below the tick rate.** The tick-based engine
  (ADR-1) is a deliberate ceiling on responsiveness; nothing on this roadmap
  changes that trade-off. A use case that needs sub-tick determinism belongs
  in firmware, not pinctl.

## Risks

| Risk | Description | Mitigation |
|---|---|---|
| **Firmata limitations discovered late** | StandardFirmata's message set, sampling behavior, or throughput at 57600 baud could turn out to be too limited for a node type or scale of pipeline planned for MVP (e.g. reporting rate for analog pins, no built-in debounce). | Node catalog design ([08-node-catalog.md](./08-node-catalog.md)) intentionally keeps debounce/edge-detect in the engine, not the firmware, so most gaps are absorbable in software. If a genuine protocol limitation is hit, it's documented against ADR-2 rather than worked around silently, and only then does the custom-protocol backlog item get triggered. |
| **Serial reliability on Pi hardware** | USB serial over a Pi's USB controller (especially Zero/Zero 2 W, which shares bus bandwidth with WiFi) has a history of flaky enumeration, port renumbering (`/dev/ttyACM0` becoming `ttyACM1` after replug), and occasional silent stalls. | Reconnect-with-backoff and the serial allowlist (already designed in [03-architecture.md](./03-architecture.md#failure-modes--handling)) absorb transient failures; the udev pinning example in [14-deployment.md](./14-deployment.md) addresses port renumbering for setups with multiple boards. Tick-overrun stats (per [06-runtime.md](./06-runtime.md)) make silent stalls observable instead of invisible. |
| **Vue Flow fit for the editor** | Vue Flow is chosen for the node editor (per the brief) but hasn't been validated against pinctl's specific needs: live-value overlays updating at tick rate, inline validation error display, custom port typing (`bool` vs `float64`). If any of these turn out to be awkward to build on top of Vue Flow, UI velocity in v0.3 suffers. | v0.3 is scoped specifically to surface this early — building the live-overlay and validation-error UI is one of its explicit goals, not deferred to v0.4. If Vue Flow proves a poor fit, the cost is contained to the `web/` package (per the architecture's dependency direction, `web` only talks to `internal/api`); no backend rework would be required to swap editor libraries. |
