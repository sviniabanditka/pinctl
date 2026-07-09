# pinctl — Serial Protocol

This document specifies how `internal/board` talks to a board over the wire: the
Firmata subset pinctl actually uses, the transport, the session lifecycle from
port discovery through reconnect, how a board's identity is tracked across
replugs, and error handling. It follows the fixed decisions in
[00-design-brief.md](00-design-brief.md) (StandardFirmata, 57600 baud,
`go.bug.st/serial`) and the agent-side contract described in
[04-firmware-agent.md](04-firmware-agent.md). Where `internal/board` sits in
the overall system, and how `Session` is consumed by the engine, is covered in
[03-architecture.md](03-architecture.md).

## Contents

- [Why Firmata (recap)](#why-firmata-recap)
- [Firmata subset used](#firmata-subset-used)
- [Transport](#transport)
- [Session lifecycle](#session-lifecycle)
- [Board identity](#board-identity)
- [Analog reporting](#analog-reporting)
- [Digital reporting](#digital-reporting)
- [Error handling](#error-handling)
- [Message sequence diagrams](#message-sequence-diagrams)
- [Limits of Firmata and the v2 path](#limits-of-firmata-and-the-v2-path)

## Why Firmata (recap)

Per the brief (ADR-2 in [03-architecture.md](03-architecture.md)): the MCU runs
stock, never-reflashed **StandardFirmata**, and `internal/board` speaks
Firmata's wire protocol directly — no vendored/forked C++ sketch, no custom
framing. This buys zero firmware development, a mature protocol with existing
Go clients to reference, and a board that "just works" once flashed once (see
[04-firmware-agent.md](04-firmware-agent.md#flashing-instructions-the-only-firmware-step)).
The cost — a protocol not designed for pinctl's specific needs — is accepted
for MVP and its concrete edges are catalogued in
[Limits of Firmata and the v2 path](#limits-of-firmata-and-the-v2-path).

## Firmata subset used

Firmata is versioned independently of any given sketch; StandardFirmata as
shipped with the Arduino `Firmata` library implements **protocol version
2.5.x** (major.minor reported by the board itself at handshake — see
[Session lifecycle](#session-lifecycle)). pinctl targets 2.5 and treats a
lower reported major version as an unsupported board (see
[Error handling](#error-handling)).

Firmata defines far more message types than pinctl needs (e.g. I2C, one-wire,
servo, string data, scheduler sysex — mostly v2 device roles, out of scope per
the brief's domain model). The subset `internal/board` implements:

| Message | Direction | Wire form | Purpose |
|---|---|---|---|
| Protocol Version Query / Report | agent → runtime (auto on boot) | `0xF9 <major> <minor>` | First bytes the board sends after reset; used to detect "board is alive and speaking Firmata" |
| Report Firmware (sysex) | runtime ↔ agent | `0xF0 0x79 ... 0xF7` | Firmware name + version string; logged for diagnostics, not required for operation |
| Capability Query / Response (sysex) | runtime → agent → runtime | `0xF0 0x6B ... 0xF7` / `0xF0 0x6C ... 0xF7` | Enumerates, per pin, which modes it supports (input, output, analog, PWM, etc.) — drives `PinCapabilities` |
| Analog Mapping Query / Response (sysex) | runtime → agent → runtime | `0xF0 0x69 ... 0xF7` / `0xF0 0x6A ... 0xF7` | Maps Firmata pin numbers to analog channel numbers (`A0` → channel 0, etc.) |
| Set Pin Mode | runtime → agent | `0xF4 <pin> <mode>` | Switch a pin between `INPUT`/`OUTPUT`/`ANALOG`/`PWM` per the device role bound to it |
| Set Digital Pin Value | runtime → agent | `0xF5 <pin> <0/1>` | Single-pin digital write (used for pins outside a full port write) |
| Digital I/O Message | runtime ↔ agent | `0x90+port <lsb> <msb>` | 8-pin port value, both directions: runtime writes a port's outputs, agent reports a port's inputs when reporting is enabled |
| Digital Port Reporting | runtime → agent | `0xD0+port <0/1>` | Enable/disable unprompted digital reports for a given 8-pin port |
| Analog I/O Message | agent → runtime | `0xE0+channel <lsb> <msb>` | Agent's unprompted analog value report for a channel |
| Analog Reporting | runtime → agent | `0xC0+channel <0/1>` | Enable/disable unprompted analog reports for a channel |
| PWM write | runtime → agent | Analog I/O Message form (`0xE0+channel`) is *not* used for PWM; PWM uses `0xF5`-style Extended Analog (`0xF0 0x6F <pin> <value...> 0xF7`) for pins/values beyond the 7-bit basic write | Set duty cycle on a PWM-capable pin |
| Sampling Interval (sysex) | runtime → agent | `0xF0 0x7A <lsb> <msb> 0xF7` | Sets the interval (ms) between unprompted analog/digital reports |
| Sysex start/end | framing | `0xF0 ... 0xF7` | Wraps all sysex message bodies above |

Not used by pinctl in MVP: I2C request/reply, OneWire, Servo config, String
data, Scheduler sysex, Encoder — none map to a v0.x device role
(`digital-in`/`digital-out`/`analog-in`/`pwm-out`).

## Transport

| Property | Value |
|---|---|
| Physical | USB CDC serial (the Uno/Nano's onboard USB-serial bridge) |
| Baud rate | 57600 (StandardFirmata's default; not renegotiated) |
| Framing | 8 data bits, no parity, 1 stop bit (8N1) — Firmata's standard serial framing |
| Go library | [`go.bug.st/serial`](https://pkg.go.dev/go.bug.st/serial) |
| Port ownership | Exactly one goroutine per open port (see [03-architecture.md](03-architecture.md#concurrency-model)); all reads/writes go through that goroutine's channels, never called directly from other goroutines |

`go.bug.st/serial` is used purely as a byte pipe — opening the port, setting
baud/framing, and blocking reads/writes. The Firmata message framing itself
(distinguishing multi-byte messages, buffering partial reads, sysex
start/end) is `internal/board`'s own state machine layered on top; no
higher-level Firmata client library is assumed.

## Session lifecycle

A `Session` (see [03-architecture.md](03-architecture.md#internalboard))
moves through the following states: `discovering → connecting → handshaking →
syncing → ready → offline → reconnecting → ready | permanently-failed`.

### 1. Port discovery

`board.Manager` is constructed with the **serial allowlist** from
`/etc/pinctl/config.yaml` (per the brief: config carries "port, data dir,
serial allowlist"). The allowlist is a list of concrete port paths (e.g.
`/dev/ttyACM0`, `/dev/ttyUSB0`) — pinctl does **not** probe every serial
device on the system and guess which ones are Arduinos; it only ever opens
ports the operator has explicitly listed. This keeps discovery deterministic
and avoids accidentally opening an unrelated serial device (e.g. a GPS
module, a Pi's own console UART) that happens to also be a `/dev/tty*` node.

### 2. Open

For each allowlisted port, the session goroutine opens it via
`go.bug.st/serial` at 57600/8N1. Opening the port on most Uno/Nano boards
**resets the MCU** (see [Error handling](#error-handling) — DTR auto-reset);
the session accounts for this before attempting the handshake.

### 3. Handshake

- Wait (bounded timeout, see table below) for the board to spontaneously emit
  its **Protocol Version** report (`0xF9 <major> <minor>`) — StandardFirmata
  sends this once on boot, unprompted.
- Send **Report Firmware** query; read the sysex reply (firmware name +
  version) for logging/diagnostics.
- If no Protocol Version report arrives within the timeout, or the major
  version is unsupported, the session fails to `permanently-failed` for this
  connect attempt and the reconnect loop applies backoff as usual (a
  misflashed or non-Firmata device on an allowlisted port shouldn't be
  retried at full speed forever).

### 4. Capability query

- Send **Capability Query** sysex; parse the response into per-pin supported
  modes.
- Send **Analog Mapping Query** sysex; parse the response into
  Firmata-pin-number → analog-channel-number.
- Combine both into the `PinCapabilities` exposed via `Session.Capabilities()`.
  This is what `internal/api` uses to validate a device binding (e.g.
  refusing to bind a `pwm-out` device to a pin that Capability Query didn't
  report PWM support for).

### 5. Pin state sync

- Send **Set Pin Mode** for every pin currently bound to a device (from
  `store.Store` device definitions the board manager was told about at
  startup, or as devices are added later via the API).
- Send **Sampling Interval** (see [Analog reporting](#analog-reporting)).
- Enable **Analog Reporting** for each analog pin bound to an `analog-in`
  device, and **Digital Port Reporting** for each port containing a pin bound
  to a `digital-in` device.
- Perform one initial digital port read implicitly via the first reporting
  message that arrives (Firmata has no explicit "read now" for digital pins
  outside of reporting).

### 6. Steady state (`ready`)

- The session's read loop continuously parses incoming bytes into Firmata
  messages and updates the cached `PinState` map (digital port + analog
  channel values) as Analog/Digital I/O messages arrive.
- The session's write path accepts queued Set Digital Pin Value / Digital I/O
  / Extended Analog (PWM) writes from `Session.DigitalWrite`/`PWMWrite` and
  serializes them onto the port.
- A lightweight health check runs alongside (see
  [Error handling](#error-handling)).

### 7. Disconnect detection

Any of the following moves the session to `offline`:

- A read or write on the port returns an OS-level error (device removed,
  `EIO`, broken pipe — `go.bug.st/serial` surfaces this on unplug).
- The health-check watchdog doesn't see *any* traffic (a report or a
  successful write) within its window.

### 8. Reconnect with backoff

- `board.Manager` retries opening the same port on an exponential backoff
  (500 ms → 1 s → 2 s → 4 s → ... capped at e.g. 30 s), only ever against
  ports in the allowlist.
- A successful reconnect replays the **entire handshake** (steps 2–5) from
  scratch — Firmata is stateless across a serial disconnect (the MCU very
  likely reset anyway, per DTR auto-reset), so there is no "resume" concept;
  every reconnect is a fresh session that happens to reuse the same
  `BoardID`/port.
- Backoff resets to the initial value on a successful reconnect.
- See [Reconnect](#4-reconnect) in the sequence diagrams below.

## Board identity

A board's persisted identity must remain stable across a simple unplug/replug
(same physical Arduino) so that device bindings and pipelines that reference
it don't silently break. Per [09-data-model.md](09-data-model.md#board-object),
a persisted `Board` has two identity-shaped fields, and this section fixes
where each one comes from:

- **`id`** (`brd_…`) — a random ID generated once at first discovery, per
  [09-data-model.md](09-data-model.md#id-strategy). Immutable, and what every
  cross-reference (`Device.boardId`, etc.) actually uses.
- **`identity.key`** — the *source of truth* `internal/board` uses to
  re-associate an already-known `Board` entry with a physical device on
  every (re)connect, independent of which `/dev/ttyACM*` path it happens to
  enumerate as. `identity.kind` is `"usb-serial"` for MVP and `identity.key`
  is a USB VID/PID + serial-number composite, e.g.
  `"VID_2341:PID_0043:SN_85033313938351E082D1"`.
- **`portHint`** — the last-known serial device path (e.g.
  `"/dev/ttyACM0"`). A diagnostic/tie-breaker only, never the identity key —
  see [09-data-model.md](09-data-model.md#board-object).

Three identity strategies were considered for what `identity.key` should be
derived from:

| Strategy | How | Stable across replug? | Stable across *port* change? | Notes |
|---|---|---|---|---|
| Serial port path (`/dev/ttyACM0`) | Use the path directly as the key | Yes, if the OS reassigns the same path (common on a Pi with one board) | **No** — plugging into a different USB port, or plugging a second board first, can shift `ttyACM0` → `ttyACM1` | Simplest; zero extra tooling; brittle with 2+ boards |
| USB VID/PID + serial number (via `udev`/sysfs) | Read `ID_VENDOR_ID`/`ID_MODEL_ID`/`ID_SERIAL_SHORT` from `udevadm info` (or walk `/sys/class/tty/<dev>/device/../serial`) at discovery time | Yes | Yes | Genuine Uno/Nano boards using the FTDI/CH340/ATmega16U2 USB bridge often expose a per-device serial number; some clone boards (cheap CH340) share **one fixed serial number across all units**, collapsing identity |
| udev-authored persistent symlink (e.g. `/dev/pinctl/board-<name>`) | Operator installs a udev rule mapping VID/PID(+serial) to a stable symlink name, pinctl's allowlist references the symlink | Yes | Yes | Most robust; requires one-time operator setup (a udev rules file), documented in [14-deployment.md](14-deployment.md) |

**Recommendation (matches [09-data-model.md](09-data-model.md#board-object)):
use USB VID/PID + serial number as `identity.key`**, resolved via sysfs
(`/sys/class/tty/<dev>/device/../serial` and friends — no shelling out to
`udevadm` required, since sysfs is directly readable) at connect time. The
serial port path is recorded only as `portHint`: useful for the UI and as a
first-match tie-breaker on a fresh scan, but never the field
`internal/board` re-associates a board by. On every (re)connect (including
after a replug that reorders `ttyACM*` devices), the session resolves
VID/PID/serial for whatever's on each allowlisted port and matches it
against known `identity.key`s in `boards.json`; a match updates `portHint`
and reuses the existing `id`, a non-match creates a new `Board` entry.

Rationale: the brief's "one serial port each" describes the physical
topology (multiple boards, one port apiece), not a mandate that the port
path itself be the identity key — and a port path fails the basic "survives
a replug into a different USB port, or a second board being plugged in
first" case that a "stable across reconnects/replugs" identity strategy
needs to hold up to, which is exactly the scenario 2+ boards on a Pi hits in
practice, not just in an edge case worth deferring. VID/PID+serial resolution
is directly readable from sysfs with no extra dependency and no operator
setup, so there's no meaningful cost to doing it from day one; the
udev-symlink approach remains documented in
[14-deployment.md](14-deployment.md) as a further upgrade for the small
minority of clone boards whose USB bridge reports a shared/non-unique serial
number (the one case VID/PID+serial can't disambiguate on its own), not
because hardware-key identity itself is deferred to v2 — same gating
philosophy as the [v2 custom protocol](#limits-of-firmata-and-the-v2-path),
now applied to that narrower fallback case instead of to hardware-key
identity as a whole.

## Analog reporting

- **Enabling:** for each Firmata pin bound to an `analog-in` device, the
  session sends `0xF4 <pin> 0x02` (Set Pin Mode → `ANALOG`) followed by
  `0xC0+channel 0x01` (Analog Reporting → on), using the channel number
  resolved from the Analog Mapping Query response, not the raw pin number.
- **Sampling interval:** a single global **Sampling Interval** sysex applies
  to *all* reporting (analog and digital) on the board — Firmata has no
  per-pin interval. pinctl sets this once per session, at handshake time, to
  a value derived from config (default: match the pipeline engine's default
  tick, 20 ms — see [Firmata limitations](#limits-of-firmata-and-the-v2-path)
  for the throughput math behind that choice).
- **Value flow:** each `0xE0+channel <lsb> <msb>` message the read loop
  parses updates the cached value for that channel in the session's
  `PinState` map (a 14-bit raw value, 0–16383 per Firmata's format, though
  AVR ADCs report 0–1023 — the extra bits are always zero on Uno/Nano). The
  engine's device-read node picks this cached value up on its next tick via
  `Session.PinState(pin)` — see
  [03-architecture.md, Tick execution](03-architecture.md#b-tick-execution).
  There is no push path from the serial read loop directly into the engine;
  the read loop only ever updates the cache, decoupling report arrival time
  from tick evaluation time (this is also why Firmata's lack of board-side
  timestamps, noted in
  [04-firmware-agent.md](04-firmware-agent.md#firmata-limitations-relevant-to-pinctl),
  doesn't need a runtime workaround: the cache-then-tick model already treats
  "when did the runtime last see this value" as good enough).

## Digital reporting

- Firmata reports digital input **per 8-pin port**, not per pin: enabling
  port 0's reporting (`0xD0 0x01`) makes the agent stream a `0x90 <lsb>
  <msb>` message for all 8 pins on that port whenever *any* pin in it
  changes, not just the one pinctl cares about.
- The session enables reporting only for ports that contain at least one pin
  bound to a `digital-in` device, and on receipt of a port message, updates
  the cached value for every pin in that port (bound or not — cheap to keep,
  simpler than filtering at parse time) and lets `Session.PinState` filter at
  read time.
- **Debounce is explicitly not handled here.** Per the domain model
  ([00-design-brief.md](00-design-brief.md)), `debounce` is a pipeline
  **node** (a transform), evaluated by the engine on the tick-sampled cached
  value — the same way a physical switch's chatter would be filtered in any
  tick-based system. `internal/board` reports every raw port change it
  receives; it does not suppress, delay, or rate-limit digital reports
  itself. This keeps the protocol layer a dumb value cache and keeps
  debounce policy (window, edge semantics) user-configurable per pipeline
  rather than baked into board firmware or Go code.

## Error handling

| Condition | Detection | Handling |
|---|---|---|
| Handshake timeout (no Protocol Version report) | No `0xF9` within ~3 s of port open (see [boot delay](#error-handling) below — timeout must exceed it) | Fail this connect attempt; `board.Manager` applies reconnect backoff |
| Unsupported protocol version | Parsed `0xF9` major `<` 2 | Fail permanently for this port (not retried on backoff — a config/hardware mismatch, not a transient fault) until the operator intervenes (e.g. re-flash) |
| Malformed / unrecognized message | A status byte that doesn't match any known Firmata message, or a sysex block missing its `0xF7` terminator | Log at debug level, discard bytes up to the next plausible status byte (top bit set), and continue — Firmata gives no way to know a frame boundary was missed other than resyncing on the next byte that looks like a status byte; this is the "silent misparse risk" flagged as a v2 trigger criterion |
| Serial I/O error (unplug) | Read/write returns an OS error from `go.bug.st/serial` | Transition to `offline` immediately; start reconnect loop |
| Board reset on serial open | DTR toggling on port open resets AVR-based Uno/Nano boards (a side effect of the bootloader's auto-reset design, not something pinctl triggers deliberately) | Session waits a fixed **boot delay (~2 s)** after opening the port before sending anything, so the bootloader has finished and StandardFirmata is running and ready to receive; the handshake's own read timeout is set comfortably longer than this delay |
| Watchdog / health check | No traffic (report or successful write ack-equivalent) seen within a rolling window (e.g. 5× the sampling interval, minimum ~1 s) | Treated the same as a detected I/O error — proactively marks the session `offline` and starts reconnect, rather than waiting for the OS to notice a half-dead connection (a board that's hung but keeps the USB link up would otherwise never be detected) |
| Write timeout | `DigitalWrite`/`PWMWrite` caller-supplied `ctx` expires before the write is flushed to the port | Return the `ctx` error to the caller (the engine tick treats it as a failed write for that node this tick, per [03-architecture.md](03-architecture.md#b-tick-execution)); does not, by itself, mark the board offline (a single slow write isn't proof of disconnection — the watchdog above is what escalates a *pattern* of no traffic) |

## Message sequence diagrams

### 1. Connect handshake

```
Runtime (session goroutine)                Arduino (StandardFirmata)
        |                                          |
        |--- open serial port (57600, 8N1) ------->|
        |                                    (DTR toggle resets MCU)
        |                                          | bootloader ~2s, then StandardFirmata boots
        |<---------- 0xF9 <major> <minor> ---------| (Protocol Version, unprompted)
        |--- 0xF0 0x79 0xF7 (Report Firmware) ----->|
        |<-- 0xF0 0x79 <ver> <name...> 0xF7 --------|
        |--- 0xF0 0x6B 0xF7 (Capability Query) ---->|
        |<-- 0xF0 0x6C <per-pin modes...> 0xF7 -----|
        |--- 0xF0 0x69 0xF7 (Analog Mapping Query) ->|
        |<-- 0xF0 0x6A <pin->channel map> 0xF7 -----|
        |--- 0xF0 0x7A <interval> 0xF7 (Sampling) -->|
        |--- 0xF4 <pin> <mode> (per bound pin) ----->|
        |--- 0xC0+ch 0x01 (enable analog reporting)->|
        |--- 0xD0+port 0x01 (enable digital report)->|
        |                                          |
   session -> ready
```

### 2. Analog read flow (steady state)

```
Arduino                                    Runtime session goroutine       Engine tick
   |                                              |                            |
   | (every sampling interval)                    |                            |
   |--- 0xE0+ch <lsb> <msb> (analog report) ----->|                            |
   |                                        update PinState cache               |
   |                                              |                            |
   |                                              |<--- PinState(pin) ---------|  (every 20ms tick,
   |                                              |---- cached value --------->|   non-blocking)
```

### 3. Digital write flow

```
Engine tick (sink node)     Runtime session goroutine              Arduino
      |                              |                                |
      |--- DigitalWrite(pin, hi) --->|                                |
      |                       queue on writeCh                        |
      |                              |--- 0x90+port <lsb> <msb> ----->|
      |                              |                          apply to pin(s)
      |<--- return (queued/ctx ok) --|                                |
```

Note: Firmata gives no application-level acknowledgement for a digital
write — the "ack" `Session.DigitalWrite` waits for is the write reaching the
OS write buffer / port successfully (or the caller's `ctx` expiring), not
confirmation the MCU applied it. See
[Limits of Firmata](#limits-of-firmata-and-the-v2-path).

### 4. Reconnect

```
Runtime board.Manager                      Arduino (unplugged then replugged)
        |                                          |
        |<---- read/write returns I/O error -------X   (cable pulled)
   session -> offline
        |--- close port ----------------------------|
        |  wait backoff (500ms, 1s, 2s, 4s, ...)     |
        |--- attempt open port ---------------------X  (still unplugged: fails, backoff continues)
        |                                          |
        |                                     (cable replugged)
        |--- attempt open port --------------------->|  (succeeds)
        |                        [replay full handshake, see diagram 1]
        |                                          |
   session -> ready ; backoff reset to initial value
        |--- broadcast BoardEvent{Connected} ------->| (to board.Manager.Watch subscribers)
```

## Limits of Firmata and the v2 path

This section is the serial-protocol-level companion to
[04-firmware-agent.md, Firmata limitations relevant to pinctl](04-firmware-agent.md#firmata-limitations-relevant-to-pinctl)
and [v2: custom pinctl-agent](04-firmware-agent.md#v2-custom-pinctl-agent) —
restated here specifically as protocol/session-layer consequences:

| Limit | Consequence for `internal/board` | What a v2 `pinctl-agent` protocol would fix (documented only, not built) |
|---|---|---|
| Sampling interval is global, not per-pin | A board with one pin needing fast sampling and one needing slow sampling can't have both; the session sets one interval for the whole board | Per-channel sampling rates in a batched report frame |
| No write acknowledgement | `DigitalWrite`/`PWMWrite` "succeeds" once queued/flushed to the serial port, not once the MCU applies it — a corrupted or dropped write byte is invisible to the runtime | Per-frame CRC + explicit ack/nack, so a failed write is detectable rather than silently assumed to have landed |
| No atomic multi-pin writes | Two pins meant to change "together" are still two sequential serial writes with a small real gap between them (see [04-firmware-agent.md](04-firmware-agent.md#firmata-limitations-relevant-to-pinctl)) | A batched-write frame applying N pins in one MCU-side operation |
| No resync marker on corrupted frames | A dropped/garbled byte can be misparsed as a different valid message rather than detected as corruption (mitigated today only by scanning for the next plausible status byte, see [Error handling](#error-handling)) | Length-prefixed or delimited framing with CRC, making corruption detectable instead of silently misparsed |
| No board-side heartbeat | A wedged (not crashed, not unplugged) MCU is only caught by the runtime's own health-check timeout, which can't distinguish "hung" from "just quiet because nothing changed" until the window elapses | Explicit heartbeat frame, letting the runtime detect a hang faster and more precisely, and enabling MCU-side watchdog self-reset |
| 57600 baud ceiling | Throughput budget as computed in [04-firmware-agent.md](04-firmware-agent.md#firmata-limitations-relevant-to-pinctl) (~5760 B/s) constrains how many pins/boards/how-fast sampling is viable before queuing lag appears | Higher baud (115200–921600) — the USB-serial bridge on Uno/Nano-class boards has headroom above Firmata's default |

None of these are hit by the MVP's expected load (a handful of boards, modest
pin counts, 20 ms tick). Per the brief and
[04-firmware-agent.md](04-firmware-agent.md#trigger-criteria-for-building-it),
building `pinctl-agent` is gated on actually observing one of these limits in
a running deployment, not on this list alone.
