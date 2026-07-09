// Pure geometry/maths for HistoryChart.vue, kept out of the component so it
// can be unit-tested without mounting Vue or touching SVG. Nothing here
// knows about the DOM.

export interface HistorySamplePoint {
  t: string
  v: number
  min?: number
  max?: number
}

export interface YDomain {
  min: number
  max: number
}

// ---------------------------------------------------------------------------
// Duration parsing (Go-style: "15s", "1m", "5m", "30m", "1h", "7d")
// ---------------------------------------------------------------------------

const DURATION_UNIT_MS: Record<string, number> = {
  ms: 1,
  s: 1000,
  m: 60_000,
  h: 3_600_000,
  d: 86_400_000,
}

/** Parses a single-unit Go-style duration ("30s", "5m", "1h", "7d"). Returns
 * NaN for anything else — bucket strings the API returns are always this
 * shape, so a richer parser (compound durations like "1h30m") isn't needed. */
export function parseDurationMs(d: string): number {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h|d)$/.exec(d.trim())
  if (!m) return NaN
  return parseFloat(m[1]!) * DURATION_UNIT_MS[m[2]!]!
}

// ---------------------------------------------------------------------------
// Y scaling
// ---------------------------------------------------------------------------

const ANALOG_PAD_FRACTION = 0.05

/** Analog y-domain: the data's own min/max (using bucketed min/max when
 * present), padded ~5%. Never forced to include 0 — a sensor idling at 500
 * should not spend 98% of the chart's height on empty space below it. */
export function analogYDomain(samples: HistorySamplePoint[]): YDomain {
  if (samples.length === 0) return { min: 0, max: 1 }
  let lo = Infinity
  let hi = -Infinity
  for (const s of samples) {
    lo = Math.min(lo, s.min ?? s.v)
    hi = Math.max(hi, s.max ?? s.v)
  }
  if (lo === hi) {
    // Flat data: manufacture a span so the line doesn't collapse to a point.
    const span = Math.abs(lo) > 0 ? Math.abs(lo) * ANALOG_PAD_FRACTION : 1
    return { min: lo - span, max: hi + span }
  }
  const pad = (hi - lo) * ANALOG_PAD_FRACTION
  return { min: lo - pad, max: hi + pad }
}

/** Bool y-domain is always fixed [0, 1] — duty cycle and square waves both
 * live in that range and it must never rescale to the data. */
export function boolYDomain(): YDomain {
  return { min: 0, max: 1 }
}

/** Maps a value into a pixel y within [0, height], inverted (SVG y grows
 * downward, higher values draw higher). */
export function scaleY(v: number, domain: YDomain, height: number): number {
  const span = domain.max - domain.min || 1
  const t = (v - domain.min) / span
  return height - t * height
}

/** Maps an ISO timestamp into a pixel x within [0, width] given the query's
 * [from, to) span. */
export function scaleX(t: string, from: string, to: string, width: number): number {
  const fromMs = Date.parse(from)
  const toMs = Date.parse(to)
  const span = toMs - fromMs || 1
  return ((Date.parse(t) - fromMs) / span) * width
}

// ---------------------------------------------------------------------------
// Tick selection
// ---------------------------------------------------------------------------

/** "Nice" round-number value ticks (1/2/5 * 10^n steps) spanning a domain,
 * landing 3-4 ticks for a typical range. */
export function niceValueTicks(domain: YDomain, count = 4): number[] {
  if (domain.min === domain.max) return [domain.min]
  const range = domain.max - domain.min
  const rawStep = range / Math.max(count - 1, 1)
  const mag = Math.pow(10, Math.floor(Math.log10(rawStep)))
  const norm = rawStep / mag
  let step: number
  if (norm < 1.5) step = 1
  else if (norm < 3) step = 2
  else if (norm < 7) step = 5
  else step = 10
  step *= mag

  const start = Math.ceil(domain.min / step) * step
  const ticks: number[] = []
  for (let v = start; v <= domain.max + step * 1e-9; v += step) {
    ticks.push(Math.round(v / step) * step)
  }
  return ticks
}

/** Evenly spaced time ticks across [fromMs, toMs] — deliberately simple
 * (not calendar-aligned) since the span is always known and short-ish
 * (max query window is bounded by the range presets, hours to a week). */
