# pinctl — Design Brief (source of truth)

This file fixes the core design decisions all other docs must follow.
If a doc contradicts this brief, the brief wins.

## What pinctl is

Pipeline-based I/O controller. Arduino runs a **generic, never-reflashed agent**
exposing raw pins over USB serial. A Raspberry Pi runs the **pinctl runtime**
(single Go binary) that executes user-defined pipelines (dataflow graphs) against
those pins, and serves a **Vue 3 web UI** with a node-based visual editor.

## Fixed decisions

| Area | Decision | Rationale / alternative |
|---|---|---|
| MCU agent (MVP) | **StandardFirmata** on Arduino Uno | Battle-tested, zero firmware work, existing Go clients. Custom `pinctl-agent` protocol is a v2 option only if Firmata limits are hit (documented, not built). |
| Serial transport | USB CDC serial, 57600 baud (Firmata default), `go.bug.st/serial` | |
| Runtime | Single Go binary on Raspberry Pi (linux/arm64 + arm), no external services | systemd unit for deployment |
| Pipeline engine | Tick-based evaluation of a dataflow DAG, default tick 20 ms per pipeline | Push/event-driven engine is premature; tick is debuggable and deterministic |
| Persistence | Pipeline/device definitions = JSON files under `/var/lib/pinctl/` (dev: `~/.pinctl/`) | No database in MVP. SQLite only if/when history/metrics land (roadmap) |
| API | REST (JSON) for CRUD + WebSocket for live pin values, pipeline state, editor debugging | |
| Web UI | Vue 3 + Vite + Pinia + **Vue Flow** for node editor; static build embedded in Go binary via `embed.FS` | One artifact to deploy |
| Auth (MVP) | None — LAN tool. Roadmap: optional single-password + token | Documented as explicit non-goal for v0.x |
| Boards (MVP) | Arduino Uno/Nano (ATmega328P). Multiple boards per Pi supported (one serial port each) | Other Firmata-capable boards "should work", untested |
| Config | Single YAML file `/etc/pinctl/config.yaml` (port, data dir, serial allowlist) | |

## Core domain model

- **Board** — a connected microcontroller (serial port, Firmata session, pin capabilities).
- **Device** — user-named binding to a board pin with a role: `digital-in`, `digital-out`, `analog-in`, `pwm-out` (v2: servo, i2c). E.g. "LED on D13", "LDR on A0".
- **Pipeline** — a named dataflow graph of nodes + edges, enabled/disabled, owns a tick rate.
- **Node** — typed unit: sources (device read, constant, timer/clock), transforms (compare, threshold w/ hysteresis, scale/map, and/or/not, delay, debounce, edge-detect), sinks (device write, log). Node catalog doc defines exact set + JSON schema.
- Values on edges: `bool` or `float64` (analog normalized 0–1023 raw, scale node converts).

## Repository layout (planned)

```
pinctl/
  cmd/pinctl/          # main
  internal/board/      # serial + firmata session mgmt
  internal/engine/     # pipeline engine (graph, nodes, scheduler)
  internal/store/      # JSON file persistence
  internal/api/        # REST + WS handlers
  web/                 # Vue 3 app (built dist embedded)
  docs/
```

## Non-goals (v0.x)

- No cloud, no MQTT/Home Assistant (roadmap item), no auth, no multi-user,
- No custom firmware, no OTA flashing, no realtime guarantees below tick rate,
- No scripting language inside nodes (a `expression` node is a roadmap item).
