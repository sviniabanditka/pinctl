# pinctl — API

See [00-design-brief.md](./00-design-brief.md) for the fixed decisions this
doc builds on (REST + WebSocket, no auth in MVP), [03-architecture.md](./03-architecture.md)
for how `internal/api` sits between the browser and the rest of the runtime,
[09-data-model.md](./09-data-model.md) for the JSON shapes of boards/devices/
pipelines referenced below, [07-pipeline-engine.md](./07-pipeline-engine.md)
for the validation errors `POST /pipelines/validate` surfaces, and
[08-node-catalog.md](./08-node-catalog.md) for per-node config accepted
inside a pipeline body.

## Table of contents

- [General conventions](#general-conventions)
- [Boards](#boards)
- [Devices](#devices)
- [Pipelines](#pipelines)
- [System](#system)
- [WebSocket](#websocket-apiv1ws)
- [Static UI serving](#static-ui-serving)

## General conventions

- **Base path**: all REST endpoints are under `/api/v1`. The version prefix
  exists so a v2 API can be introduced later without breaking existing UI
  builds or third-party scripts against v1; nothing in v0.x plans to ship a
  v2 in parallel, but the prefix is free insurance.
- **Format**: JSON request/response bodies throughout (`Content-Type:
  application/json`). No form-encoded or multipart endpoints.
- **Auth**: none in the MVP — LAN tool, per the brief. Every endpoint below is
  reachable by anything that can reach the Pi's HTTP port. The roadmap's
  optional single-password + token would slot in as middleware in front of
  everything under `/api/*` without changing any handler.
- **Error envelope**: every non-2xx JSON response has the shape:

  ```json
  {
    "error": {
      "code": "validation_error",
      "message": "pipeline has a dangling edge",
      "details": {
        "field": "edges[2].to",
        "reason": "node \"n99\" does not exist"
      }
    }
  }
  ```

  `code` is a short, stable, machine-matchable string (see table below);
  `message` is a human-readable summary; `details` is optional and
  endpoint-specific (field-level validation errors, conflicting resource IDs,
  etc.).

- **Status codes**:

  | Code | Meaning | Used for |
  |---|---|---|
  | `200 OK` | success, body returned | GET, PATCH/PUT that return the updated resource, actions like enable/disable |
  | `201 Created` | resource created, body is the new resource | POST creating a board-independent resource (device, pipeline) |
  | `204 No Content` | success, no body | DELETE |
  | `400 Bad Request` | malformed request or validation failure (schema, referential integrity, DAG) | invalid body, see [09-data-model.md](./09-data-model.md#validation-rules) |
  | `404 Not Found` | referenced resource ID does not exist | GET/PATCH/DELETE on unknown `{id}` |
  | `409 Conflict` | request is well-formed but violates a state constraint | deleting a referenced device; manual write while an enabled pipeline drives the target device |
  | `500 Internal Server Error` | unexpected runtime fault | should not routinely happen; logged with a stack trace per [06-runtime.md](./06-runtime.md#error-philosophy) |

  `code` values used across the API: `not_found`, `validation_error`,
  `conflict`, `internal_error`.

## Boards

Board *metadata* (name, identity, port hint) is persisted per
[09-data-model.md](./09-data-model.md#board-object); live `status` is
runtime-only and composed into the response by `internal/api` on every read
from `board.Manager.Boards()` (see [03-architecture.md](./03-architecture.md#internalboard)).

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/boards` | List all known boards with live status. |
| `POST` | `/api/v1/boards/rescan` | Trigger a fresh scan of the serial allowlist for newly connected boards. |
| `PATCH` | `/api/v1/boards/{id}` | Rename a board (only `name` is editable). |

`GET /api/v1/boards` — response merges persisted metadata with live state:

```json
{
  "boards": [
    {
      "id": "brd_4kx9m2pqrs",
      "name": "Arduino Uno #1",
      "identity": { "kind": "usb-serial", "key": "VID_2341:PID_0043:SN_8503…" },
      "portHint": "/dev/ttyACM0",
      "status": "connected",
      "port": "/dev/ttyACM0",
      "capabilities": { "digitalPins": [0,1,2,3,4,5,6,7,8,9,10,11,12,13], "analogPins": [0,1,2,3,4,5] }
    },
    {
      "id": "brd_9zq3v7ab4c",
      "name": "Arduino Nano (greenhouse)",
      "identity": { "kind": "usb-serial", "key": "VID_2341:PID_0243:SN_55830…" },
      "portHint": "/dev/ttyACM1",
      "status": "disconnected",
      "port": null,
      "capabilities": null
    }
  ]
}
```

`status` is one of `connected`, `disconnected`, `reconnecting`, `error`
(matches the board state-change event in
[06-runtime.md](./06-runtime.md#internal-event-bus)). `port`/`capabilities`
are only populated while `connected`.

`POST /api/v1/boards/rescan` — no body. Triggers `board.Manager` to attempt a
fresh connect pass over the serial allowlist immediately (rather than waiting
for the next backoff interval) and creates a new `boards.json` entry (with an
auto-generated `name`) for any board found whose `identity.key` isn't already
known. Returns `202 Accepted` with no body — the scan is async; clients
observe results via `GET /boards` or the `boards` WebSocket topic.

`PATCH /api/v1/boards/{id}`:

```json
// request
{ "name": "Arduino Uno (workbench)" }
```

```json
// response 200
{
  "id": "brd_4kx9m2pqrs",
  "name": "Arduino Uno (workbench)",
  "identity": { "kind": "usb-serial", "key": "VID_2341:PID_0043:SN_8503…" },
  "portHint": "/dev/ttyACM0",
  "status": "connected"
}
```

## Devices

Standard CRUD over `devices.json` (see
[09-data-model.md](./09-data-model.md#device-object) for the full field
list), plus a manual-write escape hatch for bench testing.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/devices` | List all devices. |
| `POST` | `/api/v1/devices` | Create a device. |
| `GET` | `/api/v1/devices/{id}` | Get one device. |
| `PATCH` | `/api/v1/devices/{id}` | Update a device (name, pin, options — not `role` or `boardId`, see below). |
| `DELETE` | `/api/v1/devices/{id}` | Delete a device. `409` if referenced by any pipeline. |
| `POST` | `/api/v1/devices/{id}/write` | Manually write a value for testing. `409` if an enabled pipeline drives this device. |

`role` and `boardId` are immutable after creation (see
[09-data-model.md](./09-data-model.md#device-object): "changing a device's
role out from under pipelines that reference it is a delete-and-recreate,
not an edit"); `PATCH` requests that include either field return `400`.

`POST /api/v1/devices`:

```json
// request
{
  "name": "Porch LED",
  "boardId": "brd_4kx9m2pqrs",
  "pin": 13,
  "role": "digital-out",
  "options": { "initial": false, "onDisconnect": "safe-value", "safeValue": false }
}
```

```json
// response 201
{
  "id": "dev_k2n8h5wtxy",
  "name": "Porch LED",
  "boardId": "brd_4kx9m2pqrs",
  "pin": 13,
  "role": "digital-out",
  "options": { "initial": false, "onDisconnect": "safe-value", "safeValue": false },
  "createdAt": "2026-01-04T10:16:30Z",
  "updatedAt": "2026-01-04T10:16:30Z"
}
```

`DELETE /api/v1/devices/{id}` while referenced:

```json
// response 409
{
  "error": {
    "code": "conflict",
    "message": "device is referenced by 1 pipeline",
    "details": { "pipelineIds": ["pl_k2n8h5wtxy"] }
  }
}
```

### Manual pin control (`POST /api/v1/devices/{id}/write`)

Lets the UI (or a script) drive a `digital-out`/`pwm-out` device directly,
without a pipeline — useful for bench-testing wiring before building a
pipeline around it. Read-role devices (`digital-in`, `analog-in`) reject this
endpoint with `400` (nothing to write).

**Conflict rule**: a manual write is only accepted when **no enabled
pipeline** contains a `device-write` node targeting this `deviceId`. This
mirrors the engine's "every tick" write semantics in
[08-node-catalog.md](./08-node-catalog.md#device-write): if a pipeline is
live, it re-asserts its own value on the very next tick regardless, so a
manual write would either be immediately clobbered (confusing) or — worse —
race with the engine's own write to the same pin. Rather than accept a write
that's a no-op at best and a race at worst, the API rejects it outright and
tells the caller which pipeline to disable first.

```json
// request
{ "value": true }
```

```json
// response 200 (device is not driven by any enabled pipeline)
{ "id": "dev_k2n8h5wtxy", "value": true, "writtenAt": "2026-01-04T12:00:00Z" }
```

```json
// response 409 (an enabled pipeline drives this device)
{
  "error": {
    "code": "conflict",
    "message": "device is driven by an enabled pipeline",
    "details": { "pipelineId": "pl_k2n8h5wtxy", "pipelineName": "Porch light at dusk" }
  }
}
```

For `pwm-out`, `value` is an integer `0–255` (clamped, same as the engine's
own `device-write` handling); for `digital-out`, `value` is a bool.

## Pipelines

Standard CRUD over `pipelines/<id>.json` (see
[09-data-model.md](./09-data-model.md#pipeline-object)), plus enable/disable
actions and a dry-run validation endpoint the editor calls on every save
before committing.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/pipelines` | List all pipelines (summary — no `nodes`/`edges`, see below). |
| `POST` | `/api/v1/pipelines` | Create a pipeline. |
| `GET` | `/api/v1/pipelines/{id}` | Get one pipeline, full graph. |
| `PUT` | `/api/v1/pipelines/{id}` | Replace a pipeline's full definition (name, tickMs, nodes, edges). |
| `DELETE` | `/api/v1/pipelines/{id}` | Delete a pipeline. |
| `POST` | `/api/v1/pipelines/{id}/enable` | Enable (start its scheduler). |
| `POST` | `/api/v1/pipelines/{id}/disable` | Disable (stop its scheduler). |
| `POST` | `/api/v1/pipelines/validate` | Dry-run validate a pipeline body without persisting it. |

`PUT` (not `PATCH`) is deliberate: the editor always round-trips the entire
graph (Vue Flow's model is the full `nodes`/`edges` set), so there is no
meaningful partial-update shape — a `PUT` matches
`Store.SavePipeline`'s "validate the whole document, then atomically swap it
in" semantics from [09-data-model.md](./09-data-model.md#atomic-write-strategy)
exactly. `name` and `tickMs` alone can also go through the same `PUT` (send
the full current graph with just those fields changed) rather than adding a
separate partial-update path.

`GET /api/v1/pipelines` (summary form — omits `nodes`/`edges` to keep the
list view light):

```json
{
  "pipelines": [
    {
      "id": "pl_k2n8h5wtxy",
      "name": "Porch light at dusk",
      "enabled": true,
      "tickMs": 20,
      "nodeCount": 3,
      "status": "running"
    }
  ]
}
```

`status` here is runtime, from `Engine.Status` (see
[03-architecture.md](./03-architecture.md#internalengine)): one of
`running`, `paused` (board offline), `error`, or `stopped` (disabled).

`POST /api/v1/pipelines/validate` — dry-run: runs exactly the validation
`SavePipeline` would run (schema, DAG, referential integrity — see
[09-data-model.md](./09-data-model.md#validation-rules) and
[07-pipeline-engine.md](./07-pipeline-engine.md)) against the request body,
**without writing anything to disk or touching the engine**. This is what
the Vue Flow editor calls continuously (e.g. debounced on every graph edit)
to show inline errors before the user hits save.

```json
// request: same body shape as PUT /pipelines/{id}
{
  "name": "Porch light at dusk",
  "tickMs": 20,
  "nodes": [
    { "id": "n1", "type": "device-read", "config": { "deviceId": "dev_9zq3v7ab4c" }, "position": { "x": 40, "y": 120 } },
    { "id": "n5", "type": "compare", "config": { "op": "lt", "threshold": 300 }, "position": { "x": 240, "y": 120 } }
  ],
  "edges": [
    { "from": "n1.out", "to": "n5.in" },
    { "from": "n5.out", "to": "n99.in" }
  ]
}
```

```json
// response 200 — validation ran; result may still be invalid
{
  "valid": false,
  "errors": [
    {
      "field": "edges[1].to",
      "code": "dangling_edge",
      "message": "node \"n99\" does not exist"
    }
  ],
  "warnings": [
    {
      "field": "nodes[3].config.deviceId",
      "code": "multiple_writers",
      "message": "device \"dev_k2n8h5wtxy\" is targeted by more than one device-write node"
    }
  ]
}
```

`POST /pipelines/validate` itself returns `200` whenever validation *ran*
(even if the pipeline is invalid) — `valid: false` plus the `errors` array
*is* the result. A `400` from this endpoint means the request body itself
was unparsable JSON, not that the pipeline failed validation.

`warnings` uses the same element shape as `errors` (`field`, `code`,
`message`) but is **non-blocking** — a pipeline with only warnings and no
errors is `valid: true` and saveable. It surfaces conditions that are legal
but likely unintended, e.g. two `device-write` nodes targeting the same
`deviceId` (allowed per [08-node-catalog.md](./08-node-catalog.md#device-write)
and [09-data-model.md](./09-data-model.md#validation-rules)) — the editor
renders these as inline warnings rather than save-blocking errors. `warnings`
may be an empty array or omitted when there are none. `PUT /pipelines/{id}`
runs the same validation before persisting and returns `warnings` alongside
the saved pipeline for the same reason — a save is not rejected for
warnings-only results, but the editor still needs to show them.

`POST /api/v1/pipelines/{id}/enable` / `.../disable` — no body, response is
the updated summary:

```json
{ "id": "pl_k2n8h5wtxy", "enabled": true, "status": "running" }
```

Disabling a pipeline that's already disabled (or enabling one already
enabled) is idempotent — `200` with the current state, not an error.

## System

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/system` | Version, uptime, config summary. |

```json
{
  "version": "0.3.0",
  "commit": "a1b2c3d",
  "uptimeSeconds": 143207,
  "goVersion": "go1.23.4",
  "os": "linux/arm64",
  "config": {
    "httpAddr": ":8080",
    "dataDir": "/var/lib/pinctl",
    "serialBaud": 57600,
    "serialAllowlist": ["/dev/ttyACM0", "/dev/ttyUSB0"],
    "logLevel": "info"
  },
  "counts": { "boards": 2, "devices": 7, "pipelines": 3, "pipelinesEnabled": 2 }
}
```

`config` is a read-only summary of the effective `/etc/pinctl/config.yaml`
(see [06-runtime.md](./06-runtime.md#config-schema)) for support/debugging —
there is no endpoint to change config at runtime; config is file-only and
requires a restart, consistent with "invalid/missing config is the one place
startup is allowed to abort" from the runtime doc.

## WebSocket `/api/v1/ws`

A single multiplexed connection per client — one socket handles board status,
device values, pipeline state, and per-pipeline editor debug streams, all
distinguished by `topic`. This avoids the browser juggling several sockets
and lets `internal/api` apply one connection-level policy (slow-consumer
drop, close on shutdown — see
[03-architecture.md](./03-architecture.md#c-live-monitoring-via-websocket)).

### Message envelope

Every frame, either direction, is:

```json
{ "type": "...", "topic": "...", "data": { } }
```

| `type` | Direction | Meaning |
|---|---|---|
| `subscribe` | client → server | Start receiving events for `topic`. |
| `unsubscribe` | client → server | Stop receiving events for `topic`. |
| `ping` | client → server | Liveness check. |
| `pong` | server → client | Reply to `ping`. |
| `event` | server → client | A topic update; `data` shape depends on `topic`. |
| `error` | server → client | Something about a `subscribe`/`unsubscribe` request was invalid (e.g. unknown `pipelineId`). |

Aside from `subscribe`/`unsubscribe`/`ping`, the client never sends anything
— all state changes (enable a pipeline, write a device, edit a pipeline) go
through REST, per the "bus carries state, not commands" design in
[06-runtime.md](./06-runtime.md#internal-event-bus). The WebSocket is a
read-only firehose the client tunes with `subscribe`/`unsubscribe`.

### Topics

| Topic | Payload (`data`) | Notes |
|---|---|---|
| `boards` | `{ boardId, status, port }` | Board connect/disconnect/error, mirrors `board.Manager.Watch`. |
| `devices` | `{ deviceId, value, timestamp }` | Live pin values. **Throttled** — coalesced to at most one update per device per ~200ms even if the underlying pin changes every tick, so an idle-but-noisy analog input doesn't flood slow clients (matches the "engine drops snapshots for a subscriber that isn't keeping up" policy in [03-architecture.md](./03-architecture.md#concurrency-model)). |
| `pipelines` | `{ pipelineId, status, detail }` | Pipeline state changes: `running`/`paused`/`error`/`stopped`, mirrors `Engine`'s pipeline state-change event. |
| `pipeline:{id}` | `{ pipelineId, tick, nodes: { [nodeId]: value }, log?: [{ nodeId, label, value, timestamp }] }` | Per-tick node value snapshot for one pipeline (`Engine.Subscribe`, see [03-architecture.md](./03-architecture.md#b-tick-execution)) — this is what the Vue Flow editor subscribes to while debugging a pipeline open in the canvas, so it can show live values on each edge. The optional `log` array carries entries emitted by any `log` node ([08-node-catalog.md](./08-node-catalog.md#log)) that ticked this snapshot; omitted when no `log` node produced an entry that tick. Subscribing requires `pipelineId` in the `subscribe` message; **not** throttled (bounded instead by drop-if-slow-consumer, since the whole point is per-tick fidelity while actively debugging). |

### Examples

Client subscribes to board and device topics on connect:

```json
{ "type": "subscribe", "topic": "boards", "data": {} }
{ "type": "subscribe", "topic": "devices", "data": {} }
```

Client opens the editor for a pipeline and subscribes to its debug stream:

```json
{ "type": "subscribe", "topic": "pipeline:pl_k2n8h5wtxy", "data": {} }
```

Server pushes a device value update:

```json
{
  "type": "event",
  "topic": "devices",
  "data": { "deviceId": "dev_9zq3v7ab4c", "value": 287, "timestamp": "2026-01-04T12:00:00.120Z" }
}
```

Server pushes a board disconnect:

```json
{
  "type": "event",
  "topic": "boards",
  "data": { "boardId": "brd_9zq3v7ab4c", "status": "disconnected", "port": null }
}
```

Server pushes a per-tick debug snapshot while the editor has the pipeline
open:

```json
{
  "type": "event",
  "topic": "pipeline:pl_k2n8h5wtxy",
  "data": {
    "pipelineId": "pl_k2n8h5wtxy",
    "tick": 48213,
    "nodes": {
      "n1": 287,
      "n5": true,
      "n14": true
    }
  }
}
```

Same topic, on a tick where a `log` node ([08-node-catalog.md](./08-node-catalog.md#log))
also produced an entry:

```json
{
  "type": "event",
  "topic": "pipeline:pl_k2n8h5wtxy",
  "data": {
    "pipelineId": "pl_k2n8h5wtxy",
    "tick": 48214,
    "nodes": { "n1": 288, "n5": true, "n14": true, "n15": 288 },
    "log": [
      { "nodeId": "n15", "label": "ldr-raw", "value": 288, "timestamp": "2026-01-04T12:00:00.140Z" }
    ]
  }
}
```

Client unsubscribes when the editor tab is closed:

```json
{ "type": "unsubscribe", "topic": "pipeline:pl_k2n8h5wtxy", "data": {} }
```

Liveness:

```json
{ "type": "ping", "topic": "", "data": {} }
```

```json
{ "type": "pong", "topic": "", "data": {} }
```

Invalid subscribe (unknown pipeline):

```json
{
  "type": "error",
  "topic": "pipeline:pl_doesnotexist",
  "data": { "code": "not_found", "message": "pipeline \"pl_doesnotexist\" does not exist" }
}
```

On shutdown, the server closes every WebSocket cleanly (normal close frame,
not a dropped connection) as part of the graceful shutdown sequence in
[06-runtime.md](./06-runtime.md#graceful-shutdown).

## Static UI serving

Every request whose path does **not** start with `/api/` is served from the
embedded `web/dist` `embed.FS` (see
[03-architecture.md](./03-architecture.md#adr-4-embedded-ui-embedfs-not-a-separately-served-frontend)):

- A request matching a real file in the embedded build (`/assets/app.a1b2.js`,
  `/favicon.ico`, …) is served as-is with the appropriate `Content-Type`.
- Any other non-`/api/*` path (e.g. `/pipelines/pl_k2n8h5wtxy/edit`, a
  client-side Vue Router route) falls back to `index.html` — history-mode
  SPA routing, so deep links and browser refreshes on an editor URL work
  without the server needing to know Vue Router's route table.
- `/api/*` paths that don't match a defined endpoint return the standard
  JSON error envelope with `404`, never the SPA fallback — API 404s must
  stay JSON so client error handling doesn't have to sniff `Content-Type`.
