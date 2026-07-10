# pinctl — Pipeline simulation

A visual editor whose only way to answer "what does this graph do?" is to
enable it on live hardware is not much of an editor. Simulation runs a pipeline
against fake devices and a fake clock, so you can drag a sensor value and watch
the graph respond — no board, no risk, and time as fast as you like.

## Principles

1. **The same engine, or it lies.** Simulation runs the real
   `engine.Manager` with the real nodes. A second evaluator written for the
   editor would drift from the one that drives the relay, and the moment it
   drifted it would become worse than useless: confidently wrong.
   `internal/engine` already takes an injected `Clock`, `TickerFactory`, and
   `DeviceIO` — simulation is those three, faked.
2. **Pure function.** Given a graph, a device set, an input script, and a tick
   count, the result is deterministic and reproducible. Same input, same
   frames, every time.
3. **No hardware, ever.** A simulated run cannot reach `internal/board`. It is
   given its own `DeviceIO` and there is no path from it to a pin.
4. **Unsaved graphs simulate.** The editor's draft is what a user wants to
   understand, so the graph travels in the request body. Simulation never reads
   or writes the store's pipelines.

## Time

The fake clock starts at a caller-supplied instant (default: an arbitrary fixed
epoch, so runs are reproducible) and advances exactly `tickMs` per step. Nodes
that ask the time — `timer`, `clock`, `delay`, `debounce` — see that clock and
nothing else.

So a `delay` of ten minutes resolves in six hundred steps of a 1s tick, in a
few milliseconds of wall time. This is the point: you can watch what a pipeline
does overnight without waiting overnight.

## The API

```
POST /api/v1/simulate
```

```json
{
  "pipeline": { "name": "…", "tickMs": 100, "nodes": [ … ], "edges": [ … ] },
  "steps": 200,
  "startTime": "2026-07-10T08:00:00Z",
  "inputs": {
    "dev_9zq3v7ab4c": 512
  },
  "script": [
    { "tick": 50,  "deviceId": "dev_9zq3v7ab4c", "value": 120 },
    { "tick": 120, "deviceId": "dev_9zq3v7ab4c", "value": 900 }
  ]
}
```

- `pipeline` is a full pipeline body, exactly as `PUT /pipelines/{id}` takes it.
  It need not exist in the store. Its `enabled` flag is ignored.
- `inputs` seeds every readable device's value at tick 0. A device the graph
  reads but `inputs` omits starts at its type's zero, and the response says so
  in `warnings` — a silent zero is how a simulation lies.
- `script` changes an input at a given tick, so you can express "the sensor
  drops at tick 50" without driving the UI by hand.
- `steps` is capped (2000) and `tickMs` is taken from the pipeline body.

Devices are resolved from the store for their **role and pin metadata only** —
that is what gives a `device-write` node its type. Nothing is read from or
written to hardware. A device id the store doesn't know is a `400`.

### Response

```json
{
  "tickMs": 100,
  "warnings": [ { "code": "unseeded_input", "deviceId": "dev_…" } ],
  "frames": [
    {
      "tick": 1,
      "at": "2026-07-10T08:00:00.100Z",
      "nodes": { "n1": 512, "n2": false },
      "writes": { "dev_k2n8h5wtxy": false },
      "log": []
    }
  ]
}
```

`frames[i].nodes` is the same per-node value map the live debug WebSocket
sends (`pipeline:{id}`), so the editor renders a simulated frame with exactly
the code that renders a live one. `writes` is what *would* have reached a pin.
`log` carries `log` node output.

Validation failures return the same `400` shape as `POST /pipelines/validate`.
A node that errors mid-run (division by zero) ends the run early: the response
carries the frames up to that point plus an `error` naming the tick and node.
That is more useful than a `500`, because seeing the last good frame is how you
find out why.

## What simulation is not

**It is not a physics model.** A simulated `device-write` to a servo records an
angle; it does not model inertia, stall current, or the board browning out. A
pipeline that works in simulation can still be wrong about the world.

**It is not a substitute for a hardware run.** It cannot show you that pin 0 is
the RX line, that WiFi jitters, or that a relay's inrush resets the board.
Simulation proves the *logic*, and says nothing about the *wiring*.

Both of these belong in the UI's copy, not just here: a user who mistakes a
green simulation for a working install has been misled by the tool.

**The UI says so too.** The web editor's Simulate mode carries this as a
permanent, quiet line in its side panel, in the tool's own voice, not just in
this doc — see [11-web-ui.md](./11-web-ui.md#simulate-mode).

## The UI

The web editor (`/pipelines/:id`) has a **Simulate mode**, toggled from the
toolbar, that is the client of this endpoint: it POSTs the editor's own
unsaved draft (never the saved pipeline) and renders the response.

- **Inputs** — one control per device a `device-read` node names, seeded from
  the device's role (`analog-in` → slider 0–1023, `digital-in` → toggle). A
  control the user never touches sends nothing for that device, so the
  server's own `unseeded_input` warning is what tells them, rather than the
  client silently defaulting it and hiding the question.
- **Scripted changes** — a small mono list of `{tick, deviceId, value}`
  entries, editable/removable, built while scrubbing a run rather than
  requiring the whole script to be authored up front.
- **The timeline scrubber** — a range input over `frames`, plus step/play
  controls. Frames a run's `error` cut short simply aren't there to scrub
  past — the scrubber's range is `frames.length`, not the requested `steps`.
- **Rendering** — a selected frame's `nodes`/`writes` feed the *same*
  component code that renders a live `pipeline:{id}` tick, so a simulated
  frame and a live one are visually indistinguishable except for the mode
  indicator itself.

Full UI detail (keyboard map, write carry-forward, component structure) is in
[11-web-ui.md](./11-web-ui.md#simulate-mode).

## Determinism

Runs are reproducible because everything that could vary is injected: the
clock, the tick source, and device values. `internal/engine`'s topological sort
already breaks ties by node id for the same reason. Nothing in a simulated run
consults wall time, randomness, or the network.

This is also what makes simulation testable as a fixture: a graph plus an input
script plus an expected frame sequence is a regression test for node semantics
that reads like documentation.
