# pinctl — Web UI

This document specifies the Vue 3 web UI: screens, the Vue Flow pipeline
editor, live debugging, state management, and client-side validation. It
follows the decisions fixed in [00-design-brief.md](./00-design-brief.md); if
anything here appears to contradict that file, the brief wins.

Related docs: [03-architecture.md](./03-architecture.md) (where `web/` sits in
the system, ADR-4 on embedding), [08-node-catalog.md](./08-node-catalog.md)
(the node set and config schemas the editor renders), [09-data-model.md](./09-data-model.md)
(pipeline/device/board JSON shapes, node `position{x,y}`), [10-api.md](./10-api.md)
(the REST/WebSocket contract this UI is a pure client of).

## Table of contents

- [Stack and build](#stack-and-build)
- [Screens and routing](#screens-and-routing)
- [Pipeline editor](#pipeline-editor)
- [Client-side validation mirroring](#client-side-validation-mirroring)
- [Live debugging](#live-debugging)
- [Pinia stores](#pinia-stores)
- [UX details](#ux-details)
- [Component tree (pipeline editor screen)](#component-tree-pipeline-editor-screen)
- [Out of scope for MVP](#out-of-scope-for-mvp)

## Stack and build

Per the brief: **Vue 3 + Vite + Pinia + Vue Flow**, written in TypeScript.

| Concern | Choice | Notes |
|---|---|---|
| Framework | Vue 3, `<script setup>` SFCs | |
| Build tool | Vite | |
| State | Pinia | one store per domain, see [Pinia stores](#pinia-stores) |
| Node editor canvas | Vue Flow (`@vue-flow/core`) | custom node/edge renderers per [Pipeline editor](#pipeline-editor) |
| Language | TypeScript throughout | types for the pipeline/device/board JSON shapes are hand-kept in sync with [09-data-model.md](./09-data-model.md) — no server-driven codegen in MVP (no OpenAPI spec exists yet; a future doc revision may add one) |
| Routing | vue-router, **history mode** | matches the server's SPA fallback-to-`index.html` behavior in [10-api.md](./10-api.md#static-ui-serving) — a hash-mode router would work too but history mode gives cleaner, shareable deep links (`/pipelines/pl_.../edit`) and the server already supports it |
| HTTP client | `fetch` wrapped in a thin `api/` module | one function per REST endpoint in [10-api.md](./10-api.md), returning typed results or throwing a typed `ApiError` built from the error envelope |
| WebSocket client | thin wrapper around native `WebSocket`, owned by the `ws` store | see [Pinia stores](#pinia-stores) |

### Production build

`web/` builds to static assets (`web/dist/`) via `vite build`. `cmd/pinctl`
embeds `web/dist` via `embed.FS` and `internal/api` serves it for every
non-`/api/*` path (see [03-architecture.md](./03-architecture.md#adr-4-embedded-ui-embedfs-not-a-separately-served-frontend)).
There is no runtime configuration step for the built UI — it always talks to
`/api/v1` and `/api/v1/ws` on the same origin it was served from, so no
build-time or run-time "API base URL" setting is needed.

### Dev mode

`vite dev` runs a local dev server (default `:5173`) with hot module reload,
proxying `/api` (REST and the `/api/v1/ws` WebSocket upgrade) to the Go
runtime, which is run separately (`go run ./cmd/pinctl` against `~/.pinctl/`).
This is a `vite.config.ts` `server.proxy` entry only — the Go binary never
needs to know it's being developed against; from its point of view it's just
another `/api/*` client. There is no dev-only API surface.

## Screens and routing

| Route | Screen | Purpose |
|---|---|---|
| `/` | Dashboard | At-a-glance: boards, devices, pipeline statuses |
| `/boards` | Boards | List + rescan + rename |
| `/devices` | Devices | CRUD, manual write for bench testing |
| `/pipelines` | Pipeline list | List, create, delete, enable/disable |
| `/pipelines/:id/edit` | Pipeline editor | Vue Flow canvas + config panel + live debug |
| `/settings` | Settings / System | Read-only config summary, version/uptime |

Any unmatched client route (or a browser refresh on a deep link like
`/pipelines/pl_k2n8h5wtxy/edit`) resolves because the server falls back to
`index.html` for all non-`/api/*` paths — the router then resolves the route
client-side, per [10-api.md](./10-api.md#static-ui-serving). No 404 route is
needed for anything under the app's own path space; an actual unknown route
still renders a small in-app "not found" view rather than a server 404.

### Dashboard (`/`)

The landing screen. Three panels, each a thin summary view backed by the
same Pinia stores the dedicated screens use (no separate dashboard API calls):

- **Boards** — chips/cards per board: name, `status` (`connected` /
  `disconnected` / `reconnecting` / `error`), port. Click-through to `/boards`.
- **Devices** — a compact list with live value where available (from the
  `devices` WS topic, throttled ~200ms per [10-api.md](./10-api.md#topics)).
  Click-through to `/devices`.
- **Pipelines** — name, `enabled` state, runtime `status`
  (`running`/`paused`/`error`/`stopped`) from `GET /pipelines`. Click-through
  to the editor.

Empty state (nothing configured yet) replaces all three panels with a single
onboarding card: "Add a board" → "Add a device" → "Create a pipeline", each
step linking to the relevant screen. This mirrors the README's worked example
(LDR + LED) as the suggested first pipeline name/shape, but does not
scaffold it automatically — MVP has no templates.

### Boards (`/boards`)

Table: name, identity (truncated key, full value on hover/tooltip), status,
port. Actions: **Rescan** (`POST /boards/rescan`, disables itself for a few
seconds and shows a spinner — the scan is async per the API doc, so the UI
polls `GET /boards` a few times after triggering it, in addition to the live
`boards` WS topic already keeping the table current) and inline rename
(`PATCH /boards/{id}`, `name` only). No board creation via the UI — boards
only appear via rescan discovery, matching the API surface (there is no
`POST /boards`).

### Devices (`/devices`)

Table: name, board (resolved name, not raw `boardId`), pin, role, live value
(from `devices` WS topic; dash if the owning board is offline). Actions:

- **Create** — modal form: name, board (select from connected/known boards),
  pin (numeric, constrained to the selected board's `capabilities` when
  known), role (select; immutable after create, so the form makes this
  explicit — a note under the field, not a lock icon, since it's still
  editable during creation), role-specific `options` (see
  [09-data-model.md](./09-data-model.md#device-object)'s options table,
  rendered as a small per-role sub-form: `pullup` for `digital-in`; `initial`/
  `onDisconnect`/`safeValue` for `digital-out`/`pwm-out`).
- **Edit** — same form minus board/role/pin identity fields that are
  immutable per [10-api.md](./10-api.md#devices) (`PATCH` rejects `role`/
  `boardId`; the UI additionally disables `pin` editing even though the API
  doesn't explicitly forbid it, to avoid surprising "silently rewires which
  physical pin this device points at" edits — re-pointing a device to a
  different pin is delete-and-recreate, consistent with how role changes are
  handled).
- **Delete** — confirm dialog; on `409` (referenced by a pipeline) the error
  toast names the blocking pipeline(s) from `error.details.pipelineIds` and
  offers a link to each.
- **Manual write** — for `digital-out`/`pwm-out` devices only (button hidden
  for `digital-in`/`analog-in`, since `10-api.md` rejects the endpoint for
  those roles with `400`): a small inline control (toggle for bool, slider
  0–255 for PWM) that calls `POST /devices/{id}/write`. On `409` (an enabled
  pipeline drives this device), the toast names the pipeline
  (`error.details.pipelineName`) and offers "disable pipeline" as a one-click
  action from the toast itself, rather than making the user navigate away to
  find it.

### Pipeline list (`/pipelines`)

Table: name, `enabled` toggle (calls `enable`/`disable`, optimally rendered
as a switch that shows a brief pending state — see
[UX details](#ux-details) on pessimistic updates), node count, `status`
badge. Actions: create (name + `tickMs`, defaults to `20`, opens straight
into the editor with an empty canvas), delete (confirm dialog; no `409` case
here — pipelines aren't referenced by anything else), open (navigates to
`/pipelines/:id/edit`).

Empty state: "No pipelines yet" with a create button, same as the dashboard's
onboarding step.

### Settings / System (`/settings`)

Read-only rendering of `GET /system`: version, commit, uptime (formatted as
"3d 14h"), Go version, OS/arch, and the config summary (`httpAddr`,
`dataDir`, `serialBaud`, `serialAllowlist`, `logLevel`) with a note that
config is file-only and requires editing `/etc/pinctl/config.yaml` plus a
restart — there is no in-UI config editor in MVP, matching
[10-api.md](./10-api.md#system) exactly ("no endpoint to change config at
runtime"). Counts (`boards`/`devices`/`pipelines`/`pipelinesEnabled`) shown as
small stat tiles.

## Pipeline editor

The editor (`/pipelines/:id/edit`) is the centerpiece of the UI: a Vue Flow
canvas with a node palette on the left, the graph in the center, and a
config panel on the right.

```
┌─────────────┬───────────────────────────────────────────┬──────────────────┐
│  Palette    │  Vue Flow canvas                            │  Config panel    │
│  (sources/  │  - nodes (title, ports, config summary)     │  (selected node  │
│   transforms│  - edges (type-colored, live values when    │   or pipeline    │
│   /sinks)   │    debugging)                                │   settings)      │
├─────────────┴───────────────────────────────────────────┴──────────────────┤
│  Toolbar: pipeline name · tick rate · enable/disable · Save · dirty dot     │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Node palette

Grouped exactly as [08-node-catalog.md](./08-node-catalog.md) groups them —
**Sources**, **Transforms**, **Sinks** — each entry a draggable card with the
node's display name and a one-line description (lifted from the catalog
doc's summary table). Dragging a card onto the canvas creates a node of that
`type` at the drop position with `config` set to the type's documented
defaults (e.g. `compare` drops in with `{ op: "gt", threshold: 0 }`) and an
editor-assigned `id` (`n1`, `n2`, … — first unused sequential number in the
current graph, matching the ID scheme in
[09-data-model.md](./09-data-model.md#id-strategy)).

### Node rendering

Each Vue Flow node is a custom component (`PipelineNode.vue`) showing:

- **Title** — the node's `type` display name (e.g. "Compare") plus, for
  `device-read`/`device-write`, the resolved device name (not the raw
  `deviceId`) so the graph reads like the README's worked example.
- **Ports** — one dot per declared input/output port, positioned per Vue
  Flow's handle system, colored by value type:

  | Type | Color |
  |---|---|
  | `bool` | amber |
  | `float64` | teal |

  A port whose type is config-dependent (`constant.out` depends on
  `config.kind`; `delay`/`debounce` `in`/`out` depend on `config.kind`) is
  colored live from the node's current config, not a fixed per-`type` color —
  editing `kind` in the config panel recolors the port immediately.
- **Config summary** — a compact one/two-line rendering of the node's most
  relevant config fields (e.g. `compare` shows "lt 300"; `device-read` shows
  the device name; `timer` shows "every 5000ms") so the graph is scannable
  without opening the config panel for every node. Purely derived from
  `node.config`, computed client-side per node type.
- **State badge** (only while [live debugging](#live-debugging) is active) —
  see below.

### Edges and type-checking

Vue Flow's `onConnect` handler runs a **client-side mirror** of the engine's
edge type-checking rules (see [Client-side validation mirroring](#client-side-validation-mirroring))
before accepting a new connection:

- Source port type must match target port type (`bool`→`bool`,
  `float64`→`float64`). A mismatched drag shows a red "invalid connection"
  ghost edge and snaps back on release rather than completing the connection
  — this is instant, no server round-trip.
- Target port must not already have an incoming edge (each input port in the
  catalog accepts exactly one connection; `and`/`or`'s multiple `inN` ports
  are modeled as **separate distinct ports**, not one port accepting
  multiple edges, so this rule holds uniformly).
- Self-loops (a node's own output back to its own input) are rejected
  client-side immediately — always invalid, no need to wait for `validate`.

Accepted edges render colored by their value type (same amber/teal scheme as
ports). Edges that the *server-side* `validate` call subsequently flags
(e.g. part of a cycle spanning several nodes — not detectable from a single
`onConnect` check) are re-rendered with a dashed red overlay once validation
results come back, per [Client-side validation mirroring](#client-side-validation-mirroring).

### Config panel

The right sidebar shows a form for whatever is selected:

- **No selection** — pipeline-level settings: `name`, `tickMs` (numeric,
  hint: "must be ≥ any node's timing config below this rate" — see
  [08-node-catalog.md](./08-node-catalog.md)'s `timer.periodMs` constraint),
  enabled/disabled toggle (calls `enable`/`disable` directly — a separate
  action from Save, matching the API's separate endpoints).
- **Node selected** — a form generated from that node type's config schema
  as specced in [08-node-catalog.md](./08-node-catalog.md). The schemas are
  hand-encoded client-side as a TypeScript table (`nodeSchemas.ts`, one entry
  per `type`: field name, kind — `string`/`number`/`bool`/`enum`/`deviceRef`
  — default, and validation hints such as `and.inputCount`'s `>= 2` or
  `threshold-hysteresis.offLevel <= onLevel`), keyed the same way the palette
  groups nodes. This is the one place the UI encodes catalog knowledge
  outside the docs — kept intentionally small and table-driven so adding a
  node type later means adding one table entry, not a bespoke form component.
  Special field kinds:
  - `deviceRef` (`device-read`/`device-write`'s `deviceId`) renders as a
    searchable select over the `devices` store, pre-filtered to compatible
    roles (`digital-in`/`analog-in` for `device-read`,
    `digital-out`/`pwm-out` for `device-write`).
  - `enum` (`compare.op`, `edge-detect.edge`, `clock.days`, …) renders as a
    select or checkbox group as appropriate.
  - Numeric fields with a documented relationship to another field
    (`scale.inMin`/`inMax`, `threshold-hysteresis.onLevel`/`offLevel`) get an
    inline client-side check per [Client-side validation mirroring](#client-side-validation-mirroring).
- Editing a field updates the node's `config` in the local editor store
  immediately (marks the graph dirty) — there is no per-field "apply" step.

### Save

**Save** (`PUT /pipelines/{id}`) sends the full current graph
(`name`, `tickMs`, `nodes`, `edges`), matching the API's full-replace
semantics (see [10-api.md](./10-api.md#pipelines): "the editor always
round-trips the entire graph"). Flow:

1. Client-side validation (see next section) must be clean, or Save is
   disabled with a tooltip listing the first blocking error — no point
   round-tripping a graph the client already knows is invalid for reasons
   like a dangling edge from a deleted node.
2. On click, `PUT` fires. While in flight, Save shows a spinner and the
   editor is read-only (no further edits queued behind it — see
   [UX details](#ux-details) on pessimistic updates).
3. **`200`**: dirty flag clears, `updatedAt` refreshed from the response,
   success toast.
4. **`400`**: the response's field-level errors (`details.field`, e.g.
   `"edges[2].to"`) are mapped back to the offending node/edge and rendered
   inline — a red outline on the node plus an error icon with the message on
   hover, and the dashed-red edge overlay described above for edge errors.
   The graph is **not** reverted; the user's edits stay on canvas so they can
   fix and retry. This is the server-only validation surface — see
   [Client-side validation mirroring](#client-side-validation-mirroring) for
   which of these could realistically also be caught client-side vs. which
   inherently need the server's view of other objects (devices, boards).

### Enable/disable toggle

Lives in the toolbar, separate from Save (`POST /pipelines/{id}/enable` /
`/disable`, idempotent per [10-api.md](./10-api.md#pipelines)). Enabling a
pipeline with unsaved edits is blocked client-side with a tooltip ("save
first") rather than silently enabling the last-saved version while the
canvas shows something different — avoids the confusing state of "the graph
on screen isn't what's actually running."

### Dirty-state handling

The `editor` store tracks a `dirty` boolean, set `true` on any local mutation
(node add/move/delete, edge add/delete, any config field edit, pipeline
name/tickMs edit) and cleared only on a successful Save. Node **position**
changes (dragging a node) count as dirty too, since `position` is persisted
(`09-data-model.md`: "purely presentational... but persisted so the graph's
layout survives a reload") — an unsaved drag would otherwise be silently
lost on navigation, which is worse than prompting. See
[UX details](#ux-details) for the navigation guard this feeds.

## Client-side validation mirroring

The engine/store's real validation rules live in
[09-data-model.md](./09-data-model.md#validation-rules) and
[07-pipeline-engine.md](./07-pipeline-engine.md); `POST /pipelines/validate`
is the authoritative check. The editor mirrors a **subset** client-side for
instant feedback (no network round trip while dragging edges/editing forms),
and defers the rest to `validate`, called debounced (~400ms after the last
graph edit) so typing in a config field doesn't fire a request per keystroke.

| Rule | Mirrored client-side? | Why |
|---|---|---|
| Edge port type match (`bool`↔`float64`) | Yes — blocks the connection on drop | Purely local: both ports' types are already known from the node catalog table in memory |
| Edge target already has an incoming edge | Yes — blocks the connection on drop | Same, purely local |
| Self-loop edge | Yes — blocks the connection on drop | Same |
| Required input port unconnected | Yes — inline warning on the node as soon as it's added/edited, re-checked on every edge change | Derivable from the static catalog schema + current edge list, no server state needed |
| `and`/`or` `inputCount` bounds (`>= 2`) | Yes — form-level min on the field | Static schema constraint |
| `threshold-hysteresis.offLevel <= onLevel`, `scale.inMin != inMax` | Yes — inline field warning | Static, single-node arithmetic constraint from the catalog doc |
| `timer.periodMs >= pipeline.tickMs` | Yes — inline field warning, recomputed if `tickMs` changes | Both values are already in the local editor store |
| Cycle detection (topological sort) | Yes — recomputed on every edge change (client owns the full graph already, cycle detection is cheap at this node count) | Local computation over the in-memory graph; instant feedback matters here because a cycle is easy to introduce by accident when wiring |
| Duplicate edge `(from, to)` | Yes — blocks on drop | Local |
| `deviceId` references an existing device with a compatible role | Partially — the `deviceRef` select is pre-filtered to compatible, currently-known devices, so most invalid states are unreachable through the UI. **Not** re-checked if a referenced device is deleted by *another* browser tab/session while this editor is open (rare, LAN tool with usually one operator) | Requires the live `devices` store, which the editor already has, but a race with concurrent deletion elsewhere still needs the server's authoritative check |
| Device role compatibility surviving a delete-and-recreate elsewhere | No — server-only | Inherently a cross-session consistency check; the editor has no way to know another tab deleted and recreated a same-named device with a different role |
| Two `device-write` nodes targeting the same device (warning, not error) | No — server-only, surfaced on `validate`/save response | Requires seeing *other* pipelines' contents, which the editor doesn't load when editing one pipeline |
| Schema-level malformed request body | N/A | The editor only ever constructs well-formed bodies; this class of error is a defensive server check, not something a well-behaved client can trigger |

Design principle: **mirror what's derivable from data the editor already
holds in memory** (the static node catalog schema, the current graph, the
already-loaded device list); **defer to the server** anything that depends on
state the editor doesn't own (other pipelines, concurrent edits from other
sessions). This keeps the mirrored subset small and mechanically justified
rather than a growing ad hoc list.

## Live debugging

When the editor is open on a pipeline whose `enabled` is `true`, it
subscribes to the `pipeline:{id}` WS topic (see
[10-api.md](./10-api.md#topics)) and unsubscribes on navigating away or
disabling the pipeline. Opening the editor on a **disabled** pipeline does
not subscribe — there's nothing to show (no scheduler running), and the
canvas instead shows a small "enable to see live values" hint bar in place of
the debug affordances below.

- **Per-edge live values** — each `TickSnapshot.nodes[nodeId]` value is
  looked up for the node on the *source* end of every edge and rendered as a
  small label near the edge's midpoint, formatted per type (`true`/`false`
  for bool with a filled/hollow dot; numbers rounded to a sensible precision
  for float64 — full precision on hover). Updated on every WS frame for this
  topic (per-tick fidelity, not throttled — matches `10-api.md`'s note that
  `pipeline:{id}` is bounded by drop-if-slow-consumer rather than a fixed
  throttle).
- **Node state badges** — a small colored dot on each node: green while
  receiving fresh snapshots, gray/"stale" if the pipeline's `status` (from
  the `pipelines` topic, also subscribed while the editor is open) is
  `paused` (board offline) or `error`, in which case a tooltip surfaces
  `detail` from that topic's payload.
- **Device live values on Dashboard/Devices** — both screens subscribe to
  the throttled `devices` topic (not `pipeline:{id}` — they're not in a
  single-pipeline debugging context) for as long as they're mounted, and
  unsubscribe on navigating away. Values render next to each device row/card,
  with a timestamp-based staleness check (see below) rather than trusting the
  last value forever if updates stop arriving.
- **WS reconnect with backoff** — the `ws` store owns exactly one socket
  (see [Pinia stores](#pinia-stores)) and reconnects on close/error with
  exponential backoff (e.g. 500ms → 1s → 2s → 5s, capped), re-sending
  `subscribe` for every topic any component currently has an active
  subscription to (the store's refcounted topic table survives a reconnect —
  components don't need to know a reconnect happened). While disconnected,
  every live-value display (dashboard, devices, editor) shows a **stale
  indicator** (dimmed value + a small "reconnecting…" badge) rather than
  clearing the last-known value outright — better to show "this was true 8
  seconds ago, stale" than a blank field. The same staleness treatment
  applies to any single value that hasn't updated in longer than a
  type-appropriate window (e.g. no `devices` update for a device in >3× the
  topic's ~200ms throttle interval suggests the underlying board went
  offline, which the `boards` topic will also be reporting independently).

## Pinia stores

| Store | Owns | Notes |
|---|---|---|
| `boards` | List of boards + live status, synced from `GET /boards` on load and the `boards` WS topic thereafter | Actions: `fetch`, `rescan`, `rename(id, name)` |
| `devices` | List of devices + live values, synced from `GET /devices` on load and the `devices` WS topic thereafter | Actions: `fetch`, `create`, `update`, `remove`, `manualWrite(id, value)`. Live values keyed by `deviceId`, each with `value` + `timestamp` for staleness checks |
| `pipelines` | List of pipeline summaries (`GET /pipelines`) + live `status` from the `pipelines` WS topic | Actions: `fetch`, `create`, `remove`, `enable(id)`, `disable(id)`. Does **not** hold full graphs — those are the `editor` store's concern, loaded per-edit-session via `GET /pipelines/{id}` |
| `ws` | The single multiplexed `WebSocket`, connection state (`connecting`/`open`/`reconnecting`/`closed`), and a **refcounted topic subscription table** (`Map<topic, refcount>`) | `subscribe(topic)`/`unsubscribe(topic)` are called by any store/component that needs a topic; the store only sends a wire `subscribe`/`unsubscribe` frame on a 0→1 / 1→0 refcount transition, so e.g. Dashboard and Devices both being mounted and both wanting `devices` doesn't double-subscribe or unsubscribe out from under the other. Incoming `event` frames are dispatched by `topic` to the owning store's update method (`boards`/`devices`/`pipelines` stores register a handler; `pipeline:{id}` events are routed to the `editor` store when its `pipelineId` matches) |
| `editor` | The pipeline currently open for editing: local `nodes`/`edges`/`name`/`tickMs` (Vue Flow's working model), `dirty` flag, client-side validation errors (see [Client-side validation mirroring](#client-side-validation-mirroring)), server-side validation errors from the last `validate`/`PUT` response, and the latest `TickSnapshot` while live debugging | Actions: `load(pipelineId)` (GET, resets dirty), `addNode`, `moveNode`, `updateNodeConfig`, `addEdge`, `removeEdge`, `save()` (PUT), `validateNow()` (debounced trigger for `POST /pipelines/validate`). This store is torn down (and its `pipeline:{id}` subscription released) when leaving the editor route |

All stores construct their REST calls through the shared `api/` module (see
[Stack and build](#stack-and-build)) rather than calling `fetch` directly, so
the error-envelope-to-toast mapping (see [UX details](#ux-details)) is
centralized in one place.

## UX details

### Unsaved-changes guard

A `beforeRouteLeave` navigation guard on the editor route checks
`editor.dirty`; if true, it shows a confirm dialog ("Discard unsaved
changes?") before allowing navigation away, and a `beforeunload` browser
event does the same for a tab close/refresh. This is the only place in the
app that guards navigation — every other screen's mutations are immediate,
single-field REST calls with no local-only staged state to lose.

### Optimistic vs. pessimistic updates: pessimistic, documented

**Decision: pessimistic updates everywhere** (Boards rename, Devices CRUD and
manual write, Pipelines enable/disable/delete, and pipeline Save) — the UI
shows a pending state (spinner/disabled control) and only reflects the change
once the server responds, rather than updating local state immediately and
rolling back on failure.

Rationale: pinctl is a LAN tool talking to a Raspberry Pi on the same
network — round trips are single-digit milliseconds, not the hundreds of
milliseconds optimistic UI is meant to paper over. Optimistic updates would
add rollback-on-error complexity (undoing a locally-applied enable, a locally
-added device, a locally-toggled pipeline) for a latency problem that
basically doesn't exist here, and several of the mutations have server-side
outcomes that aren't safely predictable client-side anyway (enable can come
back `paused` if the board is offline; manual write can `409`; pipeline save
can fail validation) — an optimistic UI would have to immediately correct
itself in exactly the cases where getting it right matters most. Pessimistic
keeps every state transition in the UI a direct, honest reflection of a
server response.

### Empty states

Every list screen (Boards, Devices, Pipeline list) has a dedicated empty
state (icon + one-line explanation + primary action) rather than an empty
table — see the Dashboard's onboarding card for the composite version shown
when *nothing at all* is configured yet.

### Error toasts

Every non-2xx REST response is caught centrally in the `api/` module and
turned into a toast using the error envelope from
[10-api.md](./10-api.md#general-conventions): `error.message` as the toast
body, `error.code` mapped to a toast severity/icon (`validation_error` /
`conflict` → warning styling, `not_found` → info, `internal_error` → error),
and `error.details` rendered as a secondary line where it's user-meaningful
(the `409` conflict cases documented above). Toasts auto-dismiss after a few
seconds except `internal_error`, which stays until manually dismissed since
it may need reporting. Pipeline-save `400`s are the one case that does
**not** also toast — those errors render inline on the graph instead (see
[Save](#save)); a toast on top would be redundant with the inline markers and
add noise to the one screen where the user is already looking directly at
the problem.

### Loading states

Every screen shows a skeleton/spinner for its initial fetch and does not
flash empty-state UI before the first response arrives (a brief "checking…"
state is used instead of jumping straight to "no boards yet" while the
initial `GET /boards` is still in flight).

## Component tree (pipeline editor screen)

```
PipelineEditorView                      (route: /pipelines/:id/edit)
├── EditorToolbar
│   ├── PipelineNameField
│   ├── TickRateField
│   ├── EnableDisableToggle
│   ├── SaveButton                      (disabled while client-side invalid)
│   └── DirtyIndicator
├── NodePalette
│   ├── PaletteGroup ("Sources")
│   │   └── PaletteCard × N             (device-read, constant, timer, clock)
│   ├── PaletteGroup ("Transforms")
│   │   └── PaletteCard × N             (compare, threshold-hysteresis, scale,
│   │                                     and, or, not, delay, debounce, edge-detect)
│   └── PaletteGroup ("Sinks")
│       └── PaletteCard × N             (device-write, log)
├── VueFlowCanvas                       (@vue-flow/core <VueFlow>)
│   ├── PipelineNode × N                (custom node renderer)
│   │   ├── NodeTitle
│   │   ├── NodePort × N                (typed handles, bool/float64 colored)
│   │   ├── ConfigSummary
│   │   ├── ValidationBadge             (inline error/warning, if any)
│   │   └── LiveStateBadge              (only while debugging: fresh/stale/error)
│   ├── PipelineEdge × N                (custom edge renderer)
│   │   ├── EdgeLine                    (colored by value type)
│   │   ├── LiveValueLabel              (only while debugging)
│   │   └── ValidationOverlay           (dashed red, on server-flagged edges)
│   └── CanvasControls                  (Vue Flow zoom/fit/minimap)
└── ConfigPanel
    ├── PipelineSettingsForm            (shown when nothing selected)
    └── NodeConfigForm                  (shown when a node is selected)
        ├── FieldText / FieldNumber / FieldBool / FieldEnum / FieldDeviceRef
        └── FieldValidationHint         (client-side mirrored rule messages)
```

Cross-cutting, not part of the visual tree but wired in at the view level:
the view subscribes/unsubscribes `pipeline:{id}` and `pipelines` WS topics on
mount/unmount (via the `ws` store) and loads the graph into the `editor`
store on `PipelineEditorView` mount (`editor.load(route.params.id)`).

## Out of scope for MVP

| Item | Status |
|---|---|
| Mobile/responsive layout | Not designed for in MVP; the editor in particular assumes a desktop-sized viewport (palette + canvas + config panel side by side). A phone/tablet layout is a roadmap item, not attempted here. |
| Dark mode toggle | No in-app toggle. The UI follows the OS/browser's `prefers-color-scheme` automatically (light/dark CSS via media query) — system-preference-only, no user-facing setting to override it. |
| i18n | English only, no translation infrastructure. |
| Auth screens (login, token entry) | None — matches the brief's "no auth in MVP." When the roadmap's optional single-password + token lands (see [00-design-brief.md](./00-design-brief.md)), it adds a login screen and a request-level token header; out of scope here. |
