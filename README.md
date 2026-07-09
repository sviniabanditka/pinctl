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

**v0.5.0** — the MVP, plus an expression node and the instrument-panel UI.
Config-driven startup, multi-board discovery with reconnect-and-backoff,
a persisted device/pipeline JSON store, the tick engine, a REST + WebSocket
API, and the Vue Flow editor — all in a single static binary with the UI
embedded. Verified end to end on an Arduino Uno.

See [docs/](./docs/) for the full design (start at
[docs/00-design-brief.md](./docs/00-design-brief.md)) and
[docs/12-roadmap.md](./docs/12-roadmap.md) for what is deliberately out of
scope.

---

# Setup guide: Raspberry Pi + Arduino Uno

The whole path, from a fresh Pi to an LED that reacts to darkness. Roughly
fifteen minutes, most of it waiting on downloads.

## What you need

| | |
|---|---|
| Raspberry Pi | any model running Raspberry Pi OS (64-bit or armhf), on your network |
| Arduino | Uno or Nano (ATmega328P). Other Firmata-capable boards should work, untested |
| USB cable | A-to-B for an Uno — data, not charge-only |
| Optional, for the example | an LED + 220Ω resistor, a photoresistor (LDR) + 10kΩ resistor, a breadboard |

The Uno's built-in LED on pin 13 works for the LED half of the example, so you
can do a first run with nothing but the board and a cable.

## Step 1 — Flash StandardFirmata onto the Arduino (once, ever)

This is the **only** firmware step in pinctl's lifetime. After this the board
is generic: pinctl drives it over serial and never reflashes it.

