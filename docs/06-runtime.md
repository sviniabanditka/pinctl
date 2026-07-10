# pinctl — Runtime

See [00-design-brief.md](./00-design-brief.md) for the fixed design decisions
this document builds on, [03-architecture.md](./03-architecture.md) for the
component/deployment view, [07-pipeline-engine.md](./07-pipeline-engine.md)
for how pipelines are executed inside this runtime, and
[09-data-model.md](./09-data-model.md) for the JSON shapes referenced below.

## Process model

pinctl runs as **one process, one binary**: `cmd/pinctl/pinctl`. There is no
separate API server, worker, or database process — board sessions, the
pipeline engine, the REST/WebSocket API, and the embedded Vue 3 web UI all run
as goroutines inside this single process.

- **Target OS/arch:** `linux/arm64` and `linux/arm` (Raspberry Pi OS,
  32- and 64-bit).
- **Deployment:** a `systemd` unit. This is the only supported production
  deployment mode for v0.x — no Docker requirement, no orchestrator.
- **No external services.** No database server, no message broker, no reverse
  proxy required. The web UI is embedded via `embed.FS` so the binary is the
  full deployable artifact (per the brief's "one artifact" principle).

Example `systemd` unit (`/etc/systemd/system/pinctl.service`):

```ini
[Unit]
Description=pinctl runtime
After=network.target

[Service]
ExecStart=/usr/local/bin/pinctl -config /etc/pinctl/config.yaml
Restart=on-failure
RestartSec=2
User=pinctl
Group=dialout
AmbientCapabilities=
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

The service user must be in the `dialout` group (or equivalent) to open
`/dev/ttyUSB*` / `/dev/ttyACM*` serial devices without running as root.

`Restart=on-failure` is a safety net, not a crutch: per the error philosophy
below, the runtime is designed to never need it for user-config-triggered
failures. It exists for genuine process-level faults (panics in third-party
code, OOM).

## Startup and config loading

### Flags (`cmd/pinctl`)

| Flag | Meaning |
|---|---|
| `-config <path>` | Path to `config.yaml`. Empty (default): search `/etc/pinctl/config.yaml`, then `~/.pinctl/config.yaml`; a missing file at any of those falls back to defaults (not an error). |
| `-listen <addr>` | Overrides `http.addr` from `config.yaml`. |
| `-data <dir>` | Overrides `data_dir` from `config.yaml`. |
| `-port <path>` | Dev convenience: connect to exactly this serial port (e.g. `/dev/ttyACM0`), bypassing discovery and the serial allowlist entirely — no `board.Manager`, no reconnect loop, no `boards.json`. Omitting it (the production path) lets `config.yaml`'s `serial.allowlist` and the multi-board `board.Manager` drive connection. |

On startup pinctl:

1. Parses flags (above) and loads config from `-config` (or the default
   search path).
2. Loads and validates the YAML config (schema below). Invalid/missing
   required fields → fail fast with a clear error and non-zero exit; this is
   the *one* place startup is allowed to abort, because there is no safe
   partial-config state to run in.
3. Opens the device/pipeline store, creating the data directory on first run.
4. Loads device and pipeline definitions from the data directory (see
   [09-data-model.md](./09-data-model.md)) — a definition that fails to parse
   or validate is skipped with a logged error, **not** a fatal startup error
   (see "Error philosophy").
5. Without `-port`: starts `board.Manager`, which opens every allowlisted
   serial port it can and starts a Firmata session per board, with its own
   reconnect-with-backoff loop for the rest. With `-port`: opens exactly one
   board directly, no `Manager`. Either way, a board that fails to open
   (unplugged, wrong permissions, not in the allowlist) is marked
   `disconnected`; its pipelines pause (see
   [07-pipeline-engine.md](./07-pipeline-engine.md)) rather than blocking
   startup.
6. Starts every enabled pipeline's scheduler.
7. Starts the HTTP server (REST + WebSocket + embedded static UI) on the
   configured port.

### Config schema

Single YAML file, `/etc/pinctl/config.yaml` in production (`~/.pinctl/config.yaml`
in dev), per the brief's fixed decision. Fields:

| Field | Type | Default | Description |
|---|---|---|---|
| `http.addr` | string | `:8080` | Listen address for REST + WebSocket + web UI. |
| `data_dir` | string | `/var/lib/pinctl` (dev: `~/.pinctl`) | Root directory for device/pipeline JSON files. |
| `serial.allowlist` | []string | `["/dev/ttyACM*", "/dev/ttyUSB*"]` | Serial device paths pinctl may open (filepath.Match globs). An **empty list permits nothing** and skips port enumeration entirely — the setting for a network-boards-only install. `["*"]` permits any port. |
| `serial.baud` | int | `57600` | Baud rate for Firmata sessions (brief fixes this at Firmata's default). |
| `log.level` | string | `info` | One of `debug`, `info`, `warn`, `error`. |
| `log.format` | string | `text` | One of `json`, `text`. `text` is for local dev readability; `json` for production/log aggregation. |

Full example:

```yaml
http:
  addr: ":8080"

data_dir: /var/lib/pinctl

serial:
  allowlist:
    - /dev/ttyACM0
    - /dev/ttyUSB0
  baud: 57600

log:
  level: info
  format: json
```

Unknown fields in the YAML are a hard load error (`strict` decoding) —
catching typos in hand-edited config beats silently ignoring them.

## Logging strategy

pinctl logs through Go's standard `log` package (`log.Printf`/`log.Fatalf`) —
no third-party logging dependency, consistent with the "one artifact, minimal
dependencies" philosophy. Every call site prefixes its message by hand (e.g.
`"pinctl: board %s: ..."`, `"board: %s rebooted, ..."`) rather than emitting
key-value attributes; there is no per-component child logger and no `log/slog`
usage anywhere in the codebase today.

- **`log.level`/`log.format` are parsed and defaulted (`info`/`text`) by
  `internal/config` but not yet consulted anywhere else** — nothing filters
  by level and nothing switches output format; every `log.Printf` call
  always fires, always as an interpolated text line to stderr. Structured,
  leveled, JSON-capable logging (as originally sketched here) is not
  implemented; treat `log.level`/`log.format` as reserved config fields for
  now.
- **No log-based control flow.** Logs are for humans; the event bus (below)
  is the mechanism other components and the API use to react to state
  changes.
- In practice, lifecycle events (board connect/disconnect/reboot, pipeline
  start/restart, server start/shutdown) and internal faults (serial write
  failures, config/store errors) are what gets logged — see
  `cmd/pinctl/main.go` and `internal/board/session.go` for the current set
  of call sites.

## Package-by-package description

Matches the shipped repository layout (see
[00-design-brief.md](./00-design-brief.md#repository-layout-planned) and
[03-architecture.md](./03-architecture.md#component-breakdown)):

```
pinctl/
  cmd/pinctl/          # main
  internal/board/      # serial + firmata session mgmt
  internal/boardio/    # adapts board.Session to engine.DeviceIO; multi-board routing
  internal/config/     # config.yaml loading
  internal/engine/     # pipeline engine (graph, nodes, scheduler)
  internal/expr/       # total expression evaluator (backs the expression node)
  internal/model/      # persisted domain types shared by store, engine, api
  internal/store/      # JSON file persistence
  internal/api/        # REST + WS handlers
  web/                 # Vue 3 app (built dist embedded)
  docs/
```

- **`cmd/pinctl`** — process entrypoint only: flag parsing, config loading,
  constructing and wiring the `board`, `boardio`, `engine`, `store`, and
  `api` packages, installing signal handlers, and running until shutdown.
  Contains no business logic.
- **`internal/board`** — owns serial port lifecycle and Firmata protocol
  sessions (`go.bug.st/serial` per the brief). One `Session` per connected
  board: connect/reconnect (via `board.Manager`), pin capability discovery,
  reading/writing pin state, mid-session reboot detection. Also enforces the
  serial allowlist from config.
- **`internal/boardio`** — adapts `internal/board`'s `Session` to
  `engine.DeviceIO`, mapping a `model.Device`'s role onto the matching
  Firmata pin operation; routes by `device.BoardID` for the multi-board case
  (`boardio.MultiIO`). The engine depends only on `internal/boardio`, never
  on `internal/board` directly.
- **`internal/config`** — loads and validates `config.yaml` (schema below),
  with documented defaults and a fixed search order when no `-config` path
  is given.
- **`internal/engine`** — the pipeline engine: graph representation, topo
  sort/validation, the node interface and built-in node implementations
  (including the `expression` node, backed by `internal/expr`), and the
  per-pipeline ticker/scheduler. Reads/writes device values through
  `engine.DeviceIO` (implemented by `internal/boardio`), never talks to
  serial directly. See [07-pipeline-engine.md](./07-pipeline-engine.md) for
  the full design.
- **`internal/expr`** — a small, dependency-free, total expression
  evaluator over declared typed inputs, used by the engine's `expression`
  node. No variables, loops, user-defined functions, state, or I/O — see
  [03-architecture.md, ADR-6](./03-architecture.md#adr-6-expression-node-as-a-total-evaluator-not-an-embedded-scripting-language).
- **`internal/model`** — the persisted domain types (`Board`, `Device`,
  `Pipeline`, `Node`, `Edge`) shared by `store`, `engine`, and `api`, whose
  JSON shapes follow [09-data-model.md](./09-data-model.md).
- **`internal/store`** — JSON file persistence for device and pipeline
  definitions under `data_dir`. Owns atomic writes (write-temp-then-rename),
  schema versioning/migration (see below), and load-time validation before
  handing definitions to `engine`/`board`. No database in the MVP, per the
  brief.
- **`internal/api`** — REST handlers for CRUD on boards/devices/pipelines,
  and the WebSocket handler that streams live pin values, board state, and
  pipeline state to the web UI, sourced from the internal event bus. Also
  serves the embedded `web/` static build.
- **`web/`** — the Vue 3 + Vite + Pinia + Vue Flow admin UI; built and
  embedded into the Go binary via `embed.FS` at build time. Not part of the
  runtime process model beyond being static assets it serves.

## Internal event bus

`board`, `engine`, and `api` do not call into each other directly for state
changes — they communicate through an in-process event bus (a typed
publish/subscribe mechanism, e.g. a small wrapper over Go channels or a
fan-out broadcaster; no external broker). This keeps the packages decoupled:
`board` and `engine` don't need to know a WebSocket client exists, and `api`
doesn't need to know how a pin value changed.

Event categories:

| Event | Published by | Consumed by | Payload (sketch) |
|---|---|---|---|
| **Pin update** | `board` | `engine` (as node input), `api` (live view) | `board_id`, `pin`, `value`, `timestamp` |
| **Board state change** | `board` | `engine` (pause/resume pipelines), `api` (UI status) | `board_id`, `state` (`connected` \| `disconnected` \| `error`), `detail` |
| **Pipeline state change** | `engine` | `api` (UI status, debugging) | `pipeline_id`, `state` (`running` \| `paused` \| `error`), `detail`, tick stats |

Design notes:

- The bus is **fire-and-forget, non-blocking**: a slow or absent subscriber
  (e.g. no WebSocket client connected) must never back-pressure `board` or
  `engine`. Publishers drop events for a subscriber that isn't keeping up
  rather than block a tick.
- The bus carries **state, not commands.** Commands (enable a pipeline,
  write a device value from the UI) flow the other direction through normal
  method calls / REST handlers, not the event bus.
- This is an in-process abstraction only — it is not a message broker and
  never crosses the process boundary. If pinctl ever needs cross-process
  events (e.g. multiple runtimes), that is out of scope for v0.x.

## Graceful shutdown

On `SIGINT`/`SIGTERM` (as sent by `systemd stop`):

1. Stop accepting new HTTP connections; let in-flight REST requests finish
   with a bounded timeout.
2. Signal every pipeline scheduler to stop after its current tick completes
   (no pipeline is interrupted mid-tick).
3. Close WebSocket connections cleanly (clients see a normal close, not a
   dropped connection).
4. Close all board serial sessions.
5. Exit `0`.

The whole sequence runs under an overall deadline (a few seconds); anything
not finished by the deadline is forcibly closed so `systemd` doesn't have to
`SIGKILL` a hung process. There is no persistence step on shutdown beyond
what `store` already does synchronously on every definition change — pipeline
*definitions* are always durable; pipeline *runtime* state (node internal
state, e.g. a delay timer) is intentionally not persisted across restarts
(see hot-reload semantics in
[07-pipeline-engine.md](./07-pipeline-engine.md)).

## Resource budget on Raspberry Pi

pinctl targets comfortable operation on a Raspberry Pi 3/4-class board
(a few hundred MB RAM available to user processes, single-to-quad ARM
cores), alongside other things the Pi might be doing. Rough expectations for
a typical install (a handful of boards, tens of pipelines, tens of nodes
each):

- **Memory:** low tens of MB resident for the runtime itself (Go binary +
  embedded web assets + in-memory device/pipeline state). No unbounded
  growth expected — JSON definitions are small, and the engine holds only
  current tick values plus small per-node state (a float/bool/timestamp or
  two), not history, in the MVP.
- **CPU:** a single 20 ms-tick pipeline with tens of nodes should cost
  microseconds to low single-digit milliseconds of evaluation per tick — well
  under the tick budget. The dominant cost at scale is the number of
  concurrently ticking pipelines, not per-node complexity, since nodes are
  simple pure functions over small state (see
  [07-pipeline-engine.md](./07-pipeline-engine.md)).
- **I/O:** serial reads/writes are the main external I/O and are bounded by
  Firmata's 57600 baud link, not by the Pi's own capacity.
- Exact numbers are intentionally not promised as hard SLAs here — the
  design goal is "trivially fits on a Pi 3+", not a benchmarked ceiling; the
  per-pipeline tick stats (see
  [07-pipeline-engine.md](./07-pipeline-engine.md#debug-and-observability-hooks)) exist
  precisely so real overrun/CPU pressure is observable in the field instead
  of guessed at here.

## Error philosophy

The runtime's central reliability principle: **user configuration mistakes
must never crash the process.**

- **Pipelines fail independently.** A panic or error while evaluating one
  pipeline's tick is recovered and confined to that pipeline: it is marked
  `error`, an event is published, a log line is written, and every other
  pipeline keeps ticking normally. There is no shared mutable state between
  pipelines that a failure could corrupt.
- **Boards fail independently of each other and of pipelines.** One board
  disconnecting (unplugged, USB error) never affects another board's session;
  it only pauses pipelines that read/write devices on that board (see
  [07-pipeline-engine.md](./07-pipeline-engine.md)).
- **Bad definitions are skipped, not fatal.** A malformed or invalid
  device/pipeline JSON file found at startup or during a hot reload is
  logged and excluded from the running set; it does not stop other
  definitions from loading or abort startup.
- **The only fatal startup errors are: unreadable/invalid top-level config,
  and inability to bind the HTTP listener.** Both are things an operator
  must fix before pinctl can do anything useful, so failing fast with a
  clear message is more helpful than limping along.
- **Recovered panics are logged at `error` with a stack trace** and treated
  as a bug report, not a normal error path — they should not routinely
  happen, but the process must survive them.

## Versioning and compatibility for pipeline JSON

Persisted pipeline (and device) definitions carry a `schemaVersion` integer
field (see [09-data-model.md](./09-data-model.md) for the full JSON shape).

- **On load**, `store` reads `schemaVersion` before parsing the rest of the
  document. A version newer than the runtime understands is rejected with a
  clear "upgrade pinctl" error (definition skipped, not fatal to startup, per
  the error philosophy above). A version older than current is passed
  through a chain of in-process migration functions
  (`migrateV1toV2`, `migrateV2toV3`, ...), each a pure transform on the
  decoded JSON structure, until it reaches the current version.
- **Migrations are additive and forward-only.** There is no downgrade path;
  once a definition is migrated and saved, it is written back at the current
  `schemaVersion`. The original file is not overwritten until the migrated
  version has round-tripped through validation successfully.
- **Node catalog changes are versioned independently of the envelope.**
  Adding a new node type or an optional field to an existing node's config
  is not a `schemaVersion` bump; removing a field, changing a field's type,
  or changing edge-value semantics is. See
  [08-node-catalog.md](./08-node-catalog.md) for the node JSON schema this
  interacts with.
- **No automatic silent rewriting of user intent.** Migrations only change
  representation (e.g. renaming a field, restructuring nesting); they never
  change a pipeline's runtime behavior. If a migration cannot preserve
  behavior unambiguously, it is refused with an error rather than guessed.
