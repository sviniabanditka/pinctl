# pinctl — Firmware / Agent (Microcontroller Side)

This document covers everything that runs *on* the Arduino: what the agent's job
is, why StandardFirmata was picked for the MVP, the one-time flashing procedure,
the Uno pin map, Firmata's practical limits for pinctl, a support matrix, and the
v2 "custom agent" option. It follows the fixed decisions in
[00-design-brief.md](00-design-brief.md) and feeds the transport layer described
in [05-serial-protocol.md](05-serial-protocol.md). Where the agent sits in the
overall system is shown in [03-architecture.md](03-architecture.md).

## Contents

- [Role of the generic agent](#role-of-the-generic-agent)
- [Why StandardFirmata for the MVP](#why-standardfirmata-for-the-mvp)
- [Flashing instructions (the only firmware step)](#flashing-instructions-the-only-firmware-step)
- [Arduino Uno pin map](#arduino-uno-pin-map)
- [Firmata limitations relevant to pinctl](#firmata-limitations-relevant-to-pinctl)
- [Supported / expected-to-work boards](#supported--expected-to-work-boards)
- [v2: custom pinctl-agent](#v2-custom-pinctl-agent)

## Role of the generic agent

Per the design brief, the microcontroller is deliberately kept "dumb": it runs a
single, generic, **never-reflashed** agent that does nothing but expose the
board's pins over USB serial. All decision-making — comparisons, thresholds,
delays, debounce, pipeline logic — lives in the pinctl runtime on the
Raspberry Pi, not on the MCU.

The agent's job is limited to:

- Reporting which pins exist and what each pin can do (digital in/out, PWM,
  analog in) — the *capability query*.
- Setting a pin's mode on request (`INPUT`, `OUTPUT`, `PWM`, `ANALOG`).
- Writing digital or PWM values to a pin on request.
- Sampling analog/digital inputs at a configurable interval and streaming the
  values back unprompted (the *reporting* mechanism).
- Nothing else. No conditionals, no state machines, no persistence, no timers
  beyond the sampling interval.

This split is why "hardware behavior fully configurable without recompiling or
flashing firmware" (README) holds: the firmware never encodes application
logic, so it never needs to change when a pipeline changes.

## Why StandardFirmata for the MVP

[StandardFirmata](https://github.com/firmata/arduino) is the sketch bundled
with the Arduino IDE examples (`File > Examples > Firmata > StandardFirmata`).
It already implements exactly the generic-agent contract above:

| Capability pinctl needs | StandardFirmata support |
|---|---|
| Digital I/O (read/write) | Yes — digital port messages |
| Analog read | Yes — analog messages, per-pin reporting |
| PWM output | Yes — analog message used as PWM value on PWM-capable pins |
| Pin capability query | Yes — Capability Query sysex, enumerates modes per pin |
| Configurable sampling interval | Yes — Sampling Interval sysex |

Rationale (from the brief): it is battle-tested, requires **zero firmware
work** from pinctl (the sketch is flashed as-is, unmodified), and mature Go
Firmata client libraries already exist to talk to it. Building a custom
protocol before hitting a real limitation would be premature — see
[v2: custom pinctl-agent](#v2-custom-pinctl-agent) for the criteria that would
justify it.

## Flashing instructions (the only firmware step)

This is the **only** step a pinctl user ever performs on the firmware side —
once, per physical board, ever. After this, the board is never touched again;
all further changes happen in the web UI.

Using [`arduino-cli`](https://arduino.github.io/arduino-cli/):

```bash
# 1. One-time setup: install the AVR core (covers Uno/Nano/ATmega328P boards)
arduino-cli core update-index
arduino-cli core install arduino:avr

# 2. Install the Firmata library (provides the StandardFirmata example sketch)
arduino-cli lib install Firmata

# 3. Find the board and its port
arduino-cli board list
# e.g. /dev/ttyACM0   arduino:avr:uno   Arduino Uno

# 4. Locate the StandardFirmata sketch shipped with the Firmata library
#    (typically under ~/Arduino/libraries/Firmata/examples/StandardFirmata)
STANDARD_FIRMATA=~/Arduino/libraries/Firmata/examples/StandardFirmata

# 5. Compile and upload — one command, no source edits
arduino-cli compile --fqbn arduino:avr:uno "$STANDARD_FIRMATA"
arduino-cli upload -p /dev/ttyACM0 --fqbn arduino:avr:uno "$STANDARD_FIRMATA"
```

After upload, the board immediately speaks Firmata at 57600 baud over USB CDC
serial (see [05-serial-protocol.md](05-serial-protocol.md)) and pinctl's
runtime can discover and use it without any further firmware interaction.
There is no OTA flashing and no re-flash-on-config-change in pinctl — this is
an explicit non-goal (see the brief).

## Arduino Uno pin map

The Uno (ATmega328P) is the MVP reference board. Pin capabilities as exposed
through Firmata's Capability Query:

| Pin | Firmata pin # | Digital I/O | PWM | Analog in | Notes |
|---|---|---|---|---|---|
| D0 | 0 | Yes | No | No | **Reserved** — UART RX, used for USB serial itself |
| D1 | 1 | Yes | No | No | **Reserved** — UART TX, used for USB serial itself |
| D2 | 2 | Yes | No | No | |
| D3 | 3 | Yes | **Yes** | No | |
| D4 | 4 | Yes | No | No | |
| D5 | 5 | Yes | **Yes** | No | |
| D6 | 6 | Yes | **Yes** | No | |
| D7 | 7 | Yes | No | No | |
| D8 | 8 | Yes | No | No | |
| D9 | 9 | Yes | **Yes** | No | |
| D10 | 10 | Yes | **Yes** | No | |
| D11 | 11 | Yes | **Yes** | No | |
| D12 | 12 | Yes | No | No | |
| D13 | 13 | Yes | No | No | Onboard LED wired here |
| A0 | 14 | Yes* | No | **Yes** | Usable as digital pin 14 if reconfigured |
| A1 | 15 | Yes* | No | **Yes** | |
| A2 | 16 | Yes* | No | **Yes** | |
| A3 | 17 | Yes* | No | **Yes** | |
| A4 | 18 | Yes* | No | **Yes** | Also I2C SDA (v2 use) |
| A5 | 19 | Yes* | No | **Yes** | Also I2C SCL (v2 use) |

`*` Analog pins can be switched to digital I/O mode via `SET_PIN_MODE`, but
pinctl's MVP device roles (`digital-in`, `digital-out`, `analog-in`,
`pwm-out`) treat A0–A5 as analog-input-only by default.

D0/D1 are excluded from pinctl's device catalog for this board: they carry the
serial link used to talk to the agent itself, so claiming them for GPIO would
break the connection.

## Firmata limitations relevant to pinctl

These are the concrete constraints StandardFirmata imposes, kept here so v2
trigger criteria can be evaluated against real numbers rather than guesses:

- **Sampling rate.** The sampling interval sysex accepts an interval in
  milliseconds; StandardFirmata's practical floor is generally accepted to be
  around 1–2 ms, but running that low leaves no serial bandwidth for anything
  else (see throughput below). pinctl's pipeline tick is 20 ms — matching or
  slightly undersampling that tick is the realistic operating point.
- **57600 baud throughput math.** At 57600 bps, ignoring stop/parity
  overhead, that's roughly 5760 bytes/sec (8 data bits + start/stop ≈ 10 bits
  per byte at 57600 bps ⇒ ~5760 B/s). A single analog reporting message is 3
  bytes; a digital port message is also 3 bytes. With, say, 6 analog pins
  reporting at a 20 ms interval, that's `6 analog pins × 3 bytes = 18
  bytes / 20 ms = 900 B/s` for analog alone — comfortably inside budget. But
  push the interval down or add digital port reporting for all pins and
  the board can saturate the link, causing queuing delay that shows up as lag
  between pipeline tick and actual value freshness. This is a real, hittable
  ceiling with more boards, more reporting pins, or shorter intervals.
- **No atomic multi-pin operations.** Firmata has no "set these N pins
  together" message. Digital port messages batch 8 pins that share a port,
  but writing pin A and pin B in the same instant still means two serial
  writes in sequence — there is a few-hundred-microsecond-to-millisecond gap
  between them, so simultaneous multi-pin actuation (e.g. two relays that must
  switch in the same tick) is only "same pipeline tick," never truly atomic
  at the wire level.
- **No board-side timestamping.** Reported values carry no timestamp; the
  runtime timestamps on arrival, so latency (serial + OS buffering) shows up
  as jitter in the pipeline, not as a knowable board-side delay.
- **No flow control / backpressure signal.** If the runtime falls behind
  reading, the board keeps sending; it has no way to be told "slow down,"
  it can only be told a new (larger) interval.

None of this blocks the MVP — pinctl's 20 ms tick and modest per-board pin
counts stay well inside these limits — but they are the first things to
measure if latency or missed samples are ever reported as a bug.

## Supported / expected-to-work boards

| Board | MCU | Status |
|---|---|---|
| Arduino Uno | ATmega328P | **MVP target** — tested |
| Arduino Nano | ATmega328P | **MVP target** — tested (same core, same pin capabilities) |
| Arduino Mega 2560 | ATmega2560 | Expected to work — StandardFirmata supports it, more pins/UARTs; untested |
| Arduino Leonardo / Micro | ATmega32U4 | Expected to work — native USB CDC differs slightly from Uno's USB-serial bridge; untested |
| ESP8266 / ESP32 boards | Xtensa | Not targeted — Firmata exists for these but pin numbering/voltage (3.3V) differs; out of scope for v0.x |
| Other AVR "Uno-compatible" clones (Nano clones w/ CH340, etc.) | ATmega328P | Expected to work once the correct USB-serial driver is installed on the Pi; untested |

Per the brief: Uno/Nano are the only boards the MVP is built and tested
against. Anything else "should work" because StandardFirmata supports it, but
is explicitly untested and unsupported until exercised.

## v2: custom pinctl-agent

The brief fixes StandardFirmata as the *only* MVP agent and treats a custom
protocol as a **documented, not built**, v2 option. This section exists so
that decision is pre-specified rather than improvised later.

### What a custom `pinctl-agent` firmware would add

- **Binary framing** — a fixed or length-prefixed frame format instead of
  Firmata's mixed variable-length/sysex message set, cutting per-message
  parsing overhead and ambiguity on resync.
- **CRC** — a checksum (e.g. CRC-8/CRC-16) per frame, so corrupted frames are
  detected instead of silently misparsed as a different message type.
- **Higher baud rate** — e.g. 115200–921600 baud (USB CDC on an Uno/Nano-class
  board is limited by the USB-serial bridge, not the wire, so headroom exists
  above Firmata's 57600 default) to raise the throughput ceiling described
  above.
- **Batching** — a single frame reporting multiple pins' values together
  (true atomic snapshot per read cycle), instead of one message per pin.
- **Watchdog heartbeat** — the agent emits (or expects) a periodic heartbeat
  frame so the runtime can detect a wedged/crashed MCU distinctly from "cable
  unplugged," and optionally the agent self-resets via the AVR watchdog timer
  if it stops hearing from the runtime.

### Trigger criteria for building it

Build `pinctl-agent` only when one of these is actually observed, not
speculated:

1. **Throughput ceiling hit in practice** — the 57600 baud budget is
   measurably saturated for a real deployment (multiple boards, or many
   reporting pins at low intervals) and raising Firmata's baud rate isn't a
   viable fix.
2. **Atomicity requirement surfaces** — a pipeline use case genuinely needs
   two or more pins actuated in the same instant and "same 20 ms tick" is
   demonstrated to be insufficient.
3. **Firmata parsing/resync fragility causes field-reported bugs** — garbage
   bytes or partial frames after reconnects (see
   [05-serial-protocol.md](05-serial-protocol.md#error-handling)) cause
   observable pin-state corruption, not just a log warning.
4. **Undetected board hangs** — a wedged MCU is indistinguishable from a
   disconnected one for long enough to cause a real incident, motivating the
   watchdog heartbeat specifically.

Until one of these is demonstrated against a running MVP, StandardFirmata
stays as-is, per the brief.