Do this from whatever machine is convenient — your laptop or the Pi itself.
In the Arduino IDE it's **File → Examples → Firmata → StandardFirmata → Upload**.
With [`arduino-cli`](https://arduino.github.io/arduino-cli/):

```sh
arduino-cli core update-index
arduino-cli core install arduino:avr
arduino-cli lib install Firmata Servo

SKETCH=~/Arduino/libraries/Firmata/examples/StandardFirmata
arduino-cli board list                       # find your board's port
arduino-cli compile --fqbn arduino:avr:uno "$SKETCH"
arduino-cli upload -p /dev/ttyACM0 --fqbn arduino:avr:uno "$SKETCH"
```

Install `Servo` alongside `Firmata`, even though the sketch's name doesn't
mention it: StandardFirmata's `.ino` includes `Servo.h` unconditionally and
the compile fails without it.

> On macOS the port is `/dev/cu.usbmodem*`, not `/dev/tty.usbmodem*` — a `tty`
> device blocks until it sees carrier detect and will appear to hang.

**Close the Arduino IDE afterwards.** Its serial monitor reopens the port
whenever the board appears, and it will fight pinctl for it.

## Step 2 — Install pinctl on the Pi

```sh
curl -fsSL https://raw.githubusercontent.com/sviniabanditka/pinctl/main/deploy/install.sh | sudo sh
```

The script detects the architecture, downloads the matching binary from the
[latest release](https://github.com/sviniabanditka/pinctl/releases), verifies
its checksum, then:

- installs `/usr/local/bin/pinctl` (runtime + web UI, one file),
- creates the `pinctl` system user,
- creates `/etc/pinctl/` and `/var/lib/pinctl/` (config and data),
- writes a starter `/etc/pinctl/config.yaml`, never overwriting an existing one,
- installs the systemd unit — but **does not start it**, so you can review the
  config first.

Re-running the script is how you upgrade.

Prefer to build it yourself:

```sh
git clone https://github.com/sviniabanditka/pinctl && cd pinctl
cd web && npm ci && npm run build && cd ..
go build -o pinctl ./cmd/pinctl
sudo ./deploy/install.sh ./pinctl
```

## Step 3 — Point the config at your board

Plug the Arduino into the Pi and find its port:

```sh
ls /dev/ttyACM* /dev/ttyUSB*
```

An Uno with a genuine USB chip is usually `/dev/ttyACM0`; clones with a CH340
chip come up as `/dev/ttyUSB0`. Then edit `serial.allowlist` in
`/etc/pinctl/config.yaml` so it lists that port:

```yaml
http:
  addr: ":8080"           # UI + API listen address

data_dir: /var/lib/pinctl

serial:
  allowlist:              # pinctl only ever opens ports listed here
    - /dev/ttyACM0
    - /dev/ttyUSB0
  baud: 57600             # StandardFirmata's default

log:
  level: info
```

The allowlist accepts glob patterns (`/dev/ttyACM*`). An empty list means
"any serial port" — fine on a dedicated Pi, worth thinking about elsewhere.
pinctl identifies each board by its USB vendor/product/serial number, so a
board keeps its name and its devices even if it comes back on a different
port number.

Serial access comes from the `dialout` group, which the systemd unit already
grants the service user. Nothing to do.

## Step 4 — Start it

```sh
sudo systemctl enable --now pinctl
systemctl status pinctl
journalctl -u pinctl -f          # watch it find the board
```

You should see the handshake within a couple of seconds of the service
starting:

```
pinctl: multi-board mode: serial allowlist [/dev/ttyACM0 /dev/ttyUSB0]
pinctl: listening on :8080
```

## Step 5 — Wire the example (optional)

```
LDR:  5V ──[ LDR ]──┬── A0
                    └──[ 10kΩ ]── GND

LED:  D13 ──[ 220Ω ]──▶|── GND         (or just use the built-in "L" LED)
```

The LDR and the resistor form a divider: as light falls, A0's reading falls
with it. Leave A0 unconnected and it will float — it reads plausible numbers
that drift slowly and respond to a fingertip, which is enough to test with.

## Step 6 — Build the pipeline in the browser

Open `http://<pi-address>:8080`. The board should be listed as **connected**,
with its port and firmware version.

1. **Add devices.** Press `+` → *Add device*. Create `LDR on A0` — pin `14`,
   role `analog-in`. Create `LED on D13` — pin `13`, role `digital-out`.
   Pin numbers are Firmata's single numbering: on an Uno, `D0`–`D13` are
   `0`–`13` and `A0`–`A5` are `14`–`19`. Their live values start streaming
   into the table immediately.
2. **Create a pipeline.** Press `+` → *Create pipeline*. The editor opens.
3. **Add nodes.** Press `/` (or the `+` in the corner) and pick:
   - `device-read` → select the LDR,
   - `expression` → write `a < 500`, one `float64` input named `a`, output `bool`,
   - `device-write` → select the LED.
4. **Wire them** left to right, dragging from port to port.
5. **Save**, then **Enable**.

Cover the sensor and the LED lights. The values on the nodes and edges are
live: you are watching the pipeline think.

To change the behavior, edit the expression — `a < 300`, or
`a < 500 && b` once you add a second input — and press Save. It takes effect
on the next tick. The Arduino is never touched.

## When something is wrong

| Symptom | Cause |
|---|---|
| `Serial port busy` in the log | Something else holds the port. Usually the Arduino IDE's serial monitor: `sudo lsof /dev/ttyACM0` names it. Close it. |
| Board never appears | The port isn't in `serial.allowlist`, or the cable is charge-only. `ls /dev/ttyACM*` and `journalctl -u pinctl` are the two things to check. |
| Handshake times out | The board isn't running StandardFirmata, or it's a clone whose port is `/dev/ttyUSB0` while the allowlist says `ttyACM0`. |
| Values freeze at one number | The board rebooted. pinctl detects this and reconfigures automatically (`rebooted, reconfiguring` in the log) — if you see frozen values *without* that line, the board is fine and the pin is simply floating. |
| Pipeline shows `error` | A node failed three ticks in a row — division by zero, or a board that went away. The editor names the node. |
| UI unreachable | `systemctl status pinctl`, then check `http.addr` and the Pi's firewall. |
| LED won't respond to a manual toggle | An enabled pipeline already drives that pin; pinctl returns a conflict rather than fighting it. Disable the pipeline first. |

More: [docs/14-deployment.md](./docs/14-deployment.md).

---

## Development

```sh
# frontend
cd web && npm ci && npm run dev     # dev server, proxies /api to :8422
npm run build                       # writes web/dist, embedded via web/embed.go
npm run test

# backend — build the web app first, embed.go needs web/dist to exist
go build -o pinctl ./cmd/pinctl
./pinctl -port /dev/ttyACM0         # single board, bypasses discovery entirely
./pinctl -config ./config.yaml -data ./devdata -listen :8080
go test ./... -race
```

| Flag | Purpose |
|---|---|
| `-config` | Path to `config.yaml` (default `/etc/pinctl/config.yaml`, then `~/.pinctl/config.yaml`; missing file = built-in defaults). |
| `-port` | Dev convenience: open exactly this serial port, bypassing discovery and `serial.allowlist`. |
| `-listen` | Override `http.addr`. |
| `-data` | Override `data_dir`. |

Without `-config`, pinctl reads and writes `~/.pinctl/` — nothing under `/etc`
or `/var` is touched.

## Docs

- [docs/00-design-brief.md](./docs/00-design-brief.md) — philosophy and scope
- [docs/02-requirements.md](./docs/02-requirements.md) — requirements
- [docs/03-architecture.md](./docs/03-architecture.md) — component boundaries, data flow
- [docs/05-serial-protocol.md](./docs/05-serial-protocol.md) — the Firmata subset, session lifecycle
- [docs/06-runtime.md](./docs/06-runtime.md) — process model, config schema, logging
- [docs/07-pipeline-engine.md](./docs/07-pipeline-engine.md) — tick model, validation, hot-reload
- [docs/08-node-catalog.md](./docs/08-node-catalog.md) — every node type
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
- Go runtime, Vue 3 + Vue Flow editor, embedded in one binary