export function timeTicks(fromMs: number, toMs: number, count = 5): number[] {
  if (count <= 1 || fromMs === toMs) return [fromMs]
  const step = (toMs - fromMs) / (count - 1)
  return Array.from({ length: count }, (_, i) => fromMs + step * i)
}

function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

const ONE_DAY_MS = 86_400_000

/** Time-tick label format, adapted to the span: `HH:MM` within a day,
 * `DD/MM` beyond it. */
export function formatTimeTick(ms: number, spanMs: number): string {
  const d = new Date(ms)
  if (spanMs <= ONE_DAY_MS) {
    return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`
  }
  return `${pad2(d.getDate())}/${pad2(d.getMonth() + 1)}`
}

// ---------------------------------------------------------------------------
// Gap detection
// ---------------------------------------------------------------------------

/** Gap threshold in ms: bucketed data uses 1.5x the bucket duration (an
 * omitted window is exactly one missing bucket); raw data has no fixed
 * cadence, so fall back to 1.5x the median spacing between the samples
 * actually present. */
export function gapThresholdMs(samples: HistorySamplePoint[], bucketMs?: number): number {
  if (bucketMs !== undefined && !Number.isNaN(bucketMs)) return bucketMs * 1.5
  if (samples.length < 2) return Infinity
  const diffs: number[] = []
  for (let i = 1; i < samples.length; i++) {
    diffs.push(Date.parse(samples[i]!.t) - Date.parse(samples[i - 1]!.t))
  }
  diffs.sort((a, b) => a - b)
  const median = diffs[Math.floor(diffs.length / 2)]!
  return median * 1.5
}

/** Splits samples (assumed sorted by time) into contiguous runs, breaking
 * wherever the gap to the previous sample exceeds `thresholdMs`. A chart
 * draws each run as its own path segment and never draws a line between
 * them — a gap is real absence, not a value to interpolate across. */
export function segmentByGap(samples: HistorySamplePoint[], thresholdMs: number): HistorySamplePoint[][] {
  if (samples.length === 0) return []
  const segments: HistorySamplePoint[][] = [[samples[0]!]]
  for (let i = 1; i < samples.length; i++) {
    const prev = samples[i - 1]!
    const cur = samples[i]!
    if (Date.parse(cur.t) - Date.parse(prev.t) > thresholdMs) {
      segments.push([cur])
    } else {
      segments[segments.length - 1]!.push(cur)
    }
  }
  return segments
}

// ---------------------------------------------------------------------------
// Bool rendering mode
// ---------------------------------------------------------------------------

export type BoolRenderMode = 'square' | 'duty'

/** Raw bool samples draw as a square wave (digital signals look digital);
 * bucketed bool samples draw as a filled duty-cycle area (the bucket average
 * of a bool is its duty cycle in [0,1], a genuinely different quantity that
 * must not be drawn as if it were a smooth analog trace). */
export function boolRenderMode(bucketed: boolean): BoolRenderMode {
  return bucketed ? 'duty' : 'square'
}

// ---------------------------------------------------------------------------
// Range presets (DeviceHistorySheet.vue)
// ---------------------------------------------------------------------------

export interface RangePreset {
  id: '1h' | '6h' | '24h' | '7d'
  label: string
  spanMs: number
  /** Auto-selected so the span yields ~200-400 points. */
  bucket: string
}

/** `bucket` is picked per range so a query returns roughly 200-400 points:
 * 1h -> 15s (240 pts), 6h -> 1m (360 pts), 24h -> 5m (288 pts), 7d -> 30m
 * (336 pts). */
export const RANGE_PRESETS: RangePreset[] = [
  { id: '1h', label: '1h', spanMs: 3_600_000, bucket: '15s' },
  { id: '6h', label: '6h', spanMs: 6 * 3_600_000, bucket: '1m' },
  { id: '24h', label: '24h', spanMs: 24 * 3_600_000, bucket: '5m' },
  { id: '7d', label: '7d', spanMs: 7 * 86_400_000, bucket: '30m' },
]

export function rangePreset(id: string): RangePreset {
  return RANGE_PRESETS.find((r) => r.id === id) ?? RANGE_PRESETS[0]!
}
