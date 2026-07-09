# pinctl — Testing Strategy

This document describes how pinctl is tested: the test pyramid, the fake
board used to simulate hardware deterministically, the (optional,
non-CI) hardware-in-the-loop plan, frontend testing, and CI. It follows
[00-design-brief.md](./00-design-brief.md) and the component boundaries in
[03-architecture.md](./03-architecture.md); nothing here changes those
boundaries — it only tests them.

## Table of contents

- [Test pyramid](#test-pyramid)
- [Engine simulation tests](#engine-simulation-tests)
- [Fake board](#fake-board)
- [Store tests](#store-tests)
- [API tests](#api-tests)
- [WebSocket tests](#websocket-tests)
- [Hardware-in-the-loop (manual, not CI)](#hardware-in-the-loop-manual-not-ci)
- [Frontend testing](#frontend-testing)
- [CI](#ci)
- [Coverage philosophy](#coverage-philosophy)

## Test pyramid

pinctl's architecture (single binary, clean package boundaries, no external
services) makes most of the pyramid's weight sit at the unit and
in-process-integration level — there is no database, no network service to
mock, and the one genuinely untestable-in-CI dependency (a real Arduino)
is fully abstracted behind the `board.Session` interface.

| Layer | What it covers | Runs in CI |
|---|---|---|
| Unit | Individual nodes, graph validation, topo sort, value type checks | Yes |
| Engine simulation | Whole pipelines against a fake board + injectable clock, tick by tick | Yes |
| Store | JSON round-trip, atomic writes, schema versioning/migration | Yes |
| API | REST + WS handlers against a real `engine`/`store` wired to fake boards (`httptest`) | Yes |
| Frontend unit | Pinia stores, graph-diffing/validation logic in `web/` | Yes |
| Frontend component | A handful of key components (node config forms, validation display) | Yes |
| Hardware-in-the-loop | Real Uno running StandardFirmata, manual checklist | No (manual only) |
| End-to-end (browser) | Not present in MVP | No |

The shape is intentionally unit/simulation-heavy and E2E-light: pinctl's
riskiest logic (tick scheduling, DAG evaluation order, hot-reload
correctness) is exactly the part a fake board and an injectable clock can
exercise deterministically, without flakiness from real timing or a real
serial port.

## Engine simulation tests

The engine (`internal/engine`) is the highest-value thing to test
thoroughly, because it's where FR-PIPE-EXEC-* and NFR-PERF-* live, and
because a bug here is a silent behavior change in someone's physical
automation, not a crash.

Simulation tests build a real `engine.Engine` wired to a
[fake board](#fake-board) and an injectable clock instead of `time.Ticker`,
so a test can advance time deterministically — "one tick" is a function
call, not a `time.Sleep`.

Pattern: table-driven scenarios, one row per tick, asserting on the fake
board's pin writes and/or the pipeline's `TickSnapshot`. The README's
LDR→LED example becomes the canonical scenario:

```go
func TestLDRTurnsOnLEDBelowThreshold(t *testing.T) {
    fb := fakeboard.New(fakeboard.WithAnalogSequence(pinLDR, []int{
        900, 900, 300, 300, 300, 900, // dark, dark, ... light again
    }))
    clk := simclock.New()
    eng := engine.New(fb, engine.WithClock(clk))

    pipeline := buildLDRToLEDPipeline(t, threshold: 512, hysteresis: 20)
    require.NoError(t, eng.Load(ctx, []model.Pipeline{pipeline}))

    cases := []struct {
        tick      int
        wantLED   bool
    }{
        {0, false}, // 900 > threshold: dark ⇒ LED... wait, see node semantics
        {2, true},  // 300 < threshold: bright enough to trip
        {5, true},  // hysteresis keeps LED on until well above threshold again
    }
    for _, tc := range cases {
        clk.AdvanceTicks(tc.tick - clk.Ticks())
        got, ok := fb.DigitalPin(pinLED)
        require.True(t, ok)
        assert.Equal(t, tc.wantLED, got, "tick %d", tc.tick)
    }
}
```

(Exact node semantics/threshold direction are defined in
[08-node-catalog.md](./08-node-catalog.md); the example above illustrates
the *test shape*, not the final assertions.)

What engine simulation tests cover:

- Node evaluation order matches topological order (FR-PIPE-EXEC-2) —
  assert via a node that records its own eval order given a diamond-shaped
  DAG.
- Per-tick device writes are coalesced, never more than once per pin per
  tick (FR-PIPE-EXEC-6).
- A fault in one node/pipeline doesn't stop other pipelines
  (FR-PIPE-EXEC-5, NFR-REL-2) — inject a panicking node, assert sibling
  pipelines keep advancing.
- Hot-reload (`Reload`) swaps a running pipeline's graph without dropping
  ticks or affecting other pipelines (FR-PIPE-EDIT-6).
- Board-offline pausing: fake board reports `ErrBoardOffline`, assert the
  pipeline marks affected nodes stale rather than erroring the whole tick
  (per [03-architecture.md](./03-architecture.md#failure-modes--handling)).
- Configurable tick rate is honored (NFR-PERF-1) — the simulated clock
  makes this a fast, exact assertion instead of a timing-sensitive one.

## Fake board

`internal/board` exposes `Session` as an interface (see
[03-architecture.md](./03-architecture.md#internalboard)) specifically so
tests never need a real serial port. `internal/board/fakeboard` (test-only
package, or `_test.go`-internal — implementation detail) implements it
in-memory:

```go
// fakeboard implements board.Session and board.Manager entirely in memory.
type Board struct {
    // pin state, guarded by a mutex — no goroutine/channel plumbing needed
    // since there's no real serial port to own.
}

func New(opts ...Option) *Board
func WithAnalogSequence(pin int, values []int) Option // scripted reads
func WithCapabilities(caps board.PinCapabilities) Option

func (b *Board) DigitalWrite(ctx context.Context, pin int, high bool) error
func (b *Board) PWMWrite(ctx context.Context, pin int, value uint8) error
func (b *Board) PinState(pin int) (board.PinValue, bool)

// Test-only accessors, not part of board.Session:
func (b *Board) DigitalPin(pin int) (bool, bool)   // assert on writes
func (b *Board) SetAnalog(pin int, v int)           // drive a value manually
func (b *Board) SetOffline(offline bool)             // simulate disconnect
```

Design points:

- **Scriptable analog sequences** let a test drive "sensor gets darker over
  N ticks" without a real clock or a real LDR — `WithAnalogSequence`
  advances one value per call to whatever the engine's read path calls
  (`PinState`), or per simulated tick if that's a cleaner contract; the
  exact advance-trigger is an implementation detail resolved when
  `internal/engine`'s clock injection is built.
- **`SetOffline`** simulates a board disconnect without touching a real
  port, so reconnect/pause behavior (NFR-REL-1, NFR-REL-5) is testable
  without hardware.
- The fake implements the same `board.Manager`/`board.Session` interfaces
  real code depends on, so API tests (below) can wire a fake board all the
  way up through `engine` and `internal/api` with no production code path
  skipped.

## Store tests

`internal/store` tests run against a real temp directory on disk (no
mocking a filesystem — it's cheap and catches real `os` edge cases):

- **Round-trip:** save a device/pipeline, reload, assert deep equality.
- **Atomic writes:** kill/interrupt a write mid-flight (e.g. by writing to
  a temp file and asserting the original is untouched until rename) to
  cover FR-PERSIST-3/NFR-REL-4 — this is more a property test on the
  temp-file-then-rename implementation than a true power-loss simulation,
  since Go can't literally pull power; the assertion is "the original file
  is never observed in a partially-written state."
- **Malformed JSON:** a hand-crafted invalid file in the data dir is
  rejected with a clear error, and — per
  [06-runtime.md](./06-runtime.md#error-philosophy) — does not prevent
  other valid definitions in the same directory from loading
  (FR-PERSIST-4).
- **Schema versioning:** a fixture at each historical `schemaVersion`
  migrates forward correctly and round-trips through validation (see
  [06-runtime.md](./06-runtime.md#versioning-and-compatibility-for-pipeline-json)).
- **Validation:** cycle detection, dangling edges, and edge type mismatches
  (`bool` vs `float64`) are rejected at `SavePipeline` with field-level
  errors (FR-PIPE-EDIT-4).

## API tests

`internal/api` tests use `net/http/httptest` against a **real** `engine`
and `store` (temp dir), wired to one or more [fake boards](#fake-board) —
not mocks of `engine`/`store` themselves. This is a deliberate choice: the
value of API tests here is catching wiring bugs across package boundaries
(does a REST save actually trigger `Engine.Reload`? does a validation
error from `store` come back as a 400 with the right shape?), which a
mocked `engine`/`store` would hide.

Covers:

- CRUD REST endpoints for devices/pipelines, including validation error
  responses (FR-PIPE-EDIT-4, NFR-USE-2).
- Enable/disable pipeline reflected in engine state within the same
  request-response cycle.
- Board state endpoint reflects fake-board connect/offline transitions
  (FR-BOARD-5, FR-BOARD-6).
- The data-flow walkthrough in
  [03-architecture.md](./03-architecture.md#a-editor-saves-a-pipeline--validation--store--engine-hot-reload)
  end to end: PUT pipeline → validate → persist → reload, asserted via the
  fake board's observed pin writes on the next simulated tick.

## WebSocket tests

WS tests use `httptest.NewServer` with a real WS client dialing in (e.g.
`nhooyr.io/websocket` or the standard library's test helpers), against the
same real-engine/fake-board setup as API tests:

- Live pin values arrive within one tick of a fake-board value change
  (NFR-PERF-6 — assertable exactly here since the simulated clock removes
  timing flakiness).
- `TickSnapshot` frames match `Engine.Subscribe`'s per-node values.
- A slow/non-reading client does not block the tick loop or other
  subscribers — assert by having one test client stop reading and
  confirming a second client and the engine's tick count keep advancing
  (backs up the "drop, don't block" design in
  [03-architecture.md](./03-architecture.md#concurrency-model)).
- Board connect/disconnect events are relayed over the same connection.
- Clean close on server shutdown (graceful-shutdown sequence in
  [06-runtime.md](./06-runtime.md#graceful-shutdown)).

## Hardware-in-the-loop (manual, not CI)

Real hardware is never required for CI to pass — it's flaky, unavailable
on GitHub-hosted runners, and everything it would catch beyond the fake
board is specifically *StandardFirmata-on-real-USB* behavior. That gets a
manual checklist instead, run before tagging a release and whenever
`internal/board`'s serial/Firmata code changes meaningfully.

**Manual HIL checklist** (one Arduino Uno, StandardFirmata flashed,
LDR on A0, LED on D13 — the README's example wiring):

1. Fresh `pinctl` start with the Uno plugged in: board reaches
   `connected` state within a few seconds; capabilities reported match the
   Uno's real pin map.
2. Build/load the LDR→LED pipeline; covering the sensor turns the LED on
   within one tick period, visually.
3. Unplug the USB cable mid-run: board transitions to `disconnected`
   without a crash; pipeline pauses (no LED flicker/false writes).
4. Replug: board reconnects automatically (reconnect-with-backoff), pin
   capabilities re-queried, pipeline resumes without manual re-enable.
5. Restart `pinctl` (systemd-style `SIGTERM`) with the board still
   plugged in: process exits cleanly within the shutdown deadline, no
   corrupted JSON in the data dir afterward.
6. Two Ardunios plugged in simultaneously: both connect independently;
   unplugging one doesn't disturb the other's pipeline.
7. Long-running soak (multi-hour, LED-blink pipeline left running):
   no memory growth, no missed-tick warnings in the log beyond
   occasional/expected jitter.
8. Confirm the Arduino's expected DTR-triggered reset on serial connect
   is visible (LED blink pattern on boot) and pinctl's Firmata handshake
   tolerates it (see [troubleshooting](./14-deployment.md#troubleshooting)).

This checklist is tracked as a release-time task (e.g. a PR template
checkbox or an issue template), not a CI job.

## Frontend testing

`web/` uses **Vitest** for unit tests, run in CI:

- **Pinia store tests:** the bulk of frontend test value — pipeline/device
  store mutations, optimistic-update rollback on a failed save, WS message
  handling (applying a `TickSnapshot` frame to store state), client-side
  graph validation logic that mirrors backend rules for inline error
  display (NFR-USE-2).
- **Component tests (minimal):** a small set of components with real logic
  worth isolating — the node config form (type-specific fields, validation
  messages) and the live-value overlay renderer. Presentational/layout
  components are not unit tested; they're covered indirectly by whatever
  manual QA happens per release.

**No end-to-end (browser) tests in MVP.** Rationale: E2E tests
(Playwright/Cypress against a running pinctl + browser) would be the only
thing that exercises Vue Flow's actual drag-and-drop canvas, but they are
also the most expensive tests to write and maintain relative to the
project's size (one contributor to a handful, no dedicated QA), and they'd
need either a real or fake board wired into a full running binary just to
get past the "add a device" step. The store/logic-level Vitest coverage
plus the manual HIL checklist together cover the behaviors that matter most
(data correctness, live updates) without that cost. Revisit if the project
grows enough contributors that manual regression testing of the editor UI
becomes the bottleneck.

## CI

GitHub Actions, one workflow per concern:

| Job | What it does |
|---|---|
| `go-test` | `go vet ./...`, `go test ./...` (race detector on) on `linux/amd64` — the fast, everyday feedback loop. |
| `go-build-cross` | `GOOS=linux GOARCH=arm64 go build ./...` and `GOARCH=arm GOARM=7 go build ./...` — compile-only checks (no test execution; no arm64/arm runners) that the two production targets (`02-requirements.md` FR-DEPLOY-1) actually build. |
| `golangci-lint` | Standard Go linting (`golangci-lint run`), config committed at the repo root. |
| `web-build` | `npm ci && npm run build` in `web/` — catches TypeScript/build errors; also `npm run test` (Vitest) for the frontend unit suite. |
| `release` | Tag-triggered: builds the frontend, embeds `web/dist` via `embed.FS`, cross-compiles the `linux/arm64` and `linux/arm` binaries, and attaches them (plus checksums) to the GitHub release. See [14-deployment.md](./14-deployment.md) for the install script that consumes these artifacts. |

All jobs above `release` run on every PR; `release` runs only on tag push.
There is no hardware-in-the-loop job — see
[above](#hardware-in-the-loop-manual-not-ci).

## Coverage philosophy

No enforced coverage gate in v0.x — a hard percentage threshold on a
young, single/small-contributor project tends to produce tests written to
satisfy the number rather than to catch real bugs. Instead, coverage
expectations are qualitative and layered by risk:

| Area | Expectation | Why |
|---|---|---|
| `internal/engine` | High — every node type, validation rule, and concurrency edge case (Reload-while-ticking, panic recovery) should have a simulation test. | This is where a silent bug becomes a wrong physical outcome (a light that doesn't come on); it's also the most deterministically testable part of the system via the fake board + injectable clock. |
| `internal/board`'s Firmata/protocol logic | High for the parts that don't require a real port (message encode/decode, capability parsing, reconnect/backoff state machine) — tested against a fake `io.ReadWriter` at the protocol level, separate from the fake `board.Session` used elsewhere. | Protocol bugs are subtle and hard to catch via HIL testing alone; they deserve unit-level coverage of the wire format itself. |
| `internal/store` | High — atomic writes and schema migration are exactly the kind of code where "it worked in manual testing" hides real data-loss bugs. | FR-PERSIST-3/4 and NFR-REL-4 are explicit non-negotiable requirements. |
| `internal/api` handlers | Moderate — happy path plus the documented error shapes (400s, board-offline responses) per endpoint; not every permutation of malformed input. | Handlers are thin translation layers over already-well-tested `engine`/`store`; most of their risk is caught by API-level integration tests, not handler unit tests. |
| `web/` components | Low/selective — only components with real logic (see [Frontend testing](#frontend-testing)). | Time is better spent on store logic, which is where frontend bugs actually cause incorrect behavior rather than visual glitches. |

Revisit this table once the project has enough external contributors that
"what's expected to be tested" needs to be enforced by CI rather than
reviewer judgment.
