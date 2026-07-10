# pinctl — Network boards (Firmata over TCP)

A board does not have to be on the end of a USB cable. Firmata is a byte
protocol; a TCP socket carries bytes. An ESP32 running `StandardFirmataWiFi`
listens on a port, and pinctl connects to it exactly as it opens a serial
port — same handshake, same capability query, same engine, same editor.

This is why `Transport` is `io.ReadWriteCloser` and `Session` has never
mentioned a serial port: a `net.Conn` already satisfies it. The work is not in
the session. It is in discovery, identity, and being honest about a wire that
can vanish for thirty seconds without telling anyone.

## Table of contents

- [What changes and what doesn't](#what-changes-and-what-doesnt)
- [Addresses](#addresses)
- [Identity](#identity)
- [Liveness](#liveness)
- [Latency and tick rate](#latency-and-tick-rate)
- [Configuration](#configuration)
- [Security](#security)
- [Firmware](#firmware)
- [Failure modes](#failure-modes)
- [What is deliberately not built](#what-is-deliberately-not-built)

## What changes and what doesn't

Unchanged: `Session`, the handshake, capability discovery, the pin cache, the
engine, `internal/boardio`, the API, the UI, MQTT, history. A network board is
a board.

Changed:

| Concern | Serial | Network |
|---|---|---|
| Transport | `go.bug.st/serial` | `net.Conn` over TCP |
| Boot delay | ~2s (opening the port resets the MCU) | none — the MCU is already up |
| Discovery | enumerate ports, match an allowlist | connect to explicitly configured addresses |
| Identity | USB VID/PID + serial number | the configured address (see below) |
| Death | read returns an error promptly | a socket can hang; see [Liveness](#liveness) |

The active `0xF9` protocol-version query the handshake already sends —
introduced because opening `/dev/cu.*` on macOS never asserts DTR — is exactly
what a TCP board needs too: it has been running for hours and its boot-time
report is long gone.

## Addresses

A board address is a string. Serial addresses are paths (`/dev/ttyACM0`).
Network addresses carry a scheme: `tcp://192.168.1.50:3030`.

`Open(addr)` dispatches on the scheme. There is no second `Open`, no parallel
`NetworkSession`, and no `if isNetwork` scattered through the manager.

## Identity

pinctl re-associates a board across reconnects by an identity key, so a device
bound to "the LDR on A0" follows its board rather than its port
([05-serial-protocol.md](./05-serial-protocol.md)). A USB board offers a serial
number. **Firmata offers no unique identifier**, and a TCP socket has none:
there is nothing to ask.

So a network board's identity is its configured address —
`kind: "network"`, `key: "tcp://192.168.1.50:3030"`.

The consequence is worth stating plainly rather than hiding: **if the board's
IP changes, pinctl sees a new board**, and devices bound to the old one stay
bound to something that will never reconnect. Two ways to avoid it, both the
operator's job and both documented in the config:

- give the board a DHCP reservation, or
- address it by hostname: `tcp://esp32-porch.local:3030`. The name is stable
  even when the address isn't, so the identity is stable too.

Hostname is the recommended form. pinctl resolves it on every connect attempt,
which is also what lets a rebooted board that came back on a new address be
found again.

## Liveness

A serial port that goes away fails a read. A TCP socket does not: unplug the
board's power and the socket sits there, healthy as far as the kernel knows,
until something times out — which, with default settings, can be many minutes.
Meanwhile pipelines tick against a pin cache frozen at the last value it ever
saw. That is the same silent-stale-data failure as a board that reboots
mid-session, and it deserves the same seriousness.

Three mechanisms, all necessary:

1. **TCP keepalive** on the connection (15s idle, so a dead peer is detected in
   well under a minute rather than after the kernel's default two hours).
2. **A read deadline** longer than the sampling interval but far shorter than
   patience: a board that reports analog values every 20ms and has said nothing
   for 10s is not a slow board, it is a gone board. The deadline is refreshed
   on every successful read, so a legitimately quiet board — all inputs stable,
   nothing to report — must still speak, which it does: Firmata's analog
   reporting sends values whether or not they changed.
3. **Dial and write timeouts**, so a half-open socket cannot wedge a write from
   the engine's tick goroutine.

A board with no analog inputs enabled reports nothing when nothing changes, and
would trip the read deadline while perfectly healthy. So on a network board,
whenever nothing has been read for a while, pinctl sends a protocol-version
query as a heartbeat — a request the board must answer, costing two bytes.
`Session` has no device model to consult, so it heartbeats regardless of what
is bound to the board; a talkative board simply never triggers one.

**The heartbeat's reply must not look like a reboot.** Mid-session reboot
detection fires on an *unsolicited* version report
([05-serial-protocol.md](./05-serial-protocol.md)), and the heartbeat solicits
exactly that message. The session therefore marks a heartbeat as outstanding
and swallows the matching reply. Get this wrong and a quiet board reconfigures
itself every few seconds, forever.

A serial `Session` shares this exact mechanism (`Session.heartbeatLoop`,
`heartbeatAwaiting`) rather than a parallel implementation — a serial board's
read never times out on its own the way a TCP read deadline does, so there
the same heartbeat also does the killing; see
[05-serial-protocol.md](./05-serial-protocol.md)'s "Liveness" section.

## Latency and tick rate

USB serial at 57600 baud has predictable sub-millisecond latency. WiFi has tens
of milliseconds and occasional multi-hundred-millisecond stalls when the radio
sleeps or the channel is busy.

This does not break the engine: reads come from a cache the read loop fills
asynchronously, and writes are fire-and-forget. It does mean a 20ms tick on a
network board produces control that is *sampled* every 20ms but *actuated* with
WiFi's jitter. For anything where that matters — a servo tracking a sensor, a
PID loop — the honest answer is that the loop belongs on the board, not across
a radio link. pinctl is not that, and does not pretend to be.

Recommended: `tickMs: 100` or slower for pipelines that drive network boards.
The UI does not enforce it; the docs say it once, here.

## Configuration

```yaml
serial:
  allowlist:
    - /dev/ttyACM0

network:
  # Explicitly configured board addresses. Each is dialled on startup and
  # retried with backoff forever. There is no scanning: pinctl will not
  # probe your network looking for open ports.
  boards:
    - tcp://esp32-porch.local:3030
    - tcp://192.168.1.51:3030
  dial_timeout: 5s
  read_timeout: 10s      # a board silent for this long is treated as gone
  keepalive: 15s
```

An empty or absent `network.boards` means no network boards, no dialling, no
goroutine. Serial discovery is unaffected.

## Security

**Firmata over TCP is unauthenticated and unencrypted.** Anyone who can reach
the board's port can drive every pin on it — read your sensors, and switch
whatever your relays switch. The board itself has no concept of a password.

pinctl cannot fix this, and does not pretend to by adding a setting. What is
true:

- Put network boards on a VLAN or an IoT SSID that cannot reach the internet,
  and that your untrusted devices cannot reach either.
- The board listens; pinctl connects. A firewall rule permitting only the Pi's
  address to reach port 3030 is the single most effective control available,
  and it is one line on most routers.
- `StandardFirmataWiFi` accepts one client at a time. That is not a security
  feature — it is a denial-of-service surface: whoever connects first owns the
  board, and pinctl will sit retrying.

If the pins actuate something that matters, run the board on USB.

## Firmware

`StandardFirmataWiFi` from the Firmata library, with `wifiConfig.h` edited for
the network and a static `TCP_PORT` (3030 by convention). It is a TCP **server**
and accepts **one client**.

The ESP32's pin numbering is its own; pinctl learns it from the capability
query like it does for any board, so the UI shows what the board reports rather
than what an Uno would have.

## Failure modes

| Failure | Behaviour |
|---|---|
| Board unreachable at startup | Dial fails, retried with the same backoff serial boards use (1s→10s). Other boards and the runtime are unaffected. |
| Board powered off mid-run | Keepalive or the read deadline fires within ~10s; the session dies; the board goes `disconnected`; its pipelines degrade exactly as for an unplugged USB board. |
| WiFi drops and returns | The socket dies, the manager redials, the handshake reruns, `OnReady` re-applies pin modes, safe values and servo configuration, and enabled pipelines restart. Identical to the USB reconnect path. |
| Board reboots but the socket survives | The unsolicited version report fires the existing mid-session-reboot detection and the board is reconfigured without a reconnect. |
| Hostname stops resolving | Dial fails, retried. The board keeps its identity, because the identity is the name, not the address. |
| Someone else connects first | pinctl's dial is refused or its connection is dropped. It retries. The log says so, once per backoff step, not once per attempt. |

## What is deliberately not built

**mDNS discovery.** `StandardFirmataWiFi` does not advertise a service, so
there would be nothing to discover; adding a browser for `_firmata._tcp` would
find boards only if their firmware were also modified. Configured addresses are
honest about what is actually knowable.

**Network scanning.** pinctl will not sweep a subnet for open ports. A tool
that probes your network because it might find something useful is a tool you
cannot deploy in someone else's house.

**TLS.** The firmware does not speak it. A setting implying pinctl could
protect a link the board cannot protect would be a lie.
