# pinctl — MQTT bridge and Home Assistant discovery

pinctl publishes its device values to an MQTT broker and accepts commands
for its output devices, announcing itself to Home Assistant so its pins
appear as entities without any HA-side configuration.

The bridge is **off by default** and adds no behaviour when disabled: no
connection, no goroutine, no topic. It is an observer of the runtime, not a
participant — the pipeline engine neither knows nor cares that it exists.

> This supersedes the "no MQTT/Home Assistant" non-goal in
> [00-design-brief.md](./00-design-brief.md), which the brief has been
> amended to reflect. The rest of that non-goal (no cloud, no multi-user)
> stands.

## Table of contents

- [Principles](#principles)
- [Topics](#topics)
- [Payloads](#payloads)
- [Home Assistant discovery](#home-assistant-discovery)
- [Commands and the pipeline-conflict rule](#commands-and-the-pipeline-conflict-rule)
- [Publishing policy: deadband and rate](#publishing-policy-deadband-and-rate)
- [Availability](#availability)
- [Configuration](#configuration)
- [Architecture](#architecture)
- [Failure modes](#failure-modes)
- [Security](#security)

## Principles

1. **The engine is authoritative.** A pipeline that drives a device owns that
   pin. An MQTT command targeting it is refused, exactly as
   `POST /devices/{id}/write` returns `409 device_driven_by_pipeline`
   ([10-api.md](./10-api.md)). One writer per pin, always.
2. **One read path.** The board is polled once. Its values fan out to every
   observer — the WebSocket hub and the MQTT bridge alike. The bridge never
   touches `internal/board` or `engine.DeviceIO` directly.
3. **Disabled means absent.** With `mqtt.enabled: false` (the default) no
   client is constructed and no goroutine runs.
4. **The broker is untrusted input.** Every inbound payload is validated
   against the device's role and range before it reaches a pin.

## Topics

`base_topic` defaults to `pinctl`. `<device>` is the device's `id`
(`dev_9zq3v7ab4c`), which is stable across renames — names are for humans,
ids are for machines.

| Topic | Direction | Retained | QoS | Purpose |
|---|---|---|---|---|
| `pinctl/status` | out | yes | 1 | `online` / `offline` (LWT) |
| `pinctl/<device>/state` | out | yes | 0 | Current value |
| `pinctl/<device>/available` | out | yes | 1 | `online` / `offline` — the device's board is connected |
| `pinctl/<device>/set` | in | — | 1 | Command for an output device |
| `<discovery_prefix>/<component>/pinctl_<device>/config` | out | yes | 1 | Home Assistant discovery |

State is retained so a subscriber that connects later — or Home Assistant
after a restart — sees the last known value immediately rather than a blank
entity. Discovery configs are retained for the same reason: that is how HA
rediscovers entities on boot.

## Payloads

Plain scalars, not JSON envelopes: HA's default value templates read them
directly, and a human running `mosquitto_sub` sees a number, not a wrapper.

| Role | State payload | Command payload |
|---|---|---|
| `analog-in` | `0`–`1023` (raw ADC counts) | — |
| `digital-in` | `ON` / `OFF` | — |
| `digital-out` | `ON` / `OFF` | `ON` / `OFF` |
| `pwm-out` | `0`–`255` (duty cycle) | `0`–`255` |
| `servo` | `0`–`180` (angle, degrees) | `0`–`180` |

Anything else on a `set` topic is rejected and logged once per device per
minute — a misconfigured automation should not fill the journal.

## Home Assistant discovery

One retained config message per device, published on connect and whenever a
device is created, renamed, or deleted (deletion publishes an empty payload,
which is how HA removes an entity).

| Role | HA component | Notes |
|---|---|---|
| `analog-in` | `sensor` | `state_class: measurement`; no unit — raw counts are unitless |
| `digital-in` | `binary_sensor` | |
| `digital-out` | `switch` | |
| `pwm-out` | `number` | `min: 0`, `max: 255`, `mode: slider` |
| `servo` | `number` | `min: 0`, `max: 180`, `step: 1`, `mode: slider`, `unit_of_measurement: "°"` |

Every entity carries `availability` (both the global `pinctl/status` and its
own `pinctl/<device>/available`, with `availability_mode: all`), a
`unique_id` of `pinctl_<device>`, and a shared `device` block so all of a
board's pins group under one HA device:

```json
{
  "name": "LDR on A0",
  "unique_id": "pinctl_dev_9zq3v7ab4c",
  "state_topic": "pinctl/dev_9zq3v7ab4c/state",
  "availability_mode": "all",
  "availability": [
    { "topic": "pinctl/status" },
    { "topic": "pinctl/dev_9zq3v7ab4c/available" }
  ],
  "state_class": "measurement",
  "device": {
    "identifiers": ["pinctl_brd_4kx9m2pqrs"],
    "name": "Arduino Uno #1",
    "manufacturer": "pinctl",
    "model": "StandardFirmata 2.5",
    "sw_version": "v0.5.0"
  }
}
```

A device whose board has never connected still gets a discovery message; its
`available` topic simply says `offline`. HA shows the entity as unavailable,
which is the truth.

## Commands and the pipeline-conflict rule

A command arrives on `pinctl/<device>/set`. The bridge:

1. Resolves the device. Unknown id → log, drop.
2. Rejects a command for an input role (`analog-in`, `digital-in`) — those
   have no `set` subscription in the first place, so this is defence in depth.
3. **Checks whether an enabled pipeline contains a `device-write` node
   targeting this device.** If one does, the command is refused: the engine
   owns that pin. The bridge logs it, republishes the device's current state
   (so the HA entity snaps back rather than showing a lie), and does nothing
   else.
4. Parses and range-checks the payload against the role.
5. Writes through the same `engine.DeviceIO` the manual REST write uses.

Step 3 is the same predicate the REST API enforces, and it lives in one
place — `store.EnabledPipelineWritingDevice` — so the two surfaces can never
drift apart. A safety rule implemented twice is a safety rule implemented
once and violated once.

## Publishing policy: deadband and rate

An analog pin is never still: ADC noise moves the last bits constantly. The
WebSocket tolerates this — one browser tab, values thrown away on arrival.
An MQTT broker does not: every jitter becomes a retained message, a network
packet, and a row in Home Assistant's recorder database forever.

So the bridge publishes a device's state only when:

- the value changed by at least `analog_deadband` raw counts (default `4`,
  ≈0.4% of range) for `analog-in`; any change at all for booleans, and
- at least `min_publish_interval` has passed since that device's last publish
  (default `1s`), and
- at most `max_publish_interval` has passed regardless of change (default
  `60s`) — a periodic heartbeat so a subscriber knows the value is current
  and not merely unchanged.

These are per-device. Booleans ignore the deadband and publish on every edge,
subject to `min_publish_interval`.

## Availability

The client connects with a **last will** of `offline` on `pinctl/status`,
retained. On successful connect it publishes `online` there. So a broker that
loses pinctl marks every entity unavailable within the keepalive window,
without pinctl having to be alive to say so.

Per-device availability tracks the device's board: `online` while that board's
session is connected, `offline` otherwise. This is why a board that unplugs
greys out exactly its own pins in Home Assistant, not everyone's.

## Configuration

A new `mqtt:` section in `config.yaml` ([14-deployment.md](./14-deployment.md)):

```yaml
mqtt:
  enabled: false                  # default; nothing runs when false
  broker: tcp://192.168.1.10:1883 # tcp://, tls://, ws://, wss://
  client_id: pinctl               # must be unique on the broker
  username: pinctl
  password_file: /etc/pinctl/mqtt-password   # or MQTT_PASSWORD in the env
  base_topic: pinctl
  discovery_prefix: homeassistant # HA's own default; "" disables discovery
  analog_deadband: 4              # raw ADC counts
  min_publish_interval: 1s
  max_publish_interval: 60s
```

`password` may be given inline for a throwaway setup, but `password_file`
(mode `0600`, read once at startup) is what the installer configures. The
password is never logged, never echoed in an error, and never leaves the
process.

`discovery_prefix: ""` publishes state and accepts commands without
announcing anything — the right setting for a broker feeding something that
isn't Home Assistant.

## Architecture

Firmata never reports an output pin's value back, so a poll loop alone can
only ever see `digital-in`/`analog-in`. Output state (`digital-out`,
`pwm-out`, `servo`) becomes observable the other way around: every writer — the
pipeline engine, the REST manual write, and this bridge's own command
handler — writes through one `engine.DeviceIO` wrapped with
`devicebus.WrapWriter`. On a successful write, the wrapper calls
`Bus.NoteWrite`, which feeds the exact same `Update` stream and
change-detection the poll loop uses. `cmd/pinctl` wires this up once, at
startup, before handing the wrapped `DeviceIO` to the engine manager, the
API server, and this bridge's `Deps.IO` — so there is exactly one seam
where a write becomes "observed", no matter which of the three made it.

```
                    engine.DeviceIO (wrapped)
                            │
        ┌───────────────────┼────────────────────┐
        │                   │                    │
   pipeline engine    REST manual write    mqtt command handler
        │                   │                    │
        └───────────────────┼────────────────────┘
                             ▼
                  internal/devicebus.WrapWriter
             (NoteWrite on every successful write)
                             │
internal/board ──> internal/boardio ──> engine.DeviceIO (unwrapped read/write)
                                            │
                          ┌─────────────────┴──────────────────┐
                          │        internal/devicebus          │   one poll,
                          │  polls readable devices, fans out  │   many readers
                          │  (+ NoteWrite for writable ones)   │
                          └───┬────────────────────────────┬───┘
                              │                            │
                    internal/api (WS hub)         internal/mqtt (bridge)
                                                           │
                                                        broker
```

This is why the "devices" WebSocket topic and MQTT's state topic both now
carry output values, not just inputs: a pipeline flipping a relay, a
`POST /devices/{id}/write`, and an MQTT command all reach subscribers
through the same one path.

`internal/mqtt` depends on `store` (devices, pipelines, boards), `devicebus`
(values, via the wrapped `engine.DeviceIO` above), and `board.Manager`'s
status (per-device availability). Nothing depends on `internal/mqtt`;
deleting the package would leave a compiling binary with one dead config
section.

The bridge owns one goroutine for its command handler and one for its publish
loop, both stopped by the context that stops the HTTP publishers.

## Failure modes

| Failure | Behaviour |
|---|---|
| Broker unreachable at startup | Log once, retry with backoff forever. pinctl serves its UI and runs pipelines regardless — the bridge is never on the critical path. |
| Broker drops mid-run | The client reconnects with backoff; on reconnect it republishes discovery, availability and every device's current state. |
| Command for a pipeline-driven device | Refused, logged, current state republished. See [above](#commands-and-the-pipeline-conflict-rule). |
| Command with a malformed payload | Dropped, logged (rate-limited per device). |
| Board offline | That board's devices publish `offline` on their availability topics. Values stop; the last retained value stays retained, which is why availability exists. |
| pinctl killed | LWT fires: `pinctl/status` = `offline`. |

## Security

The bridge accepts writes to physical pins from whoever can publish to the
broker. Treat broker access as equivalent to physical access to the board.

- Use a dedicated broker account for pinctl, restricted to `pinctl/#` and
  the discovery prefix.
- Prefer `tls://`; the credentials are otherwise on the wire in the clear.
- pinctl does not authenticate its own MQTT clients, has no ACL of its own,
  and never will — that is the broker's job, and doing it twice would do it
  worse.
- The pipeline-conflict rule is a correctness guarantee, not a security
  boundary: it stops an automation from fighting the engine, not an attacker
  from toggling a relay they already have broker rights to.
