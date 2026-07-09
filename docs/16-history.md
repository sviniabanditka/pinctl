# pinctl — Value history

pinctl records what its devices did, so a sensor's last week is a query rather
than a memory. Without it a value exists only while a browser tab is open:
the editor's sparkline holds sixty samples and forgets them on reload.

History is the **only** thing that lives in a database. Devices and pipelines
remain JSON files under the data dir — human-readable, git-diffable, restored
by copying a directory ([ADR-3](./03-architecture.md#adr-3-json-files-not-a-database)).
That ADR anticipated this: *"SQLite only if/when history/metrics land."* This
is that, and no more than that.

## Table of contents

- [Principles](#principles)
- [Storage](#storage)
- [What gets recorded](#what-gets-recorded)
- [Retention](#retention)
- [Query API](#query-api)
- [Configuration](#configuration)
- [SD cards](#sd-cards)
- [Failure modes](#failure-modes)

## Principles

1. **History observes; it never participates.** The recorder is one more
   `devicebus` subscriber. A failing or full database must never stall a
   pipeline tick, a board write, or the HTTP server.
2. **Samples only.** No config, no pipelines, no state the runtime needs to
   start. Delete `history.db` and pinctl comes up with everything intact,
   minus the past.
3. **Every writer is recorded.** Values reach the bus from the board poll and
   from `devicebus.WrapWriter` on `engine.DeviceIO`, so a pipeline turning an
   LED on is recorded exactly like a sensor moving
   ([15-mqtt-bridge.md](./15-mqtt-bridge.md#architecture)).
4. **The card is not free.** A Raspberry Pi writes to an SD card that wears
   out. Defaults are chosen to keep writes rare and batched, not to maximise
   fidelity.

## Storage

`<data_dir>/history.db`, SQLite via `modernc.org/sqlite` — a pure-Go driver, so
`CGO_ENABLED=0` cross-compilation to `linux/arm64` and `linux/arm` keeps
working. That constraint eliminated `mattn/go-sqlite3`.

```sql
CREATE TABLE samples (
    device_id TEXT    NOT NULL,
    ts        INTEGER NOT NULL,  -- unix milliseconds, UTC
    value     REAL    NOT NULL   -- bools are 0 or 1
) STRICT;

CREATE INDEX samples_device_ts ON samples (device_id, ts);

CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT) STRICT;  -- schema_version
```

Pragmas: `journal_mode=WAL` (a reader never blocks the writer),
`synchronous=NORMAL` (a crash can lose the last transaction — acceptable for
telemetry, and the alternative fsyncs the card on every batch),
`busy_timeout=5000`, `auto_vacuum=incremental`.

A `bool` is stored as `0.0`/`1.0` rather than in its own column: one table, one
index, and a bucketed average of a boolean is its duty cycle, which is a real
answer to a real question.

Writes are **batched**: samples accumulate in memory and flush inside one
transaction every `flush_interval` (default `5s`), or sooner if the buffer
exceeds 512 rows. A flush that fails is logged and dropped — telemetry is not
worth blocking the runtime for.

## What gets recorded

Every device on the bus: inputs as they are polled, outputs as they are
written. A sample is recorded when, since that device's last recorded sample:

- the value moved by at least `deadband` (raw ADC counts for `analog-in`;
  any change for booleans), **and** at least `min_interval` has passed; or
- `max_interval` has passed regardless of change — a heartbeat, so a flat line
  in a chart means "measured and unchanged" rather than "nothing was running".

This is the same shape of policy the MQTT bridge applies, and it exists for the
same reason: an analog pin never sits still, and every ADC wobble would
otherwise become a row forever. The two share `internal/sampling`.

Deleting a device deletes its samples. Renaming one keeps them: history is
keyed by the device's id, which never changes.

## Retention

Rows older than `retention` (default `168h`, seven days) are deleted hourly,
followed by an incremental vacuum bounded to a few hundred pages so the pass
never stalls. Retention is checked once at startup, too — a Pi that was off for
a month should not carry a month of stale rows.

There is no downsampling-into-coarser-tables tier. If seven days of five-second
samples is not enough, raise `retention` and watch the file; if it is far too
much, lower it. A rollup tier is a real feature with real bugs, and nobody has
asked for one yet.

## Query API

```
GET /api/v1/devices/{id}/history?from=<rfc3339>&to=<rfc3339>&bucket=<dur>&limit=<n>
```

| Param | Default | Meaning |
|---|---|---|
| `from` | `to` − 1h | Inclusive start |
| `to` | now | Exclusive end |
| `bucket` | none | Aggregate into fixed windows (`30s`, `5m`, …) |
| `limit` | `2000` (max `10000`) | Row cap |

Without `bucket`, raw samples are returned, newest-truncated at `limit`. With
it, each window yields `avg(value)`, plus `min`/`max` so a chart can draw a
band rather than lie about a spike it averaged away. An empty window is
omitted, not zero-filled — absence of data is not zero.

```json
{
  "deviceId": "dev_9zq3v7ab4c",
  "role": "analog-in",
  "from": "2026-07-09T12:00:00Z",
  "to": "2026-07-09T13:00:00Z",
  "bucket": "30s",
  "samples": [
    { "t": "2026-07-09T12:00:00Z", "v": 512.4, "min": 505, "max": 519 },
    { "t": "2026-07-09T12:00:30Z", "v": 511.9, "min": 507, "max": 516 }
  ]
}
```

`404` for an unknown device. `400` for an unparseable duration, a `from` after
`to`, or a `limit` over the cap. With history disabled the endpoint returns
`503 history_disabled` — an honest "this instance doesn't keep history" beats
an empty array that looks like a device nobody ever read.

## Configuration

```yaml
history:
  enabled: true            # the point of the feature; see "SD cards" below
  retention: 168h          # 7 days
  deadband: 8              # raw ADC counts; booleans ignore it
  min_interval: 5s         # floor between recorded samples, per device
  max_interval: 5m         # heartbeat: record even when unchanged
  flush_interval: 5s       # batch window; one transaction per flush
```

## SD cards

The defaults above record, per analog device, at most one row every five
seconds and at least one every five minutes: between ~300 and ~17k rows a day,
flushed in one transaction every five seconds. That is a few megabytes a week
and a handful of writes a minute — well within what a decent card tolerates.

Raise `min_interval` and `flush_interval`, or set `enabled: false`, on a card
you care about or a device you don't. Point `data_dir` at an SSD or a tmpfs if
you'd rather trade durability for the card's life.

## Failure modes

| Failure | Behaviour |
|---|---|
| Database cannot be opened at startup | Log it, run with history disabled. The runtime serves and controls hardware regardless — never a startup failure. |
| A flush fails (disk full, corruption) | Log, drop that batch, keep serving. Consecutive failures back off and stop retrying rather than filling the journal. |
| Disk fills | SQLite errors; batches drop. Retention keeps running, so space is reclaimed as rows age out. |
| `history.db` deleted while running | SQLite continues against the open handle until restart; the file returns on the next startup. Nothing else is affected. |
| Query spans millions of rows | `limit` caps the response; the index on `(device_id, ts)` keeps the scan bounded. |
