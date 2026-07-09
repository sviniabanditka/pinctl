# pinctl — Project Overview

See [00-design-brief.md](./00-design-brief.md) for the fixed design decisions this
document builds on, and [02-requirements.md](./02-requirements.md) for the
functional/non-functional requirements derived from it.

## Problem statement

Arduino-class boards are cheap, reliable, and well understood — but changing
*behavior* on one means changing *firmware*: editing a sketch, recompiling,
re-flashing over serial, and power-cycling attached hardware to test. This has
real costs for the kind of tinkering pinctl targets:

- **Slow iteration.** A one-line change to a threshold or a timing value costs
  a full compile/upload/reset cycle, often 10–30 seconds, breaking flow when
  tuning a sensor by hand.
- **No visibility into running behavior.** Once flashed, the logic is a black
  box. Debugging means adding `Serial.print` calls, recompiling again, and
  reading a scrolling log — there is no live view of pin values or decision
  state.
- **Logic and hardware access are tangled together.** Every sketch re-derives
  the same boilerplate (debounce, hysteresis, edge detection, delays) inside
  `loop()`, coupled to the exact pins in use, making it hard to reuse or
  reason about behavior independent of wiring.
- **All-or-nothing deployment.** There is no way to enable/disable one
  behavior while others keep running, or to inspect one pipeline's state
  without halting the whole board.

None of this is a hardware problem — the microcontroller is perfectly capable
of driving pins. It is a *tooling* problem: logic is compiled into the one
place (the MCU) where it is hardest to see and change.

## Vision

Keep the microcontroller doing what it is good at — fast, deterministic pin
I/O — and move all behavior definition off of it. The Arduino runs one
generic, never-reflashed agent for its entire lifetime. All logic (reading
sensors, comparing values, applying delays, deciding outputs) lives in the
**pinctl runtime** on a Raspberry Pi, expressed as visual, editable
**pipelines**. Changing behavior means editing a pipeline and saving — no
compiler, no upload, no reset, and the running state is always inspectable
through the web UI.

## Target users

- **Makers** who want to prototype physical-computing projects (blink an LED,
  read a sensor, drive a relay) without relearning embedded C++ or waiting on
  recompiles for every tweak.
- **Home-automation tinkerers** wiring up simple sensors/actuators (motion,
  light, temperature, relays) who want quick, inspectable logic without
  standing up a full home-automation stack.
- **Educators** teaching I/O, sensors, and control-flow concepts, who need
  behavior that students can see running, step through, and modify live
  rather than a fixed compiled binary.

