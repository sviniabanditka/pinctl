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
