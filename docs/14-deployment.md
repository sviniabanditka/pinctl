# pinctl — Deployment

This document covers installing, configuring, running, upgrading, and
troubleshooting pinctl on a Raspberry Pi. It follows
[00-design-brief.md](./00-design-brief.md) and
[06-runtime.md](./06-runtime.md) (process model, config schema, logging);
if anything here appears to contradict those, they win.

## Table of contents

- [Supported targets](#supported-targets)
- [Install](#install)
- [Configuration](#configuration)
- [systemd unit](#systemd-unit)
- [udev and serial access](#udev-and-serial-access)
- [Upgrade](#upgrade)
- [Backup](#backup)
- [Logs](#logs)
- [Troubleshooting](#troubleshooting)
- [Flashing StandardFirmata](#flashing-standardfirmata)

## Supported targets

| Target | Status |
|---|---|
| Raspberry Pi OS (arm64, 64-bit) | Primary supported target (Pi 4, Pi 5, Zero 2 W in 64-bit mode). |
| Raspberry Pi OS (armhf/armv7, 32-bit) | Primary supported target (Pi 2/3/4 in 32-bit mode). The released `armv7` binary is built with `GOARM=7`. |
| Raspberry Pi OS on ARMv6 (Pi Zero, Zero W, Pi 1) | No release binary — the `armv7` asset faults on an ARMv6 core. Build with `GOARM=6` and install it locally (`sudo ./install.sh ./pinctl`). |
| Generic linux/amd64, linux/arm64 | Builds and runs (useful for a Linux dev box or a non-Pi SBC); not a reference platform for the performance requirements in [02-requirements.md](./02-requirements.md). |
| macOS (darwin/arm64) | Development only, per NFR-PORT-2 — not a deployment target. |

Only Raspberry Pi OS is exercised by the [manual HIL checklist](./13-testing.md#hardware-in-the-loop-manual-not-ci) before a release; other Linux distributions on arm64/armhf are expected to work (same kernel serial driver model) but are untested.

## Install

pinctl ships as a **single static binary** with the web UI embedded — there
is nothing else to install (no runtime, no separate web server, no
database), per the brief's "one artifact" principle.

### Install script

```sh
curl -fsSL https://raw.githubusercontent.com/sviniabanditka/pinctl/main/deploy/install.sh | sudo sh
```

The script:

1. Detects OS/arch (`linux/arm64`, `linux/armv7` (armhf), or `linux/amd64`
   for a non-Pi Linux box), refuses to run on anything else.
2. Downloads the matching release binary (`pinctl-linux-<arch>`) and the
   published `checksums.txt` from the GitHub release (see the `release` CI
   job in [13-testing.md](./13-testing.md#ci)).
3. Verifies the checksum before installing anything — **never pipe the
   download step to `sh` without the checksum verification staying intact**
   if you're adapting the script; a corrupted or tampered binary should
   fail loudly, not install silently.
4. Installs to `/usr/local/bin/pinctl` (`0755`, owned by `root`).
5. Creates `/etc/pinctl/` and `/var/lib/pinctl/` if missing, with a
   `pinctl` system user/group owning `/var/lib/pinctl/`.
6. Drops a starter `/etc/pinctl/config.yaml` if one doesn't already exist
   (never overwrites an existing config).
7. Installs the [systemd unit](#systemd-unit) but does **not** enable or
   start it automatically — the operator reviews `config.yaml` (in
   particular the serial allowlist) first, then runs
   `sudo systemctl enable --now pinctl`.

### Manual install (equivalent, for anyone who doesn't want to curl|sh)

```sh
# download+verify the release asset for your arch, then:
sudo install -o root -g root -m 0755 pinctl /usr/local/bin/pinctl
sudo useradd --system --no-create-home --groups dialout pinctl
sudo mkdir -p /etc/pinctl /var/lib/pinctl
sudo chown pinctl:pinctl /var/lib/pinctl
sudo cp config.yaml /etc/pinctl/config.yaml
sudo cp pinctl.service /etc/systemd/system/pinctl.service
sudo systemctl daemon-reload
```

### Directory layout

| Path | Purpose | Owner |
|---|---|---|
| `/usr/local/bin/pinctl` | The binary. | `root:root` |
| `/etc/pinctl/config.yaml` | Configuration (see below). | `root:root`, world-readable |
| `/var/lib/pinctl/` | Device/pipeline JSON definitions (data dir). | `pinctl:pinctl` |
| `/etc/systemd/system/pinctl.service` | systemd unit. | `root:root` |

Dev mode (running `pinctl` directly, not under systemd) uses
`~/.pinctl/` as the data dir and `~/.pinctl/config.yaml` as config, per
[06-runtime.md](./06-runtime.md#config-schema) — nothing under `/etc` or
`/var` is touched in dev mode.

## Configuration

Full annotated `config.yaml`, matching the schema fixed in
[06-runtime.md](./06-runtime.md#config-schema) (unknown fields are a hard
load error — strict decoding, so a typo here fails fast instead of being
silently ignored):

```yaml
# /etc/pinctl/config.yaml

http:
  # Listen address for REST + WebSocket + the embedded web UI.
  # Bind to a specific LAN interface (e.g. "192.168.1.10:8080") instead of
  # ":8080" if the Pi has more than one network interface and pinctl should
  # only be reachable on one of them. There is no auth in the MVP — treat
  # this address as the trust boundary (see brief: "must not be exposed
  # beyond a trusted LAN").
  addr: ":8080"

# Root directory for device/pipeline JSON files (FR-PERSIST-1).
# Must be writable by the user pinctl runs as (see systemd unit below).
data_dir: /var/lib/pinctl

serial:
  # Serial device paths pinctl is permitted to open (FR-BOARD-4).
  # Empty list ([]) means "allow any" — fine for a single-user dev box,
  # NOT recommended once other things on the Pi might also open serial
  # devices, since pinctl would then compete for/steal a port meant for
  # something else.
  allowlist:
    - /dev/ttyACM0
    - /dev/ttyUSB0
    # See "udev" below for a stable alternative to /dev/ttyACM0 that
    # survives replugging or having more than one board attached:
    # - /dev/pinctl-board0

  # Baud rate for Firmata sessions. Fixed at StandardFirmata's default
  # (57600) per the design brief; only change this if you're running
  # firmware that was itself configured for a non-default baud rate.
  baud: 57600

# Explicitly configured Firmata-over-TCP boards (StandardFirmataWiFi) — see
# 17-network-boards.md for the full picture (addresses, identity, the three
# liveness mechanisms, firmware). Empty/absent means no network boards, no
# dialling, no goroutine; serial discovery above is unaffected either way.
network:
  boards:
    - tcp://esp32-porch.local:3030
  dial_timeout: 5s
  read_timeout: 10s # a board silent this long is treated as gone
  keepalive: 15s

  # Security: Firmata over TCP is unauthenticated and unencrypted — anyone
  # who can reach the port can drive every pin. Put network boards on a
  # VLAN/IoT SSID that can't reach the internet or be reached by untrusted
  # devices, and firewall the port to only the Pi's address; there is no
  # pinctl setting that changes this. See 17-network-boards.md#security.

log:
  # debug | info | warn | error. debug is per-tick node evaluation detail
  # (see 06-runtime.md) — very high volume, only turn on while actively
  # debugging a specific pipeline.
  level: info

  # json (production/journald-friendly) or text (readable in a terminal
  # during local development).
  format: json

# Optional MQTT bridge and Home Assistant discovery — see
# 15-mqtt-bridge.md for the full picture (topics, payloads, the
# pipeline-conflict rule, the deadband/rate publishing policy). Off by
# default: with enabled: false (or the section omitted entirely) no MQTT
# client is constructed and no goroutine runs — "disabled means absent".
mqtt:
  enabled: false
  broker: tcp://192.168.1.10:1883 # tcp://, tls://, ws://, wss://
  client_id: pinctl               # must be unique on the broker
  username: pinctl
  # password and password_file are mutually exclusive; see below.
  password_file: /etc/pinctl/mqtt-password
  base_topic: pinctl
  discovery_prefix: homeassistant # HA's own default; "" disables discovery
  analog_deadband: 4              # raw ADC counts
  min_publish_interval: 1s
  max_publish_interval: 60s
```

### MQTT password

`mqtt.password` (inline, fine for a throwaway setup), `mqtt.password_file`,
and the `MQTT_PASSWORD` environment variable are three ways to supply the
broker password — checked in that order, and `password`/`password_file` are
mutually exclusive. Exactly one is required if the broker needs auth; none
is required for an anonymous-access broker.

`password_file` is what a real install should use:

- Owned by the `pinctl` service user, mode `0600` — `pinctl` refuses to
  start with `mqtt.enabled: true` if the file is group- or other-readable.
  The installer does **not** create this file for you (there is no
  sensible default password to write); create it yourself:
  ```sh
  echo -n 'the-broker-password' | sudo tee /etc/pinctl/mqtt-password >/dev/null
  sudo chown pinctl:pinctl /etc/pinctl/mqtt-password
  sudo chmod 0600 /etc/pinctl/mqtt-password
  ```
- Read once at startup; a password rotation needs a service restart, same
  as any other config change (`FR-DEPLOY-5`).

`MQTT_PASSWORD` is the alternative for a secrets-manager/environment-based
deployment instead of a file on disk. The systemd unit reads it from
`/etc/pinctl/pinctl.env` via `EnvironmentFile=-/etc/pinctl/pinctl.env` (the
leading `-` makes the file optional, so a deployment that isn't using this
path needs nothing there at all):
```sh
# /etc/pinctl/pinctl.env — mode 0600, owned by root, not committed anywhere
MQTT_PASSWORD=the-broker-password
```

The password is never logged, never echoed in an error, and never leaves
the process (docs/15-mqtt-bridge.md's `config.Secret` redacts it in every
`%v`/`%+v`/`%#v` print).

### Making it show up in Home Assistant

With `mqtt.enabled: true` and a `discovery_prefix` matching HA's own MQTT
integration setting (`homeassistant` is both pinctl's and HA's default),
every pinctl device announces itself the moment the bridge connects — no
YAML, no manual entity setup on the HA side. Point the HA MQTT integration
at the same broker, restart pinctl (or just wait for its next reconnect),
and each device shows up grouped under one HA device per pinctl board (the
`device` block in the discovery payload, see 15-mqtt-bridge.md). If an
entity doesn't appear: check `journalctl -u pinctl | grep mqtt` for a
connection or auth error first — a bad broker URL or password is by far the
most common cause, and pinctl logs it rather than failing silently.

## systemd unit

`/etc/systemd/system/pinctl.service` (installed by the install script;
shown here in full, matching [06-runtime.md](./06-runtime.md) with
deployment-oriented hardening added):

```ini
[Unit]
Description=pinctl runtime
Documentation=https://github.com/sviniabanditka/pinctl
After=network.target
Wants=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/pinctl -config /etc/pinctl/config.yaml
Restart=on-failure
RestartSec=2

# Optional: MQTT_PASSWORD (see "MQTT password" above) instead of
# mqtt.password_file. The leading "-" makes the file optional — a
# deployment not using this path needs nothing at /etc/pinctl/pinctl.env
# and systemd silently skips it.
EnvironmentFile=-/etc/pinctl/pinctl.env

# Identity: unprivileged user, dialout membership is what actually grants
# serial device access (see udev section below) — there is no reason for
# pinctl to run as root.
User=pinctl
Group=pinctl
SupplementaryGroups=dialout

# Hardening (safe defaults for a single-purpose service; loosen only if
# something below breaks a legitimate need):
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/var/lib/pinctl
ProtectHome=true
AmbientCapabilities=
CapabilityBoundingSet=

[Install]
WantedBy=multi-user.target
```

Notes:

- `ProtectSystem=strict` makes the whole filesystem read-only to the
  service except `ReadWritePaths` — `/var/lib/pinctl` is the only
  directory pinctl ever needs to write to (config is read-only at
  runtime, per FR-DEPLOY-5: config changes need a restart, not a live
  write-back).
- `Restart=on-failure` is a safety net, not a crutch — per the runtime's
  [error philosophy](./06-runtime.md#error-philosophy), user-config
  mistakes are designed to never crash the process; this restart exists
  for genuine process-level faults.
- Enable and start: `sudo systemctl enable --now pinctl`.
- Check status: `sudo systemctl status pinctl`.

## udev and serial access

### Group-based access (default, sufficient for one board)

The `pinctl` service user must be in the `dialout` group (already set via
`SupplementaryGroups=dialout` above) to open `/dev/ttyACM*`/`/dev/ttyUSB*`
without running as root — this is standard Raspberry Pi OS behavior, no
custom udev rule required for a single board.

### Pinning a board to a stable device path (optional, recommended for multiple boards)

Linux assigns `/dev/ttyACM0`, `/dev/ttyACM1`, etc. by enumeration order,
which can change across reboots or replugs — especially with more than one
board attached. A udev rule pinning each board to a fixed symlink by its
USB serial number avoids that, and lets `serial.allowlist` reference a
name that won't silently start pointing at the wrong board:

```sh
# Find the board's USB serial number:
udevadm info -a -n /dev/ttyACM0 | grep -m1 '{serial}'
# ATTRS{serial}=="85033313938351E0A162"
```

`/etc/udev/rules.d/99-pinctl.rules`:

```
SUBSYSTEM=="tty", ATTRS{idVendor}=="2341", ATTRS{serial}=="85033313938351E0A162", SYMLINK+="pinctl-board0"
```

(`idVendor=="2341"` is Arduino's USB vendor ID; adjust for clones/other
vendors via `udevadm info` above.) Reload rules with
`sudo udevadm control --reload-rules && sudo udevadm trigger`, then
reference `/dev/pinctl-board0` in `serial.allowlist` instead of
`/dev/ttyACM0`.

## Upgrade

pinctl's "one artifact" design makes upgrades a binary swap:

1. Download the new release binary (or re-run the install script, which is
   idempotent for the binary step).
2. `sudo systemctl stop pinctl`
3. Replace `/usr/local/bin/pinctl`.
4. `sudo systemctl start pinctl`

Data files under `/var/lib/pinctl/` are forward-compatible by design: every
persisted definition carries a `schemaVersion`
(see [06-runtime.md](./06-runtime.md#versioning-and-compatibility-for-pipeline-json)),
and a newer runtime migrates older definitions in place on load. There is
no separate "migration step" to run — it happens automatically the first
time the new binary starts. Downgrading to an older binary after data has
been migrated forward is not supported (migrations are forward-only); keep
a [backup](#backup) before a major-version upgrade if you might need to
roll back.

## Backup

Everything that defines pinctl's state lives in two directories:

```sh
sudo tar czf pinctl-backup-$(date +%F).tar.gz \
    /var/lib/pinctl \
    /etc/pinctl
```

Restoring is the reverse: stop the service, extract over the same paths,
restore ownership (`chown -R pinctl:pinctl /var/lib/pinctl`), start the
service. Because devices and pipelines are plain JSON files (ADR-3 in
[03-architecture.md](./03-architecture.md)), the backup is human-readable
and diffable — `git init` on `/var/lib/pinctl` is a reasonable poor-man's
version history if you want one, though pinctl itself doesn't manage that.

**One caveat: `history.db`.** The value history
([16-history.md](./16-history.md)) is a SQLite database in WAL mode, so it
has `.db`, `.db-wal` and `.db-shm` files whose contents are only consistent
with each other at an instant. `tar` on a running service can capture a torn
copy. Either stop the service first, or back the database up separately while
it runs:

```sh
sqlite3 /var/lib/pinctl/history.db ".backup '/tmp/history-backup.db'"
```

The JSON files have no such problem — they are written atomically (temp file
+ rename), so a `tar` of a running instance always sees a complete one. And
losing `history.db` entirely costs only the past: pinctl starts with every
device and pipeline intact.

## Logs

pinctl logs to stdout/stderr under systemd, which journald captures
automatically — no separate log file to manage in the systemd deployment
mode.

```sh
# Follow live logs:
journalctl -u pinctl -f

# Since last boot:
journalctl -u pinctl -b

# Only warnings and above:
journalctl -u pinctl -p warning

# JSON-formatted lines (default log.format: json) piped through jq:
journalctl -u pinctl -o cat | jq .
```

Log levels (set via `log.level`, see [Configuration](#configuration) and
[06-runtime.md](./06-runtime.md#logging-strategy)):

| Level | When to use it |
|---|---|
| `debug` | Actively debugging one pipeline's tick-by-tick behavior. High volume — don't leave it on. |
| `info` (default) | Normal operation: board connect/disconnect, pipeline enabled/disabled, server started. |
| `warn` | Investigating a pipeline that seems paused or a board that keeps dropping — surfaces tick overruns and definitions skipped at load. |
| `error` | Quiet running, only want to be told about actual faults. |

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Board not detected / stays `disconnected` | Permissions: `pinctl` user not in `dialout`; or the port isn't in `serial.allowlist`; or a bad/loose USB cable. | Check `journalctl -u pinctl \| grep board` for the specific error. Verify `groups pinctl` includes `dialout`. Verify the exact path in `serial.allowlist` matches `ls /dev/tty{ACM,USB}*` (or the udev symlink). Try a different cable — many USB cables are charge-only (no data lines). |
| Uno resets when pinctl connects | **Expected behavior**, not a bug: opening a serial port with DTR asserted triggers the Arduino Uno/Nano's auto-reset (same as the Arduino IDE's serial monitor). The board reboots, StandardFirmata restarts, and pinctl's handshake accounts for this delay. | No fix needed. If the reset delay (roughly 1-2s) is disruptive for a specific setup, a 10µF capacitor between RESET and GND on the Uno is the standard hardware workaround (outside pinctl's scope) — see [04-firmware-agent.md](./04-firmware-agent.md). |
| Pipeline "enabled" but not running / no writes happening | Its board is offline (check board status via the API/UI — a paused pipeline due to an offline board is normal, not an error); or the pipeline has a validation error from a stale/deleted device reference. | Check board connectivity first. Check `journalctl -u pinctl -p warning` for "pipeline paused" or "definition skipped" lines. Re-save the pipeline in the editor to surface any inline validation errors. |
| UI unreachable in browser | Wrong `http.addr` (bound to a specific interface, not `:8080`); firewall blocking the port; service not actually running. | `sudo systemctl status pinctl` to confirm it's active. `curl -v http://localhost:8080/` from the Pi itself to isolate network vs. process issues. Check `http.addr` in `config.yaml` matches how you're reaching the Pi (hostname/IP). |
| High tick-overrun warnings in logs | Too many/too heavy pipelines for the hardware (see NFR-PERF-3/4 budgets per Pi model); or a board's serial link is intermittently stalling and blocking writes past their ack timeout. | Check how many pipelines/nodes are enabled against the budget for your Pi model in [02-requirements.md](./02-requirements.md#performance-nfr-perf-). Check for a specific slow/flaky board correlating with the overruns in the logs. Consider raising the affected pipeline's tick period (10-1000ms range, NFR-PERF-1) if sub-20ms responsiveness isn't actually needed for it. |

## Flashing StandardFirmata

The **only** firmware step in pinctl's entire lifecycle — done once per
board, before it's ever plugged into a Pi running pinctl. There is no OTA
flashing, no re-flash-on-update, no firmware version pinctl needs to track
beyond "does it speak StandardFirmata" (per the brief's "generic,
never-reflashed agent" decision).

Using [`arduino-cli`](https://arduino.github.io/arduino-cli/) (any other
method that flashes the stock `StandardFirmata` example sketch — Arduino
IDE, PlatformIO — works identically):

```sh
arduino-cli core install arduino:avr
arduino-cli lib install Firmata Servo
arduino-cli compile --fqbn arduino:avr:uno \
    "$(arduino-cli lib list --format json | jq -r '.[] | select(.library.name=="Firmata") | .library.install_dir')/examples/StandardFirmata"
arduino-cli upload -p /dev/ttyACM0 --fqbn arduino:avr:uno \
    "$(arduino-cli lib list --format json | jq -r '.[] | select(.library.name=="Firmata") | .library.install_dir')/examples/StandardFirmata"
```

The `Servo` library is required alongside `Firmata`, even though the sketch
is named `StandardFirmata` — its `.ino` includes `Servo.h` unconditionally,
so `arduino-cli compile`/the Arduino IDE's verify step fails without it
installed, regardless of whether the target board actually has a servo
attached.

Or, more simply, if you already have the Arduino IDE installed: open
**File → Examples → Firmata → StandardFirmata**, select the correct board
and port, and click Upload. After this, the board never needs to be
touched with a programmer again — all behavior changes happen through
pinctl's pipelines, per the project's core philosophy.