pinctl is explicitly *not* aimed at production/industrial control, safety-
critical automation, or users who need firmware-level real-time guarantees —
see [Non-goals](#non-goals).

## Key concepts glossary

| Term | Definition |
|---|---|
| **Board** | A connected microcontroller: a serial port, its Firmata session, and the pin capabilities it reports. One board = one physical Arduino attached over USB. |
| **Device** | A user-named binding to a specific pin on a board with a role — `digital-in`, `digital-out`, `analog-in`, or `pwm-out` (v2: servo, i2c). Example: "LED on D13", "LDR on A0". Devices are the addressable endpoints pipelines read from and write to. |
| **Pipeline** | A named dataflow graph (nodes + edges) that is enabled/disabled as a unit and owns its own tick rate. Pipelines are the unit of user-authored behavior. |
| **Node** | A typed unit inside a pipeline graph: a source (device read, constant, timer/clock), a transform (compare, threshold with hysteresis, scale/map, boolean logic, delay, debounce, edge-detect), or a sink (device write, log). Edge values are `bool` or `float64`. |
| **Agent** | The generic firmware running on the Arduino (StandardFirmata in the MVP) that exposes raw pins over USB serial and is never reflashed to add behavior. |
| **Runtime** | The single Go binary running on the Raspberry Pi that owns boards, executes pipelines tick-by-tick, persists definitions, and serves the REST/WebSocket API and the built web UI. |

## How it compares to alternatives

| Tool | Where logic runs | Editing model | Reflash needed? | Live inspection | Fit for pinctl's use case |
|---|---|---|---|---|---|
| **Raw Arduino sketches** | On the MCU | C++ source | Yes, every change | None built-in | Full control, but every tweak costs a compile/upload cycle |
| **Node-RED** | Server (Node.js) | General-purpose node-based flows | No | Yes (flow debug) | Very flexible, but general-purpose — no first-class board/pin/device model, heavier runtime |
| **Firmata clients** (e.g. Johnny-Five) | Host machine, via scripting | Code (JS/Python) against a Firmata client library | No | Depends on script | Same "logic off the MCU" idea, but you're back to writing/deploying host code, not a visual editor |
| **Home Assistant** | Server (Python) | YAML automations / UI | No | Yes (via HA UI) | Built for a whole smart-home ecosystem and integrations; heavy for "wire two nodes together on a Pi" |
| **ESPHome** | On the MCU (compiled) | YAML, compiled to firmware | Yes, on every config change | Via Home Assistant | Great for ESP-family devices with HA integration, but still a compile-and-flash workflow |
| **pinctl** | Raspberry Pi (runtime) | Visual pipeline editor (Vue Flow) | Never (generic agent) | Yes (REST + WebSocket live state) | Purpose-built for "edit behavior, not firmware," with a minimal domain model (Board/Device/Pipeline/Node) |

pinctl's niche is the intersection: a first-class, minimal domain model built
specifically around boards/pins/pipelines (unlike Node-RED or Home
Assistant's broader scope), with logic fully off the MCU and never requiring
a reflash (unlike raw sketches or ESPHome).

## Guiding principles

These follow directly from the brief's philosophy and fixed decisions:

- **Firmware is generic and stable.** The Arduino agent (StandardFirmata) is
  never reflashed to change behavior; all behavior lives in pipelines on the
  Pi.
- **Behavior is configurable, not hardcoded.** Pipelines are data (JSON),
  editable at runtime, not compiled artifacts.
- **Inspectable and debuggable by default.** Live pin values, pipeline state,
  and editor debugging are first-class (WebSocket), not something bolted on
  later.
- **Pipelines over embedded state machines.** A dataflow graph of small,
  typed nodes is easier to reason about, test, and reuse than ad hoc `loop()`
  logic.
- **One artifact, one process.** A single Go binary with the web UI embedded
  (`embed.FS`) and no external services (no database, no message broker)
  keeps deployment on a Pi trivial.
- **Deterministic over clever.** A fixed-tick evaluation loop (default 20 ms)
  is chosen over push/event-driven evaluation because it is predictable and
  debuggable; premature event-driven complexity is avoided until a real need
  is demonstrated.
- **Simplicity of the MVP is deliberate, not accidental.** StandardFirmata
  instead of a custom protocol, JSON files instead of a database, no auth —
  each is a documented, revisitable choice, not an oversight (see the brief's
  "Fixed decisions" table).

## Non-goals

Carried over verbatim in spirit from the design brief (v0.x scope):

- No cloud connectivity, no MQTT or Home Assistant integration (both are
  roadmap items, not MVP).
- No authentication or multi-user support (LAN-only tool for v0.x; optional
  single-password + token auth is roadmap).
- No custom firmware and no OTA flashing — the agent protocol is
  StandardFirmata for the MVP; a custom `pinctl-agent` protocol is a v2 option
  only if Firmata proves limiting.
- No real-time guarantees below the pipeline tick rate — pinctl is not a
  real-time control system.
- No scripting/expression language inside nodes for the MVP (an `expression`
  node is a roadmap item).
- No database — persistence is JSON files under `/var/lib/pinctl/` (or
  `~/.pinctl/` in dev); SQLite is only being considered if/when
  history/metrics land.

See [02-requirements.md](./02-requirements.md) for how these non-goals map to
concrete MVP scope (in/out table) and requirement IDs.
