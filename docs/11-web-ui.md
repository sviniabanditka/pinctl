# pinctl — Web UI

This document describes the Vue 3 web UI as built: the console, the Vue Flow
pipeline editor, the design system, live debugging, state management, and
client-side validation. It was originally written before any UI code existed;
the UI has since been built and substantially redesigned (the palette/canvas/
sidebar layout the brief sketched was replaced by a toolbar + full-bleed
canvas + floating actions — see [Pipeline editor](#pipeline-editor) for why).
Where this doc and [00-design-brief.md](./00-design-brief.md) disagree, this
doc describes what actually ships.

Related docs: [03-architecture.md](./03-architecture.md) (where `web/` sits in
the system, ADR-4 on embedding), [08-node-catalog.md](./08-node-catalog.md)
(the node set and config schemas the editor renders), [09-data-model.md](./09-data-model.md)
(pipeline/device/board JSON shapes, node `position{x,y}`), [10-api.md](./10-api.md)
(the REST/WebSocket contract this UI is a pure client of).

## Table of contents

- [Stack and build](#stack-and-build)
- [Screens and routing](#screens-and-routing)
- [The console (`/`)](#the-console-)
- [Design system](#design-system)
- [Pipeline editor](#pipeline-editor)
- [Simulate mode](#simulate-mode)
- [Client-side validation mirroring](#client-side-validation-mirroring)
- [Live debugging](#live-debugging)
- [Pinia stores](#pinia-stores)
- [UX details](#ux-details)
- [Component tree (pipeline editor screen)](#component-tree-pipeline-editor-screen)
- [Out of scope](#out-of-scope)

## Stack and build

Per the brief: **Vue 3 + Vite + Pinia + Vue Flow**, written in TypeScript.

| Concern | Choice | Notes |
|---|---|---|
| Framework | Vue 3, `<script setup>` SFCs | |
| Build tool | Vite | |
| State | Pinia | one store per domain, see [Pinia stores](#pinia-stores) |
| Node editor canvas | Vue Flow (`@vue-flow/core`, `@vue-flow/background`, `@vue-flow/controls`) | custom node/edge renderers per [Pipeline editor](#pipeline-editor) |
| Language | TypeScript throughout | types for the pipeline/device/board JSON shapes are hand-kept in sync with [09-data-model.md](./09-data-model.md) — no server-driven codegen |
| Routing | vue-router, **history mode** | matches the server's SPA fallback-to-`index.html` behavior in [10-api.md](./10-api.md#static-ui-serving) |
| HTTP client | `fetch` wrapped in a thin `api/` module | one function per REST endpoint, returning typed results or throwing a typed `ApiError` built from the error envelope |
| WebSocket client | thin wrapper around native `WebSocket`, owned by the `ws` store | see [Pinia stores](#pinia-stores) |
| Fonts | self-hosted IBM Plex Sans / Mono / Sans Condensed, woff2 only | see [Design system](#design-system) |

### Production build

`web/` builds to static assets (`web/dist/`) via `vite build`, including the
font files as fingerprinted assets. `cmd/pinctl` embeds `web/dist` via
`embed.FS` and `internal/api` serves it for every non-`/api/*` path (see
[03-architecture.md](./03-architecture.md#adr-4-embedded-ui-embedfs-not-a-separately-served-frontend)).
There is no runtime configuration step — the built UI always talks to
`/api/v1` and `/api/v1/ws` on the same origin it was served from.

### Dev mode

`vite dev` runs a local dev server (default `:5173`) with hot module reload,
proxying `/api` (REST and the `/api/v1/ws` WebSocket upgrade) to the Go
runtime, which is run separately (`go run ./cmd/pinctl` against `~/.pinctl/`).
This is a `vite.config.ts` `server.proxy` entry only — the Go binary never
needs to know it's being developed against.

## Screens and routing

The UI is two screens, not six. It started as six dedicated screens (per an
earlier draft of this doc) and was consolidated into one console once it
became clear that Boards/Devices/Pipelines never needed to be anything more
than three tables on the same page — a nav rail for two-and-a-half screens
was overhead, not structure.

| Route | Screen | Purpose |
|---|---|---|
| `/` | `HomeView` | The console: boards, devices, pipelines, all as tables |
| `/pipelines/:id` | `PipelineEditorView` | Vue Flow canvas + live debug for one pipeline |
| `/boards`, `/devices`, `/pipelines` | — | `redirect: '/'` — kept so a pre-consolidation bookmark lands somewhere useful, not documented as screens |
| anything else | `NotFoundView` | small in-app "not found" card, not a server 404 |

There is no `/settings` route and no `GET /system` call anywhere in the UI —
an earlier draft of this doc specced a read-only config/version screen, but
it was never built and nothing in the client fetches `/system`.

`App.vue` no longer has a navigation rail: with two routes, a header link
back to `/` (the `pinctl` wordmark) is all the wayfinding the app needs. The
header also carries the WebSocket connection-state indicator (`Connected` /
`Connecting…` / `Reconnecting…` / `Offline`, dot + label), visible on every
screen. The pipeline editor route sets `meta.fullscreen`, which thins the
header padding and hands the editor the entire remaining viewport with no
page scrollbar (`App.vue`'s `.content-flush`), since the canvas manages its
own scrolling/panning.

Any unmatched client route (or a browser refresh on a deep link like
`/pipelines/pl_k2n8h5wtxy`) resolves because the server falls back to
`index.html` for all non-`/api/*` paths, per
[10-api.md](./10-api.md#static-ui-serving).

## The console (`/`)

`HomeView` is the whole app apart from the editor: a page head ("Console" +
a global **+ Add** button) and three sections, each a `.data-table` fed by
the same Pinia store the rest of the app uses — there are no dashboard-only
API calls.

```
┌──────────────────────────────────────────────────────────────┐
│  Console                                              [+ Add] │
├──────────────────────────────────────────────────────────────┤
│  Boards                                          [Rescan]     │
│  Name │ Identity │ Port │ Firmware │ Capabilities             │
├──────────────────────────────────────────────────────────────┤
│  Devices                                    [Add device]      │
│  Name │ Role │ Board │ Pin │ Trace │ Value │ Control │  ⋯      │
├──────────────────────────────────────────────────────────────┤
│  Pipelines                                  [New pipeline]    │
│  Name │ Status │ Enabled │ Tick │ Nodes │  ⋯                  │
└──────────────────────────────────────────────────────────────┘
```

### Boards

Columns: name (click to inline-rename), identity (`kind:key`, mono, dimmed),
port, firmware (`name major.minor`, dimmed, `—` while disconnected),
capabilities (`N digital · N analog · N pwm · N servo`, dimmed). A leading `.dot`
colored by `status` (`connected`/`disconnected`/`reconnecting`/`error`) sits
in the name cell. **Rescan** (`POST /boards/rescan`) is fire-and-forget; the
button shows "Scanning…" while in flight and results arrive over the `boards`
WS topic (see [Live debugging](#live-debugging)), not a poll loop. Rename is
inline (click the name → text input → Save/Cancel, `PATCH /boards/{id}`).
There is no board-creation UI — boards only appear via rescan discovery,
matching the API surface (no `POST /boards`).

### Devices

Columns: name, role (`.label`), board (resolved name), pin (mono), **Trace**,
**Value**, **Control**, row actions (Edit/Delete, revealed on row hover/focus
via `.cell-actions`). Trace and Control both branch on role:

- **Inputs** (`digital-in`/`analog-in`) — Trace is a live [`Sparkline`](#sparklinevue)
  fed by a 60-sample rolling buffer the view keeps per device; Control shows
  `—` (inputs aren't written to).
- **Outputs** (`digital-out`/`pwm-out`/`servo`) — Trace is a static "last
  written" pill (no sparkline; nothing streams *from* an output); Control is
  a toggle switch for `digital-out`, a `0`–`255` range slider for `pwm-out`,
  or a `0`–`180` range slider (value suffixed `°`) for `servo` — all three
  calling `POST /devices/{id}/write`. The servo slider reuses the PWM
  control's shape (range input + mono readout beside it); only the range and
  suffix differ. Live value renders teal (`--signal-float`), same as any
  other float64 device, since a servo's angle is write-only telemetry
  reflected back through the same write-observation path pwm-out uses. The
  History action is available for servo rows exactly as for any other
  device — servo writes are recorded like any other value.

**Add device** opens `DeviceFormDialog` (name, board — hidden entirely in
single-board deployments where `GET /boards` returns nothing, letting the
server's default-board fallback apply — pin, role, then role-specific
options: `pullup` for `digital-in`; `initial`/`onDisconnect`/`safeValue` for
the three output roles (`initial`/`safeValue` are an angle 0–180 for
`servo`, a duty cycle 0–255 for `pwm-out`), `safeValue` only shown once
`onDisconnect` is set to `safe-value`. `servo` additionally shows
`minPulseUs`/`maxPulseUs` number inputs, mono, defaulting to `544`/`2400`
(the standard hobby-servo pulse range) with a hint that cheap servos often
need trimming to reach 0°/180° cleanly — these feed `SERVO_CONFIG`, not the
angle itself). Editing the same dialog on an existing device disables
board/pin/role (each carries a "delete and recreate to re-point it" hint) —
the server rejects `role`/`boardId` on `PATCH`, and the UI additionally
locks `pin`, which the API doesn't explicitly forbid, to avoid a "silently
rewires which physical pin this points at" edit.

**Delete** goes through the shared `ConfirmDialog`; a `409` (referenced by a
pipeline) surfaces via the error toast, not a dedicated dialog state — see
[Error toasts](#error-toasts). A manual write's `409` (an enabled pipeline
drives this device) is handled the same way. Creating a device with a role
the target board's pin can't do (e.g. `servo` on a pin with no timer, or any
role outside the board's reported capabilities) returns `400
unsupported_pin_mode` when the board is connected — surfaced via the same
error toast, naming the pin/role and the board's supported pins in the
server's own message rather than a client-guessed list. When the board is
offline the create still succeeds; pin-mode is only checked against a board
that's actually there to check it against.

### Pipelines

Columns: name, status (`StatusBadge`), enabled (toggle switch calling
`enable`/`disable`), tick (mono, `N ms`), node count (dimmed), row actions
(Open/Delete). The row itself is clickable (`row-clickable`, keyboard-focusable,
`Enter` opens) — Open is a redundant explicit action for pointer users who
don't want to hit the row. **New pipeline** is a small dialog (name, tick
rate, default `20`) that creates an empty-graph pipeline and navigates
straight into the editor.

### Empty states

Each section's table body degrades to a single centered text row inside the
same `.data-table` (`.table-empty`, `colspan` spanning every column) — there
is no dedicated empty-state block with an icon and a primary-action button.
The row's copy carries the call to action as prose ("No pipelines yet. Use
the + button above to create one.").

### The "+" command palette

The page head's **+ Add** button (and the `/` or `n` keyboard shortcut, when
focus isn't in a text field) opens a [`CommandPalette`](#commandpalettevue)
wired to three actions — **Rescan boards**, **Add device**, **Create
pipeline** — one per store section, grouped that way rather than as an
undifferentiated list. **Add device** is `disabled` with an explanatory
subtitle ("No board is connected — connect one first.") when
`boards.boards` has no `connected` entry.

## Design system

`src/styles/base.css` is unscoped, imported once, and is the single source
of the app's visual vocabulary — every component styles itself with its
custom properties and utility classes rather than hand-rolled color values.
The governing idea, stated in the file's own header comment: **"Instrument,
not dashboard."**

- **Saturated color is reserved for signal.** Chrome — panels, borders,
  buttons, most text — is monochrome graphite (`--panel`/`--surface`/
  `--raised`/`--hairline`/`--ink*`). There is no "primary" brand color;
  emphasis is expressed as weight and contrast (`button.primary` is filled
  `--ink` on `--panel`, not a hue) or a hairline border, never color. The
  only colors with any saturation are the **signal tokens**:

  | Token | Meaning |
  |---|---|
  | `--signal-bool` (amber) | bool ports, bool values, digital signal |
  | `--signal-float` (teal) | float64 ports, float64 values, analog signal |
  | `--signal-alert` (red) | errors, destructive actions |
  | `--signal-warn` (amber-orange) | warnings |
  | `--ink-faint` | idle/stopped status, the neutral end of the status scale |

  The consequence is a legible rule of thumb: **if something on screen is
  colored, it's data** (a port's type, a live value, a status). Chrome never
  competes with it for attention.

- **Mono means machine.** Anything a human reads as a literal value or
  identifier rather than prose — device pins, board identities/ports,
  firmware version strings, tick rates, node config values, expression
  text, log timestamps, port ids — renders in `--font-mono` (`.mono`), and
  numeric ones additionally get `font-variant-numeric: tabular-nums` (`.num`)
  so digits don't jitter as they update live. Prose renders in `--font-sans`.
  Section eyebrows and eyebrow-style labels (table headers, field labels,
  node titles, status text) use `--font-label` (IBM Plex Sans Condensed,
  uppercase, letter-spaced) via the `.label` utility.

- **Tokens.** Enclosure: `--panel` (app background) → `--surface` (cards/
  panels) → `--raised` (popovers/menus/inputs), each a step lighter, plus
  `--hairline`/`--hairline-strong` for 1px rules. Ink: `--ink` / `--ink-dim`
  / `--ink-faint`, three steps of legibility for primary/secondary/tertiary
  text. Elevation: a single `--shadow-popover`. Focus: `--focus-ring`
  (`--signal-float` at 50% opacity) applied via `:focus-visible` everywhere,
  2px outline with a 1px offset — never suppressed, only ever restyled.

- **Spacing and radius.** An 8-point-ish scale, `--space-1` through
  `--space-12`: 4 / 8 / 12 / 16 / 24 / 32 / 48px. Two radii: `--radius-sm`
  (4px, controls) and `--radius-lg` (8px, cards/popovers/dialogs).

- **The `.data-table` system.** One shared table styling, used by all three
  console sections (see [The console](#the-console-)): condensed-uppercase
  `.label`-styled headers, `.cell-mono`/`.cell-num` for identifier/numeric
  columns, `.cell-actions` for a row-hover-revealed action cluster, and a
  `.table-empty` row for the empty state. Below 640px it reflows from a grid
  into stacked label/value rows per `<td data-label="…">`, using CSS
  `content: attr(data-label)` rather than a second markup path.

- **`.dot` / `.badge`.** `.dot` is the atomic 8px status indicator (classes
  `running`/`warn`/`error`/`stopped` map onto the signal tokens); `.dot-pulse`
  animates it for a transitional state (reconnecting). `.badge` is a legacy
  pill-turned-condensed-label (dot + `.label` text) — `StatusBadge.vue` is
  the current, canonical version of the same idea.

- **Light mode.** No in-app toggle — `@media (prefers-color-scheme: light)`
  overrides the token block wholesale, warm-grey chrome in place of
  graphite. The signal tokens are **darkened, same-hue** variants in light
  mode, not the dark-mode hex values reused: the dark-mode ambers/teals/reds
  are tuned against a near-black surface and fall to ~2–4:1 contrast as
  *text* on the light theme's near-white surfaces (measured, documented
  inline in `base.css`), well under the 4.5:1 body-text floor. Each is
  darkened just enough to clear 4.5:1 on `--raised`/`--surface` while
  keeping the same hue, so bool/float/alert/warn stay identifiable across
  themes.

- **Fonts.** IBM Plex Sans, Plex Mono, and Plex Sans Condensed, self-hosted
  via `@fontsource` packages, woff2-only (`src/styles/fonts.css`) — every
  target browser this app cares about supports woff2, so shipping woff/ttf
  fallbacks alongside would be dead weight. The font files are bundled by
  Vite as fingerprinted static assets and ride along inside `web/dist`,
  which is what `embed.FS` embeds into the `pinctl` binary — so "the UI" in
  the shipped binary includes its own fonts with no external font request.

## Pipeline editor

The editor (`/pipelines/:id`) replaced the brief's original three-column
layout (fixed palette · canvas · config sidebar) with **toolbar + full-bleed
canvas + floating actions**: the palette became a modal command palette (a
persistent side rail was real estate the canvas needed more), and the config
form became a popover anchored to the selected node (co-locating the form
with the thing it edits, instead of forcing your eyes to the opposite edge
of the screen every time you click a node).

```
┌────────────────────────────────────────────────────────────────┐
│  [name] tick[__]ms  tick 42   3 errors      ● Disable   Save    │
├────────────────────────────────────────────────────────────────┤
│                                                                  │
│                                                       ┌────────┐│
│           (Vue Flow canvas, full-bleed)               │ Config ││
│                                                        │ popover││
│                              ●selected node──caret────>│        ││
│                                                        └────────┘│
│                                                          ○ LOG   │
│                                                          (+) FAB │
├────────────────────────────────────────────────────────────────┤
│  Log drawer (hidden by default, toggled by the LOG button)      │
└────────────────────────────────────────────────────────────────┘
```

### Toolbar (`EditorToolbar.vue`)

A single row, always visible, holding everything the earlier design had
scattered across a "no selection" sidebar state:

- **Name** — plain text input, direct-bound to `editor.name`.
- **Tick** — numeric input + `ms` label, bound to `editor.tickMs`.
- **Live tick counter** — `tick {N}`, shown only while enabled and receiving
  snapshots.
- **Error count** — a button ("N errors") that appears only when server
  errors or a client-detected cycle exist; clicking it selects the first
  flagged node (opening its config popover).
- **Enable/Disable** — immediately left of Save, per the design brief's
  layout intent, but implemented as its own button rather than folded into
  Save. Disabled while the graph has unsaved edits (`editor.dirty`), with a
  "Save first" tooltip — enabling a pipeline whose canvas doesn't match
  what's actually saved would show something that isn't really running.
- **Save** — disabled while saving, while any client-mirrored rule is
  violated, or while there's nothing to save; its tooltip explains which of
  the three, down to naming the first blocking node/cycle.
- **Dirty dot** — a small amber `.dot` next to Save whenever `editor.dirty`
  is true.

There is no separate "pipeline settings" panel for the no-selection state —
this toolbar *is* that state, permanently.

### Canvas and the add-node FAB

The canvas is a `<VueFlow>` instance filling `.canvas-wrapper`, with
`@vue-flow/background`'s dot grid and `@vue-flow/controls`' zoom/fit
controls (restyled to match the graphite chrome — the package ships a
light-theme panel by default). Two floating round buttons sit in the
canvas's bottom-right corner:

- **Add node** (`+`, 56px, primary) — opens the same `CommandPalette` the
  console uses, wired to the node catalog instead of console actions (see
  [`CommandPalette.vue`](#commandpalettevue)). Also opened by `/` or `n`
  when focus isn't in a form field. Selecting a type inserts the node at
  the viewport center (nudged by a small per-insert jitter so repeated
  inserts don't stack exactly on top of each other), or nodes can be
  dragged from the palette list onto the canvas via native HTML5 drag-and-
  drop and dropped at the cursor position — the palette rows themselves
  are **not** draggable cards; drag-and-drop targets the canvas drop zone,
  not a palette-card affordance.
- **LOG** (44px, secondary, stacked directly above the add-node FAB) —
  toggles the [log drawer](#logpanelvue); shows an unread-count badge while
  the drawer is closed and entries have arrived.

A "debug hint" bar floats at the top-center of the canvas: "Enable the
pipeline to see live values" while disabled, or the pipeline's non-`running`
status (`paused`/`error` + `detail`) while enabled but not actually ticking.

### `Sparkline.vue`

The signature visual motif, reused on the console's Devices trace column.
A tiny inline SVG (default 96×28) drawing the last 60 samples of a value:

- **Analog series** (`bool={false}`) draw a smooth polyline through each
  sample, with a soft gradient fill underneath.
- **Bool series** (`bool={true}`) draw a **square wave** — the path holds
  each sample's level flat and steps vertically at the next sample's x,
  rather than interpolating a slope between `0` and `1`. A digital signal
  should look digital.
- **Empty state** is a flat hairline at the trace's vertical midpoint, not
  a blank box — the same "always draw something, degrade to a neutral
  mark" instinct as the table empty states.

Color is passed in by the caller (`--signal-bool` for bool devices,
`--signal-float` for analog), keeping the component itself signal-agnostic.

### `CommandPalette.vue`

A generic, domain-agnostic modal list — the console's `/`-key action menu
and the editor's add-node menu are two callers of the same component, not
two implementations.

**Props:**

| Prop | Type | Default | Meaning |
|---|---|---|---|
| `open` | `boolean` | — | controls visibility |
| `groups` | `PaletteGroup[]` | — | `{ label: string; items: PaletteItem[] }[]` |
| `placeholder` | `string` | `'Search…'` | search input placeholder |
| `emptyText` | `string` | `'No results.'` | shown when the filter matches nothing |

```ts
interface PaletteItem {
  id: string
  title: string             // rendered mono
  subtitle?: string         // one-line description, ink-dim
  keywords?: string[]       // matched by search, not rendered
  dots?: { color: string; title: string }[]  // small trailing colored dots
  disabled?: boolean
}
interface PaletteGroup { label: string; items: PaletteItem[] }
```

**Emits:** `select(id: string)`, `close()`.

**Keyboard map:** the *caller* decides how the palette opens (`/` or `n` on
both the console and the editor, when focus isn't in an `INPUT`/`TEXTAREA`/
`SELECT`/`contenteditable`); once open, typing filters by title/subtitle/
keywords, `↑`/`↓` move the highlight (wrapping, skipping `disabled` items),
`Enter` selects the highlighted item, `Esc` closes, and `Tab` is trapped on
the search input (rows aren't independently focusable — they're driven by
`aria-activedescendant`).

**What each route feeds it:**

- **Console (`/`)** — three action items, one per section: *Rescan boards*,
  *Add device* (disabled + a reason string when no board is `connected`),
  *Create pipeline*.
- **Editor** — every node type from `NODE_SCHEMAS`, grouped Sources/
  Transforms/Sinks (see [Node catalog wiring](#node-catalog-wiring)), each
  row's `dots` showing that node type's port colors (amber for bool, teal
  for float64) so the type shape is visible before you commit to inserting it.

### `ConfigPanel.vue` popover

Renders when `selectedNodeId` is non-null; the parent view
(`PipelineEditorView`) owns all positioning math, `ConfigPanel` only renders
content and its own focus/keyboard handling (focus trap, `Esc` → `close`).

**Anchoring.** Vue Flow's reactive `viewport` (pan/zoom transform) doesn't
expose per-node *screen* rects directly, so the popover is positioned from
the selected node's real DOM element — `vueFlowRef.value.querySelector('[data-id="…"]').getBoundingClientRect()`
— rather than back-computed from the viewport transform, which is both
simpler and exact. It defaults to the node's right side; if it would overflow
the viewport it flips to the left instead, then clamps within an 8px margin.
Repositioning is scheduled via `requestAnimationFrame` (coalesced — a flag
guards against queuing more than one pending frame) and triggered by
selection change, `viewport` change (pan/zoom), the selected node's own
`position` change, a window resize, and the log drawer opening/closing
(which changes how much vertical room the popover has to sit above, at
narrow widths). Below 900px the popover becomes a full-width bottom sheet
instead of a floating card, anchored above the log drawer if it's open so
the two don't overlap.

**One `useVueFlow()` call.** `PipelineEditorView` is the only place that
calls Vue Flow's `useVueFlow()` composable — it owns `project`,
`screenToFlowCoordinate`, `vueFlowRef`, `viewport`, and `onConnect`, and
passes down what child components need as props/emits. This is deliberate,
not incidental: a second `useVueFlow()` call in a child component silently
creates its own disconnected store rather than sharing the parent's, so
`ConfigPanel` and the node/edge renderers consume Vue Flow state through
props (`selectedNodeId`, `NodeProps`, `EdgeProps`) instead of calling the
composable themselves.

**Body contents:** node title + Delete/Close in the header; any server
validation errors for the node (from the last `validate`/save response) and
a "required input not connected" warning (client-mirrored) above the fields;
then one form control per field in the node's schema (`nodeSchemas.ts`) —
text, number, boolean checkbox, enum select, `deviceRef` select (filtered to
compatible roles), a weekday multi-toggle (`clock.days`), and the
`expression` node's dedicated field kinds below.

### The `expression` node's config UI

`expression` (see [08-node-catalog.md](./08-node-catalog.md#expression))
gets three field kinds no other node type uses:

- **`inputList`** — one row per named input (`config.inputs[]`), each row a
  mono text field for the identifier plus a `float64`/`bool` type select and
  a remove button, with an "+ Add input" row beneath. Port ids on the node
  itself are derived straight from these rows in declared order — there is
  no separate "wire up ports" step.
- **`expression`** — a monospace, non-spellchecked `<textarea>` for the
  expression text, with a static helper line underneath listing the
  operators (`+ - * / %  < <= > >= == !=  && || !`) and builtins
  (`min max abs clamp floor ceil round sqrt`) the parser accepts (mirrored
  from `internal/expr`, not re-derived at runtime).
- Client-side checks run on every edit, independent of the server's
  authoritative parse: empty expression text; an input row with no name; a
  name that isn't a valid identifier; a name that collides with an existing
  input; and **reserved-name rejection** — an input can't be named after a
  builtin (`min`, `max`, …) or the `true`/`false` keyword tokens, since the
  parser's lexer would never accept it as an identifier either. This is
  deliberately shallow (no client-side expression parser) — anything that
  actually requires parsing the expression body is left to the debounced
  `POST /pipelines/validate` call.

### `LogPanel.vue`

A drawer fed by the `log` array on `pipeline:{id}` WS snapshots (see
[Live debugging](#live-debugging)), hidden by default:

- **Open state persists** across sessions in `localStorage` under
  `pinctl:editor-log-open` (`'1'`/`'0'`), so re-opening the same pipeline
  later remembers whether you had the drawer up.
- **Unread badge** — while closed, a small count badge on the LOG FAB tracks
  entries that arrived while hidden; it clears the moment the drawer opens.
- **200-entry cap** — the `editor` store's `liveLog` array is capped at 200
  entries (oldest dropped first); the panel renders them newest-first.
- Each row: time (mono, `HH:MM:SS`), the log node's label, and its value
  (mono, colored `--signal-bool`/`--signal-float` by type). A **Clear**
  button empties the buffer client-side only (nothing is sent to the
  server). The empty state points at what to do next: "Add a `log` node and
  enable the pipeline to see live values here."
- Fixed 200px height when open, docked below the canvas — the editor shell
  sizes the canvas as `flex: 1` around it rather than hard-coding a
  subtraction.

### Node rendering (`PipelineNode.vue`)

Each node is a card with a left input gutter, a centered content column,
and a right output gutter:

- **Header** — a small glyph distinguishing group at a glance (`▷` sources,
  `◆` transforms, `■` sinks) plus the type's display name (`.label`).
- **Config summary** — a compact one-line, type-specific rendering of the
  node's config (`compare` → `"lt 300"`, `device-read`/`device-write` → the
  resolved device name, `timer` → `"every 5000ms"`, …), computed client-side
  per node type. Suppressed (in favor of the live value below) once it would
  just be duplicating that live value verbatim — e.g. a `constant` node's
  summary *is* its output, so once debugging is live there's no reason to
  show the same literal twice.
- **Live value** — while [live debugging](#live-debugging) is active, the
  node's current output renders mono/tabular-nums, colored by type, and
  **pulses** (a brief opacity animation) on each tick where the value
  actually changed from the previous one — this is the freshness signal;
  there is no separate "fresh"/"stale" indicator dot (see below).
- **Status dot** — a single small dot, top-right corner, present only on
  **error** (red, from server validation) or **warning** (amber, missing
  required input or part of a client-detected cycle) — never green/gray for
  "live"/"stale". A node with neither has no dot at all.

### Port rendering

Ports are Vue Flow `Handle`s positioned by a CSS percentage, not fixed
pixel slots: the *i*-th of *n* ports on a side sits at `(i + 0.5) / n` of
the node's height, clamped a few pixels in from the top/bottom so a busy
side's outermost port never lands on the card's rounded corner. Inputs and
outputs are distributed **independently** — a node with one input and three
outputs centers its lone input at 50% while its outputs sit at 1/6, 1/2,
5/6, regardless of each other. A lone port on either side always centers
itself; it's never pinned to the top just because it's first.

Port labels (`.port-label`, the port's `id`, e.g. `in1`, `threshold`) live
in fixed 28px gutter columns on the outside of the node's content column —
a lane the content grid column never enters — so a label can never collide
with the node's title or summary text no matter how the node's height ends
up (port-count-driven or content-driven, whichever is taller).

Ports are colored by resolved value type (`PORT_COLORS`): amber for `bool`,
teal for `float64`, and a neutral `--ink-faint` grey for `any` — the
escape-hatch type on the `log` node's single input, which accepts either
type rather than committing to one so the strict type-checked connection
rule (see below) doesn't lock a `log` node out of half the graph.

### Edges and type-checking (`PipelineEdge.vue`)

`onConnect` runs a client-side mirror of the engine's edge rules (see
[Client-side validation mirroring](#client-side-validation-mirroring))
before accepting a connection — port type match (`any` matches anything),
target-already-connected, self-loop rejection, duplicate `(from, to)` —
all rejected instantly with a warning toast and no server round-trip.

Accepted edges render as a Bezier path, monochrome (`--hairline-strong`) at
rest — color and a traveling-dash "flow" animation are reserved for a
*running* pipeline's live value, so an idle graph reads as pure structure.
While debugging is active and the edge's source node has a current value:

- **Bool edges** render a small ring glyph at the path midpoint instead of
  the words "true"/"false" — a hollow ring when false, the same ring with a
  smaller filled disc inset inside it when true. The state change animates
  the inner disc's radius/opacity rather than flashing, and is inert under
  `prefers-reduced-motion` (the app's blanket reduced-motion rule zeroes all
  transition/animation durations).
- **Float64 edges** render a small mono, tabular-nums pill with the rounded
  current value.

**Server error mapping is an approximation, not exact.** The server's
validation error `field` is a `nodeId`, not an edge reference — the
engine has no concept of "this specific edge is invalid," only "this node
is." An edge is drawn with a dashed red overlay whenever *either* endpoint
node carries a server error (or both endpoints are part of a client-detected
cycle) — the closest available approximation to "this edge is implicated,"
not a precise per-edge server verdict. An earlier draft of this doc assumed
the server would return an edge-shaped error (`edges[2].to`); it doesn't.

### Save

**Save** (`PUT /pipelines/{id}`) sends the full current graph (`name`,
`tickMs`, `nodes`, `edges`), matching the API's full-replace semantics.

1. Save is disabled whenever any client-mirrored rule is violated (a cycle,
   an unconnected required input) — no point round-tripping a graph the
   client already knows is invalid.
2. While in flight, the Save button shows "Saving…" and is disabled; there's
   no optimistic local state to roll back (see
   [Pessimistic updates](#pessimistic-updates)).
3. **Success** clears the dirty flag, refreshes `updatedAt`/`enabled` from
   the response, and pushes any returned warnings as individual toasts, plus
   a "Pipeline saved" toast.
4. **`validation_error` (400)** — the response's per-node errors are mapped
   onto `editor.serverErrors` (keyed by node id) and rendered inline: a red
   node border + status dot, tooltip with the message, and the edge overlay
   described above. This does **not** also toast — see
   [Error toasts](#error-toasts). The graph is never reverted; edits stay on
   canvas so the user can fix and retry.

## Simulate mode

The editor's client of `POST /api/v1/simulate` ([18-simulation.md](./18-simulation.md)) —
"what does this graph do?" without enabling it on live hardware. Toggled by a
**Simulate** button in `EditorToolbar.vue`, immediately left of Enable/Disable
(disabled while simulating, with an "Exit simulate mode first" tooltip — the
two modes don't mix). Entering it writes nothing to `editor`'s graph state
(`nodes`/`edges`/`name`/`tickMs`/`dirty` are all untouched) and calls no save
endpoint; it only pauses this tab's own live `pipeline:{id}` WS subscription
so a live tick can't race a simulated frame for the same canvas — a pipeline
actually enabled on the server keeps running untouched, and leaving Simulate
mode re-subscribes if it's still enabled.

```
┌────────────────────────────────────────────────────────────────┬──────────┐
│  [name] tick[__]ms  tick 40 2026-…08:00:04.000  [Exit simulate] │ Simulate │
├───────────────────────────────────────────────────────────────┤│  panel   │
│                                                                  │──────────│
│           (Vue Flow canvas — same node/edge renderers,          │ Inputs   │
│            fed the selected simulated frame instead of a        │ Run      │
│            live tick)                                           │ Timeline │
│                                                                  │ Script   │
└───────────────────────────────────────────────────────────────┘└──────────┘
```

### `SimulatePanel.vue`

A docked sidebar (a flex sibling of `.canvas-wrapper`, like `LogPanel` docks
below it — not a node-anchored popover, since it has no single node to
anchor to), visible for as long as Simulate mode is active:

- **A permanent honesty line.** In the tool's own voice, always visible while
  simulating, not just a linked doc: *"This proves the logic, not the wiring.
  It cannot know that pin 0 is the RX line, that WiFi jitters, or that a
  relay's inrush resets the board."* Simulation proving the logic and saying
  nothing about the wiring is stated in both places deliberately
  ([18-simulation.md](./18-simulation.md#what-simulation-is-not)) — a user
  who mistakes a green simulation for a working install has been misled by
  the tool either way.
- **Inputs** — one row per device a `device-read` node in the current draft
  names, resolved from the `devices` store: `analog-in` → a 0–1023 slider
  with a mono readout, `digital-in` → a toggle. A control the user hasn't
  touched sends nothing in the request's `inputs` map — the panel does not
  pre-seed every device to `0` — so the endpoint's own `unseeded_input`
  warning is what surfaces the gap (rendered as an amber line naming the
  device(s)), rather than the client silently deciding a value on the user's
  behalf. A small "unseeded" tag marks an untouched control before the first
  run even happens.
- **Run controls** — `steps` (default 100, a number field capped at 2000 with
  the cap stated as a hint), the pipeline's own `tickMs` shown read-only in
  mono (simulate always uses the draft's configured tick, never a separate
  rate), and a **Run** button (disabled while a request is in flight or the
  graph fails client-side validation).
- **Timeline scrubber** — appears once a run has produced at least one frame:
  a range input over `frames`, ◀/▶ step buttons, and a play/pause button that
  advances at a fixed ~150ms-per-frame rate — deliberately not real-time,
  since a fast `tickMs` would make that unwatchable and a slow one would make
  it a long wait for nothing. The current frame's `tick` and its simulated
  `at` timestamp (mono) render both here and in the toolbar. **Keyboard:**
  `←`/`→` step one frame, `Space` toggles play/pause, `Home`/`End` jump to
  the first/last frame — active whenever focus isn't in a text field.
- **Scripted changes** — a small mono list of `{tick, deviceId, value}`
  entries (add via a compact device/tick/value row, not a keyframe editor),
  each editable in place and removable. Adding, editing, or removing an entry
  re-runs the simulation immediately, so the scrubber always reflects the
  current script.
- **Errors** — a `RunError` (`error.tick`/`error.nodeId`/`error.message`)
  stops the run at that tick: `frames` (and therefore the scrubber's range)
  simply ends there, the offending node gets the same `--signal-alert`
  border/dot a server validation error would (a dedicated check on
  `simResult.error.nodeId`, styled identically to — but tracked separately
  from — `serverErrors`, so exiting Simulate mode can't leave a stale error
  border behind), and the message renders in mono in the panel. A `400`
  validation failure (bad graph shape) instead renders through the exact
  same `serverErrors`/`mapValidationIssuesToNodes` path `Save` uses — the
  same inline node markers, not a second error UI.
- **Writes** — `device-write` nodes have no output port to reuse the node
  live-value display from, so their simulated pin value is a dedicated small
  readout fed by the frame's `writes` map, carried forward client-side
  (`lib/simulate.ts`'s `carryForwardWrites`): a device absent from a frame's
  `writes` keeps showing its last written value, and a small dot marks only
  the frame(s) where it actually changed — one signal, not a second color.

### Same rendering code, live or simulated

`PipelineNode.vue` and `PipelineEdge.vue` don't know which mode fed them.
Both read three `editor` store getters — `displayActive`, `displayNodeValues`,
`displayTick` — that resolve to the live `liveTick`/`liveNodeValues` while
debugging, or the scrubber's selected simulated frame while simulating; the
per-node value rendering, edge bool-ring glyph, float64 pill, and colors
downstream of that branch point are unmodified. This is deliberate, not
incidental: it's how a simulated frame and a live tick end up visually
indistinguishable, per [18-simulation.md](./18-simulation.md)'s "same engine,
or it lies" principle applied to the UI layer, not just the server. Node
staleness dimming (`--signal-warn` "board unreachable") is suppressed in
Simulate mode — there's no board to go unreachable.

## Client-side validation mirroring

The engine/store's real validation rules live in
[09-data-model.md](./09-data-model.md#validation-rules) and
[07-pipeline-engine.md](./07-pipeline-engine.md); `POST /pipelines/validate`
is the authoritative check. The editor mirrors a **subset** client-side for
instant feedback, and defers the rest, calling `validate` debounced (~400ms
after the last graph edit).

| Rule | Mirrored client-side? | Why |
|---|---|---|
| Edge port type match (`bool`↔`float64`, `any` matches both) | Yes — blocks the connection on drop | Purely local: both ports' types are already known from the node schema table in memory |
| Edge target already has an incoming edge | Yes — blocks on drop | Same |
| Self-loop edge | Yes — blocks on drop | Same |
| Duplicate edge `(from, to)` | Yes — blocks on drop | Local |
| Required input port unconnected | Yes — inline warning on the node, re-checked on every edge/config change | Derivable from the static schema + current edge list |
| `and`/`or` `inputCount` bounds (`>= 2`) | Yes — form-level `min` on the field | Static schema constraint |
| Cycle detection (topological sort) | Yes — recomputed on every edge change | Local computation over the in-memory graph; cheap at this node count, and easy to introduce by accident while wiring |
| `expression` empty text / empty / duplicate / malformed / reserved input names | Yes — inline messages in the config popover | Doesn't require parsing the expression body, only inspecting the input-row list |
| `deviceId` references an existing device with a compatible role | Partially — the `deviceRef` select is pre-filtered to compatible, currently-known devices | Not re-checked if a device is deleted by another browser tab while this editor is open (rare — LAN tool, usually one operator); the server's check still runs on save |
| Expression body syntax/semantics (parse errors, arity mismatches) | No — server-only | Requires the real `internal/expr` parser; not reimplemented client-side |
| Two `device-write` nodes across different pipelines targeting the same device | No — server-only, surfaced on `validate`/save | Requires seeing *other* pipelines' contents, which the editor doesn't load |

Design principle: mirror what's derivable from data the editor already holds
in memory (the static node schema table, the current graph, the loaded
device list); defer to the server anything that needs state the editor
doesn't own.

## Live debugging

When the editor is open on a pipeline whose `enabled` is `true`, it
subscribes to the `pipeline:{id}` WS topic (`editor.subscribeLive()`) and
tears the subscription down on navigating away, on disabling the pipeline,
or on `PipelineEditorView` unmount. Opening the editor on a **disabled**
pipeline does not subscribe — the canvas instead shows the "enable to see
live values" hint bar described in [Canvas and the add-node FAB](#canvas-and-the-add-node-fab).

- **Node live values and pulse** — see [Node rendering](#node-rendering-pipelinenodevue).
  A node's current value renders whenever `editor.liveTick !== null`, and
  the value text pulses on the tick it actually changes. This — not a
  color — is how freshness is conveyed; per-tick fidelity comes straight
  from the WS frame, with no client-side throttling for this topic.
- **Node status dots** are error/warning only, never a freshness indicator —
  see [Node rendering](#node-rendering-pipelinenodevue).
- **Edge live values** — bool ring glyph / float64 pill, plus a traveling-
  dash flow animation, all described in
  [Edges and type-checking](#edges-and-type-checking-pipelineedgevue).
- **Log drawer** — see [`LogPanel.vue`](#logpanelvue), fed by the same
  snapshot's optional `log` array.
- **Console live values** — the Devices table's Trace/Value columns
  subscribe to the (throttled, ~200ms per [10-api.md](./10-api.md#topics))
  `devices` topic for as long as `HomeView` is mounted, independent of
  whether any pipeline is enabled. A value is dimmed (`.stale`, via
  `lib/staleness.ts`'s `isStaleValue`) once it's gone longer than a
  generous multiple of that throttle window without an update — a soft
  signal that the owning board may have gone offline, rather than a hard
  cutoff that blanks the value.
- **WS reconnect with backoff** — the `ws` store owns exactly one socket and
  reconnects on close with exponential backoff (500ms → 1s → 2s → 5s,
  capped), re-sending `subscribe` for every topic with a nonzero refcount —
  the refcount table survives a reconnect, so components never need to
  react to one themselves. The header's connection indicator is the only
  always-visible reflection of this state (`Connected`/`Connecting…`/
  `Reconnecting…`/`Offline`).

## Pinia stores

| Store | Owns | Notes |
|---|---|---|
| `ws` | The single multiplexed `WebSocket`, `connectionState`, and a **refcounted topic subscription table** (`Map<topic, refcount>` + `Map<topic, Set<handler>>`) | `subscribe(topic, handler)`/`unsubscribe(topic, handler)` are called by any store/component that needs a topic; a wire `subscribe`/`unsubscribe` frame is only sent on a 0→1 / 1→0 refcount transition, so e.g. the console and the editor both wanting `pipelines` doesn't double-subscribe or drop the other's interest. Sends a `ping` every 30s while open. |
| `boards` | `BoardEntry[]` + loading/rescan flags | Actions: `fetch`, `rescan`, `rename(id, name)`, `subscribeLive()` (refcounted `boards` topic). `applyStatus` triggers a full re-`fetch` if an event names an unknown `boardId` (a newly rescanned board this store hasn't loaded yet), single-flighted against `loading`. |
| `devices` | `Device[]` + `liveValues: Record<deviceId, { value, timestamp }>` | Actions: `fetch`, `create`, `update`, `remove`, `manualWrite(id, value)` (also locally stamps `liveValues`, since output devices never appear on the `devices` topic — the board only reports inputs), `subscribeLive()`. |
| `pipelines` | `PipelineSummary[]` (list-view shape, no full graphs) | Actions: `fetch`, `create`, `remove`, `enable`, `disable`, `subscribeLive()` (`pipelines` topic). Full graphs are the `editor` store's concern, loaded per edit session. |
| `editor` | The pipeline currently open for editing: `nodes`/`edges`/`name`/`tickMs`/`enabled` (the Vue Flow working model), `dirty`, client-mirrored validation getters (`clientMissingInputs`, `clientCycleNodes`, `hasClientErrors`), `serverErrors`/`serverWarnings` from the last `validate`/save response, the live `liveTick`/`liveNodeValues`/`liveLog` while debugging, and [Simulate mode](#simulate-mode)'s `simulating`/`simInputs`/`simScript`/`simSteps`/`simResult`/`simFrameIndex` | Actions: `load(id)`, `addNode`, `moveNode`, `removeNode`, `updateNodeConfig` (also prunes edges wired to a port a config change removed, e.g. shrinking `and.inputCount`), `addEdge`, `removeEdge`, `scheduleValidate`/`validateNow` (debounced `POST /pipelines/validate`), `save()`, `subscribeLive()`, `teardown()` (`$reset()` + releases the live subscription — called on route leave and on `pipelineId` change), `enterSimulate()`/`exitSimulate()`, `runSimulation()`, `setSimFrameIndex`/`stepSimFrame`/`toggleSimPlay`. Getters `displayActive`/`displayNodeValues`/`displayTick` are the single branch point `PipelineNode.vue`/`PipelineEdge.vue` read through for either mode — see [Simulate mode](#simulate-mode). |

Legacy note: a `board` (singular) store also exists in `src/stores/board.ts`,
wrapping the v0.1 single-board `GET /board` endpoint. It predates the `boards`
(plural) collection store and isn't imported by any current view — the
console and editor both use `boards`. It's dead code, not a second live data
path.

All stores construct their REST calls through the shared `api/` module
rather than calling `fetch` directly, so the error-envelope-to-toast mapping
(see [Error toasts](#error-toasts)) is centralized in one place.

## UX details

### Unsaved-changes guard

A `beforeRouteLeave` guard on the editor route checks `editor.dirty`; if
true, it shows `window.confirm('Discard unsaved changes?')` before allowing
navigation away, and a `beforeunload` listener does the same for a tab
close/refresh. Node **position** changes count as dirty too (position is
persisted, per [09-data-model.md](./09-data-model.md)) — an unsaved drag
would otherwise be silently lost on navigation. This is the only place in
the app that guards navigation; every other mutation is an immediate,
single-field REST call with no local-only staged state to lose.

### Pessimistic updates

**Decision: pessimistic everywhere** — Boards rename, Devices CRUD and
manual write, Pipelines enable/disable/delete, and pipeline Save all show a
pending state (spinner/disabled control) and only reflect the change once
the server responds, rather than applying it locally first and rolling back
on failure.

Rationale: pinctl is a LAN tool talking to a Raspberry Pi on the same
network — round trips are single-digit milliseconds, not the latency
optimistic UI exists to paper over — and several mutations have outcomes
that aren't safely predictable client-side anyway (`enable` can come back
`paused` if the board is offline; manual write can `409`; save can fail
validation). Pessimistic keeps every UI state transition a direct, honest
reflection of a server response instead of something that sometimes has to
correct itself right after it renders.

### Empty states

Every list — the console's three tables, in the current build — degrades to
a single centered text row inside the same table body rather than a
dedicated icon-plus-button block; see [The console](#the-console-)'s empty
states subsection. There is no separate composite "nothing at all is
configured yet" onboarding screen distinct from the three individual empty
rows.

### Error toasts

Every non-2xx REST response is caught centrally in the `api/` module and
turned into a toast (`ToastHost.vue`) via the error envelope: `error.message`
as the toast body, `error.code` mapped to a severity (`error`/`warning`/
`info` — reflected as a colored left border, never a colored background),
and `error.details` rendered as a secondary mono line where it's
user-meaningful (e.g. the blocking pipeline for a `409` device-delete or
manual-write conflict). Toasts auto-dismiss after a few seconds. Pipeline-
save `400`s are the one case that does **not** also toast — those render
inline on the graph instead (see [Save](#save)); a toast on top would be
redundant with the inline markers on the one screen where the user is
already looking directly at the problem.

### Loading states

The console shows a "Reading instrument state…" skeleton line until its
initial `boards`/`devices` fetches resolve, rather than flashing empty-state
rows first.

### Accessibility floor

`:focus-visible` gets a visible 2px `--focus-ring` outline everywhere (never
suppressed to `none` outright — only the default browser focus ring is
replaced). A blanket `@media (prefers-reduced-motion: reduce)` rule collapses
every animation/transition duration to near-zero app-wide (dot pulses, the
edge flow animation, the bool-edge glyph transition, popover open/close,
toggle switches) rather than each component opting in individually. Layout
reflows for narrow viewports rather than requiring horizontal scroll: the
header wraps its tagline below 560px, `.data-table` rows stack into
label/value pairs below 640px, and the editor's config popover becomes a
full-width bottom sheet below 900px — together these keep the app usable
down to small-phone widths (~380px) without a horizontal scrollbar
appearing anywhere.

## Component tree (pipeline editor screen)

```
PipelineEditorView                      (route: /pipelines/:id)
├── EditorToolbar
│   ├── name field / tick field / live tick counter
│   ├── error-count button                (shown only when errors exist)
│   ├── simulate toggle                   (see Simulate mode)
│   ├── enable/disable toggle             (disabled while simulating)
│   └── Save button + dirty dot
├── canvas-wrapper
│   ├── debug-hint bar                    ("enable to see live values" / status)
│   ├── VueFlow                           (@vue-flow/core)
│   │   ├── PipelineNode × N              (node-types entry per schema type)
│   │   │   ├── node-gutter-left          (port labels)
│   │   │   ├── Handle × N (target)       (typed, bool/float64/any colored)
│   │   │   ├── node-content              (glyph, title, config summary, live value)
│   │   │   ├── node-status-dot           (error/warning only)
│   │   │   ├── Handle × N (source)
│   │   │   └── node-gutter-right
│   │   ├── PipelineEdge × N              (edge-types: 'pinctl')
│   │   │   ├── BaseEdge                  (monochrome at rest, flowing when live)
│   │   │   └── EdgeLabelRenderer         (bool ring glyph / float64 pill, live only)
│   │   ├── Background                    (dot grid)
│   │   └── Controls                      (zoom/fit, restyled)
│   ├── log-fab (LOG toggle, unread badge)
│   ├── add-node-fab (+)
│   └── CommandPalette                    (add-node mode)
├── ConfigPanel                           (popover, anchored to selected node's rect)
│   └── one field control per schema field (text/number/bool/enum/deviceRef/
│       weekdays/inputList/expression)
├── LogPanel                              (bottom drawer, toggled by log-fab)
└── SimulatePanel                         (docked sidebar, shown while simulating)
    ├── honesty line                      (always visible while simulating)
    ├── inputs                            (per device-read device, role-seeded control)
    ├── run controls                      (steps, tickMs readout, Run)
    ├── timeline scrubber                 (frames range + step/play + tick/at)
    └── scripted-change list              (add/edit/remove, re-runs on change)
```

Cross-cutting, not part of the visual tree: the view subscribes/unsubscribes
the `pipelines` WS topic (for the debug-hint status) on mount/unmount, and
loads the graph into the `editor` store on mount and on every `id` prop
change (`editor.load(props.id)`, preceded by `editor.teardown()`).

## Out of scope

| Item | Status |
|---|---|
| Dedicated Boards/Devices/Pipelines/Settings screens | Superseded by the single-console design — see [Screens and routing](#screens-and-routing). Not a gap to fill; the consolidation was deliberate. |
| Dark mode toggle | No in-app toggle. The UI follows the OS/browser's `prefers-color-scheme` automatically — system-preference-only. |
| i18n | English only, no translation infrastructure. |
| Auth screens (login, token entry) | None — matches the brief's "no auth in MVP." When the roadmap's optional single-password + token lands (see [00-design-brief.md](./00-design-brief.md)), it adds a login screen and a request-level token header; out of scope here. |
