# pinctl — Data Model

See [00-design-brief.md](./00-design-brief.md) for the fixed decisions this
doc builds on (JSON files, no database, atomic writes), [03-architecture.md](./03-architecture.md)
for how `internal/store` fits into the system, [06-runtime.md](./06-runtime.md)
for config/startup loading and schema-version migration policy,
[08-node-catalog.md](./08-node-catalog.md) for the exact node/edge shapes
referenced inside a pipeline, and [10-api.md](./10-api.md) for how these
objects are exposed over REST/WebSocket.

## Table of contents

- [On-disk layout](#on-disk-layout)
- [Atomic write strategy](#atomic-write-strategy)
- [ID strategy](#id-strategy)
- [Board object](#board-object)
- [Device object](#device-object)
- [Pipeline object](#pipeline-object)
- [Versioning and migration](#versioning-and-migration)
- [Validation rules](#validation-rules)

## On-disk layout

Per the brief, there is no database: everything lives as JSON under a single
data directory, `/var/lib/pinctl/` in production (`~/.pinctl/` in dev, matching
`data_dir` in [06-runtime.md](./06-runtime.md#config-schema)).

```
/var/lib/pinctl/               # $data_dir
  boards.json                  # collection: known boards (identity, name, port hint)
  devices.json                 # collection: all devices, across all boards
  pipelines/
    pl_4kx9m2pq.json            # one file per pipeline
    pl_9zq3v7ab.json
  .tmp/                        # scratch dir for write-temp-then-rename (same filesystem)
```

Design rationale for the split:

- **`boards.json` and `devices.json` are single collection files.** Expected
  volume is small (tens of boards/devices per install, per
  [03-architecture.md](./03-architecture.md#adr-3-json-files-not-a-database)),
  they change rarely (only through explicit CRUD, never on a tick), and
  keeping them as one file each makes "list everything" — the only query
  `internal/store` needs to support — a single read with no directory scan.
- **`pipelines/` is one file per pipeline**, keyed by ID. Pipelines are edited
  far more often than boards/devices (the Vue Flow editor autosaves as a user
  drags nodes around) and are the unit of hot-reload
  (`Engine.Reload(ctx, p)` takes one pipeline). One file per pipeline means an
  editor save only ever rewrites the pipeline being edited — never a shared
  collection file other pipelines also live in — and keeps diffs (this repo
  layout is designed to be git-diffable per ADR-3) scoped to what actually
  changed.
- **`.tmp/`** is a fixed scratch directory on the same filesystem as the
  targets it write-temp-then-renames into, so the rename in
  [Atomic write strategy](#atomic-write-strategy) is always same-filesystem
  (a cross-filesystem rename is not atomic). `internal/store` creates it on
  first run alongside the data directory itself.

Nothing under `/var/lib/pinctl/` is runtime/status data — connection state,
last-known pin values, and pipeline tick stats are never written here (see
[Board object](#board-object) and [Validation rules](#validation-rules)); they
live only in memory and are served fresh over the API.

## Atomic write strategy

Every write `internal/store` performs — creating, updating, or deleting a
board, device, or pipeline — follows the same sequence, so a crash or power
loss mid-write never leaves a torn/partial file on disk:

1. Marshal the full new document (the whole `boards.json`/`devices.json`
   collection, or the single pipeline file being changed) to JSON.
2. Write it to a temp file in `.tmp/` (e.g. `.tmp/boards.json.$pid-$rand`).
3. `fsync` the temp file.
4. `rename()` the temp file over the real target path. POSIX guarantees this
   rename is atomic — readers always see either the old file or the new one,
   never a mix.
5. `fsync` the containing directory (so the rename itself is durable, not
   just the file contents).

Deletes follow the same shape for collection files (rewrite `devices.json`
without the deleted entry, write-temp-then-rename) and are a plain `os.Remove`
for a pipeline's own file (removing a file is already atomic from readers'
perspective — there's no partial-delete state to protect against).

`internal/store` never does a partial/in-place edit of an existing file; every
save is "build the full intended document, then atomically swap it in." This
is also why `SavePipeline` validates the *entire* pipeline (schema, DAG,
referential integrity — see [Validation rules](#validation-rules)) before step
2 above: nothing invalid is ever written to a temp file in the first place, so
there is no invalid-but-durable state to clean up after.

## ID strategy

**Short random IDs, not slugs derived from names.**

- Format: `<prefix>_<10 lowercase base32 (Crockford) chars>`, e.g.
  `brd_4kx9m2pqrs`, `dev_9zq3v7ab4c`, `pl_k2n8h5wtxy`. Generated from
  `crypto/rand` at creation time.
- Prefixes: `brd_` (board), `dev_` (device), `pl_` (pipeline). The prefix is
  purely a human-debugging aid (grep-able, tells you what kind of ID you're
  looking at in a log line) — parsers must not rely on it for validation
  beyond a sanity check.
- IDs are **immutable for the lifetime of the object** and are what every
  cross-reference uses: `Device.boardId`, `device-read`/`device-write`
  node `config.deviceId` (see [08-node-catalog.md](./08-node-catalog.md)).

Why not slugs (e.g. `led-porch`, derived from `name`)? Names are
user-editable at any time (`PATCH /devices/{id}` renames a device, see
[10-api.md](./10-api.md)) and pinctl does not want a rename to either (a)
change the ID and silently break every pipeline referencing it, or (b) be
blocked because the "slug" is load-bearing. Decoupling the stable reference
key from the display name avoids that whole class of problem, at the minor
cost of IDs not being human-readable — an acceptable trade for an install
with tens of objects, not thousands, especially since the UI always resolves
IDs to names before showing them to a user.

**Node and edge IDs** (inside a pipeline document) are a separate, narrower
scheme: they only need to be unique *within that one pipeline*, not globally,
since nothing outside the pipeline ever references them. The editor
(Vue Flow) assigns short sequential-looking strings (`n1`, `n2`, …; see the
examples throughout [08-node-catalog.md](./08-node-catalog.md)) at node
creation time; `internal/store` only requires uniqueness within
`pipeline.nodes`, not any particular format. Edges do not need their own
`id` field at all — an edge is fully identified by its `from`/`to` endpoints
(see [Pipeline object](#pipeline-object)), and duplicate edges between the
same two ports are a validation error, so `(from, to)` is already a unique
key.

## Board object

A board is a connected (or previously-seen) microcontroller. **Only identity
and user-facing metadata are persisted** — live connection status, pin
capabilities discovered over Firmata, and cached pin values are runtime-only
state owned by `internal/board` and never written to `boards.json` (see
[03-architecture.md](./03-architecture.md#internalboard)); the API composes
persisted board metadata with live status on every `GET /boards` call instead
(see [10-api.md](./10-api.md)).

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | `brd_…`, immutable, see [ID strategy](#id-strategy). |
| `name` | string | yes | User-editable display name, e.g. `"Arduino Uno #1"`. |
| `identity` | object | yes | Stable hardware identity key used to re-associate this board entry with a physical device across reconnects/reboots, independent of which `/dev/ttyACM*` path it happens to enumerate as. Kept intentionally generic here — the exact key derivation (USB VID/PID + serial number, or a fallback) is [05-serial-protocol.md](./05-serial-protocol.md)'s concern; this doc only fixes the *shape*. |
| `identity.kind` | string | yes | Identity scheme, e.g. `"usb-serial"`. Forward-compatible with non-USB identity schemes later. |
| `identity.key` | string | yes | Opaque, scheme-specific stable key, e.g. a USB VID/PID + serial-number composite. Two boards must never share a `key`. |
| `portHint` | string | no | Last-known serial device path, e.g. `"/dev/ttyACM0"`. A *hint* only — `internal/board` uses `identity.key` as the source of truth and re-resolves the actual path at connect time (device paths are not stable across replug/reboot); `portHint` exists so the UI can show something useful and so first-time matching after a fresh scan has a tie-breaker. |
| `createdAt` | string (RFC 3339) | yes | Set once, on first discovery/creation. |
| `updatedAt` | string (RFC 3339) | yes | Bumped on any persisted-field change (rename, identity/port-hint update after a rescan). |

Explicitly **not** part of the persisted object: `status`
(`connected`/`disconnected`/`error`), pin capabilities, and any cached pin
value — these are runtime-only, sourced live from `board.Manager.Boards()`
(see [03-architecture.md](./03-architecture.md#internalboard)) and attached
by `internal/api` when serving `GET /boards`.

`boards.json` (annotated example):

```json
{
  "schemaVersion": 1,
  "boards": [
    {
      "id": "brd_4kx9m2pqrs",
      "name": "Arduino Uno #1",
      "identity": {
        "kind": "usb-serial",
        "key": "VID_2341:PID_0043:SN_85033313938351E082D1"
      },
      "portHint": "/dev/ttyACM0",
      "createdAt": "2026-01-04T10:12:03Z",
      "updatedAt": "2026-01-04T10:12:03Z"
    }
  ]
}
```

## Device object

A user-named binding of a role to a specific pin on a specific board (see the
core domain model in [00-design-brief.md](./00-design-brief.md)).

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | `dev_…`, immutable. |
| `name` | string | yes | User-editable display name, e.g. `"LDR on A0"`. |
| `boardId` | string | yes | Must reference an existing entry in `boards.json` (see [Validation rules](#validation-rules)). |
| `pin` | integer | yes | Pin/channel number, in the numbering `internal/board` exposes for that role (digital pin number for `digital-in`/`digital-out`/`pwm-out`; analog channel index for `analog-in`, per Firmata's analog addressing — see [05-serial-protocol.md](./05-serial-protocol.md)). |
| `role` | string | yes | One of `digital-in`, `digital-out`, `analog-in`, `pwm-out` (v2: `servo`, `i2c`, per the brief). Immutable after creation — changing a device's role out from under pipelines that reference it is a delete-and-recreate, not an edit, so referencing pipelines fail validation predictably instead of silently changing type. |
| `options` | object | no | Per-role config, see table below. Defaults apply for any omitted field. |
| `createdAt` / `updatedAt` | string (RFC 3339) | yes | Same semantics as on `Board`. |

`options` by role:

| Role | Field | Type | Default | Notes |
|---|---|---|---|---|
| `digital-in` | `pullup` | bool | `false` | Enables the pin's internal pull-up resistor. |
| `analog-in` | *(none)* | — | — | No configurable options in MVP. |
| `digital-out` | `initial` | bool | `false` | Value written to the pin as soon as the board session comes up (startup or reconnect), before any pipeline tick has run. |
| `digital-out` | `onDisconnect` | `"hold"` \| `"safe-value"` | `"safe-value"` | Behavior applied the moment the board *reconnects* after having been offline (pinctl cannot write to a board that isn't there — this governs the reconnect moment, not the outage itself). `"hold"`: do nothing special; normal pipeline-driven writes simply resume on the next tick. `"safe-value"`: write `safeValue` immediately on reconnect, before any pipeline write for this device is applied that tick. |
| `digital-out` | `safeValue` | bool | `false` | Used only when `onDisconnect: "safe-value"`. |
| `pwm-out` | `initial` | integer `0–255` | `0` | Same semantics as `digital-out.initial`. |
| `pwm-out` | `onDisconnect` | `"hold"` \| `"safe-value"` | `"safe-value"` | Same semantics as `digital-out.onDisconnect`. |
| `pwm-out` | `safeValue` | integer `0–255` | `0` | Used only when `onDisconnect: "safe-value"`. |

`devices.json` (annotated example):

```json
{
  "schemaVersion": 1,
  "devices": [
    {
      "id": "dev_9zq3v7ab4c",
      "name": "LDR on A0",
      "boardId": "brd_4kx9m2pqrs",
      "pin": 0,
      "role": "analog-in",
      "options": {},
      "createdAt": "2026-01-04T10:15:00Z",
      "updatedAt": "2026-01-04T10:15:00Z"
    },
    {
      "id": "dev_k2n8h5wtxy",
      "name": "Porch LED",
      "boardId": "brd_4kx9m2pqrs",
      "pin": 13,
      "role": "digital-out",
      "options": {
        "initial": false,
        "onDisconnect": "safe-value",
        "safeValue": false
      },
      "createdAt": "2026-01-04T10:16:30Z",
      "updatedAt": "2026-01-04T10:16:30Z"
    }
  ]
}
```

## Pipeline object

A named dataflow graph (see [07-pipeline-engine.md](./07-pipeline-engine.md)
for evaluation semantics and [08-node-catalog.md](./08-node-catalog.md) for
the full node catalog and per-node `config` schemas — this doc only fixes the
envelope). One file per pipeline: `pipelines/<id>.json`.

| Field | Type | Required | Notes |
|---|---|---|---|
| `schemaVersion` | integer | yes | See [Versioning and migration](#versioning-and-migration). |
| `id` | string | yes | `pl_…`, immutable, also the filename stem. |
| `name` | string | yes | User-editable display name. |
| `enabled` | bool | yes | Persisted, not runtime-only — re-enabled automatically on restart per [03-architecture.md](./03-architecture.md#runtime-restart). |
| `tickMs` | integer | yes | This pipeline's evaluation period, default `20` (per the brief). Nodes with internal timing (`timer`, `delay`, `debounce`) validate against this — see [08-node-catalog.md](./08-node-catalog.md). |
| `nodes` | array of Node | yes | May be empty (an empty pipeline is valid but pointless; enabling it is allowed). |
| `edges` | array of Edge | yes | May be empty. |
| `createdAt` / `updatedAt` | string (RFC 3339) | yes | Same semantics as `Board`/`Device`. |

**Node** (element of `nodes`):

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Unique within this pipeline only, see [ID strategy](#id-strategy). |
| `type` | string | yes | One of the node catalog's type names (`device-read`, `compare`, `device-write`, …). |
| `config` | object | yes | Type-specific; may be `{}` for nodes with no config (e.g. `not`). Full per-type schema is [08-node-catalog.md](./08-node-catalog.md). |
| `position` | object `{x, y}` | yes | Editor canvas coordinates (Vue Flow). Purely presentational — never read by the engine — but persisted so the graph's layout survives a reload. |

**Edge** (element of `edges`):

| Field | Type | Required | Notes |
|---|---|---|---|
| `from` | string | yes | `"<nodeId>.<port>"`, e.g. `"n1.out"`. Must reference an existing node and one of its declared output ports. |
| `to` | string | yes | `"<nodeId>.<port>"`, e.g. `"n5.in"`. Must reference an existing node and one of its declared input ports. |

Full annotated example — the README's "turn on an LED when it gets dark"
pipeline:

```json
{
  "schemaVersion": 1,
  "id": "pl_k2n8h5wtxy",
  "name": "Porch light at dusk",
  "enabled": true,
  "tickMs": 20,
  "nodes": [
    {
      "id": "n1",
      "type": "device-read",
      "config": { "deviceId": "dev_9zq3v7ab4c" },
      "position": { "x": 40, "y": 120 }
    },
    {
      "id": "n5",
      "type": "compare",
      "config": { "op": "lt", "threshold": 300 },
      "position": { "x": 240, "y": 120 }
    },
    {
      "id": "n14",
      "type": "device-write",
      "config": { "deviceId": "dev_k2n8h5wtxy" },
      "position": { "x": 440, "y": 120 }
    }
  ],
  "edges": [
    { "from": "n1.out", "to": "n5.in" },
    { "from": "n5.out", "to": "n14.in" }
  ],
  "createdAt": "2026-01-04T10:20:00Z",
  "updatedAt": "2026-01-04T11:02:41Z"
}
```

## Versioning and migration

Every persisted document — `boards.json`, `devices.json`, and each pipeline
file — carries a top-level `schemaVersion` integer, per
[06-runtime.md](./06-runtime.md#versioning-and-compatibility-for-pipeline-json).
This doc fixes the same policy for boards and devices as that doc fixes for
pipelines:

- **On load**, `internal/store` reads `schemaVersion` before parsing the rest
  of the document.
  - Newer than the runtime understands → rejected with a clear
    "upgrade pinctl" error; the document is skipped, not fatal to startup
    (see the error philosophy in [06-runtime.md](./06-runtime.md#error-philosophy)).
  - Older than current → passed through an in-process migration chain
    (`migrateV1toV2`, `migrateV2toV3`, …), each a pure transform on the
    decoded JSON, until it reaches the current version. The migrated result
    is re-validated before being accepted, and only written back to disk
    (at the current `schemaVersion`) once it round-trips successfully — the
    original file is untouched until then.
- **Migrations are additive and forward-only.** No downgrade path.
- **v0.x migration policy is deliberately loose, per the brief's "no
  guarantees below 1.0" posture**: breaking changes to the on-disk shape are
  allowed between v0.x releases without a compatibility guarantee, but every
  such change still ships a migration function in the chain above rather
  than silently failing to load older files — "best-effort forward
  migration, not a hard compatibility promise" is the v0.x bar. Once pinctl
  reaches 1.0, `schemaVersion` bumps and migrations become a real
  compatibility commitment (no more silently-allowed breaking changes).
- **Node catalog changes are versioned independently of the pipeline
  envelope** (adding an optional node/config field is not a `schemaVersion`
  bump; removing/retyping a field or changing edge-value semantics is) — see
  [06-runtime.md](./06-runtime.md#versioning-and-compatibility-for-pipeline-json)
  and [08-node-catalog.md](./08-node-catalog.md).
- **No silent rewriting of user intent.** A migration only changes
  representation (renaming/restructuring fields); if it cannot preserve
  behavior unambiguously, it refuses with an error instead of guessing.

## Validation rules

`internal/store` is the single place these are enforced — before anything is
written to disk (see [Atomic write strategy](#atomic-write-strategy)) and
before a pipeline is ever handed to `internal/engine`, per
[03-architecture.md](./03-architecture.md#a-editor-saves-a-pipeline--validation--store--engine-hot-reload).
The engine trusts everything it receives via `Load`/`Reload` completely — it
never re-validates a graph. Full per-node semantic rules (unconnected
required inputs, cycles, tick/timing constraints) are
[07-pipeline-engine.md](./07-pipeline-engine.md)'s domain; this list is the
subset that is about **data-model referential integrity**, i.e. cross-object
references staying consistent:

| Rule | Enforced on | Failure |
|---|---|---|
| `device.boardId` references an existing board | device create/update | `400` with field error |
| No two devices bind the same `(boardId, pin)` pair (FR-DEVICE-4 in [02-requirements.md](./02-requirements.md)) | device create/update | `409 Conflict` |
| `edge.from` / `edge.to` reference an existing `node.id` and one of that node's declared ports | pipeline save | `400` with field-level error, see [10-api.md](./10-api.md) |
| No duplicate edge between the same `(from, to)` pair | pipeline save | `400` |
| `device-read`/`device-write` node's `config.deviceId` references an existing device | pipeline save | `400` |
| Referenced device's `role` is compatible with the node type (`device-read` needs `digital-in`/`analog-in`; `device-write` needs `digital-out`/`pwm-out`) | pipeline save, and re-checked at `Engine.Reload`/`Load` time (a device's role cannot change post-creation, but the *reference* can go stale if the device was deleted and a same-named one recreated — see next rule) | `400` at save; pipeline kept in previous-good state with an `error` status at reload time, per [06-runtime.md](./06-runtime.md#error-philosophy) |
| A device cannot be deleted while any pipeline references its `id` | device delete | `409 Conflict`, see [10-api.md](./10-api.md) |
| A board is **not** protected the same way: deleting a board does not cascade-delete its devices. Devices left pointing at a missing `boardId` are not fatal — they simply resolve to "board offline" (same code path as a board being unplugged) and any pipeline reading/writing them is paused, not deleted | board delete, device/pipeline load | none (warning surfaced in the UI) |
| No cycles in `edges` (topological sort must succeed) | pipeline save | `400`, see [07-pipeline-engine.md](./07-pipeline-engine.md) |
| No dangling/unconnected required input port | pipeline save | `400`, see [08-node-catalog.md](./08-node-catalog.md) |
| Two `device-write` nodes may target the same `deviceId` (same or different pipeline) | pipeline save | allowed, UI warning only — not an error, see [08-node-catalog.md](./08-node-catalog.md) |

Asymmetry worth calling out: device→board references are **soft** (a missing
board degrades gracefully, matching how a board can be physically unplugged
at any moment regardless of what's in `boards.json`), while
pipeline→device references are **hard** at the API boundary (deleting a
referenced device is rejected outright) but **soft** at load/runtime (a
pipeline whose device was deleted out from under it via a schema
inconsistency, e.g. hand-edited files, fails to reload but does not corrupt
other pipelines) — consistent with the "user configuration mistakes must
never crash the process" principle in
[06-runtime.md](./06-runtime.md#error-philosophy).
