# pinctl

pinctl is an open-source pipeline-based I/O controller for Arduino-class boards.

It lets you define device behavior (LEDs, sensors, relays, PWM outputs, etc.)
using visual pipelines instead of modifying Arduino firmware. The
microcontroller runs a single generic agent (StandardFirmata) and exposes its
pins over a serial protocol; all logic — conditions, delays, comparisons,
automation — runs on a Raspberry Pi inside the pinctl runtime and is
configured through a web-based visual editor. Hardware behavior is fully
reconfigurable without ever recompiling or reflashing firmware.

## Status

pinctl is at **v0.4 — MVP**: config-driven startup, multi-board discovery
with reconnect-with-backoff, a persisted device/pipeline JSON store, the tick
engine driving every enabled pipeline, a REST + WebSocket API, and the Vue
Flow web editor, all shipped as a single static binary with the UI embedded.
See [docs/](./docs/) for the full design (start at
[docs/00-design-brief.md](./docs/00-design-brief.md)) and
[docs/12-roadmap.md](./docs/12-roadmap.md) for what's deliberately out of
scope for now.

## Quickstart

### 1. Install pinctl on the Raspberry Pi

```sh
curl -fsSL https://pinctl.example/install.sh | sh
```

This installs the binary to `/usr/local/bin/pinctl`, creates a `pinctl`
system user and `/etc/pinctl` + `/var/lib/pinctl`, drops a starter
`/etc/pinctl/config.yaml`, and installs (but does not start) the systemd
unit. Review `/etc/pinctl/config.yaml` — in particular `serial.allowlist` —
then:

```sh
sudo systemctl enable --now pinctl
```

No GitHub release has been tagged yet, so until then, build locally and
install that binary instead:

```sh
cd web && npm ci && npm run build && cd ..
go build -o pinctl ./cmd/pinctl
sudo ./deploy/install.sh ./pinctl
```

Full install/config/systemd/udev/troubleshooting reference:
[docs/14-deployment.md](./docs/14-deployment.md).

### 2. Flash StandardFirmata onto the board (once, ever)

This is the *only* firmware step in pinctl's lifecycle — done once per
board, before it's ever plugged into the Pi. Using
[`arduino-cli`](https://arduino.github.io/arduino-cli/) (the Arduino IDE's
**File → Examples → Firmata → StandardFirmata** works identically):

```sh
arduino-cli core install arduino:avr
arduino-cli lib install Firmata Servo
arduino-cli compile --fqbn arduino:avr:uno \
    "$(arduino-cli lib list --format json | jq -r '.[] | select(.library.name=="Firmata") | .library.install_dir')/examples/StandardFirmata"
arduino-cli upload -p /dev/ttyACM0 --fqbn arduino:avr:uno \
    "$(arduino-cli lib list --format json | jq -r '.[] | select(.library.name=="Firmata") | .library.install_dir')/examples/StandardFirmata"
```

The `Servo` library is required alongside `Firmata` — StandardFirmata's
`.ino` includes it even though the sketch's name doesn't mention it, and
`arduino-cli compile` fails without it. (Discovered the hard way; see
[docs/14-deployment.md#flashing-standardfirmata](./docs/14-deployment.md#flashing-standardfirmata).)

### 3. Open the UI and build a pipeline

Point a browser at `http://<pi-address>:8080` (or whatever `http.addr` is
set to). Turn on an LED when it gets dark:

1. Connect a light sensor (LDR) and an LED to the Arduino.
2. Plug the Arduino into the Pi; it should appear as `connected` in the UI.
3. Add both as devices in the pinctl web interface.
4. Create a pipeline: read light sensor → compare value with threshold → if
   below threshold, turn LED on, otherwise turn LED off.
5. Save and enable the pipeline.

The behavior changes instantly, live, without reflashing the Arduino.

## Development

```sh
# frontend
cd web && npm ci && npm run dev     # dev server with hot reload
npm run build                        # produces web/dist, embedded via web/embed.go
npm run test                         # Vitest unit tests

# backend
go build -o pinctl ./cmd/pinctl
./pinctl -port /dev/ttyACM0          # dev convenience: single board, bypasses
                                      # discovery/allowlist entirely
./pinctl -config ./config.yaml -data ./devdata -listen :8080
go test ./...
```

Useful flags (`pinctl -h` for the full list):

| Flag | Purpose |
|---|---|
| `-config` | Path to `config.yaml` (default: `/etc/pinctl/config.yaml`, then `~/.pinctl/config.yaml`; missing file = built-in defaults). |
| `-port` | Dev convenience: connect to exactly this serial port, bypassing discovery and `serial.allowlist`. |
| `-listen` | Override `http.addr` from config. |
| `-data` | Override `data_dir` from config. |

In dev mode (no systemd, no `-config`), pinctl reads/writes
`~/.pinctl/config.yaml` and `~/.pinctl/` as the data dir — nothing under
`/etc` or `/var` is touched.

## Docs

- [docs/00-design-brief.md](./docs/00-design-brief.md) — philosophy and scope
- [docs/02-requirements.md](./docs/02-requirements.md) — functional/non-functional requirements
- [docs/03-architecture.md](./docs/03-architecture.md) — component boundaries, data flow
- [docs/06-runtime.md](./docs/06-runtime.md) — process model, config schema, logging
- [docs/08-node-catalog.md](./docs/08-node-catalog.md) — pipeline node types
- [docs/10-api.md](./docs/10-api.md) — REST + WebSocket API
- [docs/13-testing.md](./docs/13-testing.md) — test strategy and CI
- [docs/14-deployment.md](./docs/14-deployment.md) — install, config, systemd, udev, troubleshooting

## Philosophy

- Firmware should be generic and stable
- Behavior should be configurable, not hardcoded
- Hardware automation should be inspectable and debuggable
- Pipelines are easier to reason about than embedded state machines

## Tech stack

- Raspberry Pi (arm64/armhf) running the pinctl binary
- Arduino Uno or compatible board running StandardFirmata over USB serial
- Go backend (tick engine, REST/WS API, JSON store) + Vue 3 web editor, embedded into one binary
