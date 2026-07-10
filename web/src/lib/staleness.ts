// The `devices` WS topic is throttled to ~200ms per docs/10-api.md; a
// value that hasn't updated in longer than a few multiples of that window
// suggests the underlying board went offline (docs/11-web-ui.md "Live
// debugging": "no devices update for a device in >3x the topic's ~200ms
// throttle interval").
const STALE_AFTER_MS = 3 * 200 * 5 // generous margin over the raw 3x throttle window

export function isStaleValue(timestamp: string | undefined, now: number = Date.now()): boolean {
  if (!timestamp) return false
  const t = Date.parse(timestamp)
  if (Number.isNaN(t)) return false
  return now - t > STALE_AFTER_MS
}

// A *paused* pipeline (docs/07-pipeline-engine.md "Degraded ticks") keeps
// ticking — the `pipeline:{id}` WS topic keeps arriving — but a tick that
// hits the unreachable device abandons evaluation right there, so nodes
// downstream of (or at) that read stop appearing in the tick snapshot
// while nodes upstream of it keep refreshing normally. There's no explicit
// per-node "stale" flag on the wire, so the editor's live-debug overlay
// infers it the same way isStaleValue does above: elapsed time since the
// node's value was last refreshed, just measured against the pipeline's
// own tick period instead of the devices topic's fixed throttle.
const TICK_STALE_MULTIPLIER = 5

export function isStaleTickValue(updatedAt: number | undefined, tickMs: number, now: number = Date.now()): boolean {
  if (updatedAt === undefined) return false
  return now - updatedAt > tickMs * TICK_STALE_MULTIPLIER
}
