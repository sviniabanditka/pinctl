import { describe, expect, it } from 'vitest'
import {
  analogYDomain,
  boolRenderMode,
  boolYDomain,
  formatTimeTick,
  gapThresholdMs,
  niceValueTicks,
  parseDurationMs,
  rangePreset,
  scaleX,
  scaleY,
  segmentByGap,
  timeTicks,
  type HistorySamplePoint,
} from './chart'

describe('parseDurationMs', () => {
  it('parses single-unit Go-style durations', () => {
    expect(parseDurationMs('15s')).toBe(15_000)
    expect(parseDurationMs('1m')).toBe(60_000)
    expect(parseDurationMs('5m')).toBe(300_000)
    expect(parseDurationMs('30m')).toBe(1_800_000)
    expect(parseDurationMs('1h')).toBe(3_600_000)
    expect(parseDurationMs('7d')).toBe(604_800_000)
  })

  it('returns NaN for garbage', () => {
    expect(parseDurationMs('bogus')).toBeNaN()
    expect(Number.isNaN(parseDurationMs(''))).toBe(true)
  })
})

describe('analogYDomain', () => {
  it('pads the data range ~5% without forcing zero', () => {
    const samples: HistorySamplePoint[] = [
      { t: 't1', v: 500 },
      { t: 't2', v: 520 },
      { t: 't3', v: 480 },
    ]
    const domain = analogYDomain(samples)
    expect(domain.min).toBeLessThan(480)
    expect(domain.max).toBeGreaterThan(520)
    // 5% of the 40-wide span is 2, so padding should be small, not zero-anchored.
    expect(domain.min).toBeGreaterThan(400)
  })

  it('uses bucketed min/max when present, not just v', () => {
    const samples: HistorySamplePoint[] = [{ t: 't1', v: 500, min: 100, max: 900 }]
    const domain = analogYDomain(samples)
    expect(domain.min).toBeLessThanOrEqual(100)
    expect(domain.max).toBeGreaterThanOrEqual(900)
  })

  it('never collapses to a zero-span domain on flat data', () => {
    const samples: HistorySamplePoint[] = [{ t: 't1', v: 512 }]
    const domain = analogYDomain(samples)
    expect(domain.max).toBeGreaterThan(domain.min)
  })

  it('defaults to [0,1] for empty samples', () => {
    expect(analogYDomain([])).toEqual({ min: 0, max: 1 })
  })
})

describe('boolYDomain', () => {
  it('is always fixed [0,1]', () => {
    expect(boolYDomain()).toEqual({ min: 0, max: 1 })
  })
})

describe('scaleY', () => {
  it('maps the domain max to y=0 and domain min to y=height (SVG y grows down)', () => {
    const domain = { min: 0, max: 100 }
    expect(scaleY(100, domain, 200)).toBeCloseTo(0)
    expect(scaleY(0, domain, 200)).toBeCloseTo(200)
    expect(scaleY(50, domain, 200)).toBeCloseTo(100)
  })
})

describe('scaleX', () => {
  it('maps from -> 0 and to -> width', () => {
    const from = '2026-07-09T12:00:00Z'
    const to = '2026-07-09T13:00:00Z'
    expect(scaleX(from, from, to, 600)).toBeCloseTo(0)
    expect(scaleX(to, from, to, 600)).toBeCloseTo(600)
    expect(scaleX('2026-07-09T12:30:00Z', from, to, 600)).toBeCloseTo(300)
  })
})

describe('niceValueTicks', () => {
  it('produces 3-4 round ticks for a typical range', () => {
    const ticks = niceValueTicks({ min: 0, max: 1023 }, 4)
    expect(ticks.length).toBeGreaterThanOrEqual(3)
    expect(ticks.length).toBeLessThanOrEqual(5)
    // Ticks should be evenly spaced round numbers.
    for (let i = 1; i < ticks.length; i++) {
      expect(ticks[i]! - ticks[i - 1]!).toBeCloseTo(ticks[1]! - ticks[0]!)
    }
  })

  it('handles a zero-span domain', () => {
    expect(niceValueTicks({ min: 5, max: 5 })).toEqual([5])
  })
})

describe('timeTicks / formatTimeTick', () => {
  it('spaces ticks evenly across the span', () => {
    const ticks = timeTicks(0, 1000, 5)
    expect(ticks).toEqual([0, 250, 500, 750, 1000])
  })

  it('formats within a day as HH:MM', () => {
    const ms = Date.parse('2026-07-09T14:05:00Z')
    expect(formatTimeTick(ms, 3_600_000)).toMatch(/^\d{2}:\d{2}$/)
  })

  it('formats beyond a day as DD/MM', () => {
    const ms = Date.parse('2026-07-09T14:05:00Z')
    expect(formatTimeTick(ms, 7 * 86_400_000)).toMatch(/^\d{2}\/\d{2}$/)
  })
})

describe('gapThresholdMs', () => {
  it('uses 1.5x the bucket duration when bucketed', () => {
    expect(gapThresholdMs([], 30_000)).toBe(45_000)
  })

  it('falls back to 1.5x the median raw spacing', () => {
    const samples: HistorySamplePoint[] = [
      { t: '2026-01-01T00:00:00Z', v: 1 },
      { t: '2026-01-01T00:00:05Z', v: 1 },
      { t: '2026-01-01T00:00:10Z', v: 1 },
    ]
    expect(gapThresholdMs(samples)).toBe(7_500)
  })

  it('is infinite for fewer than 2 raw samples', () => {
    expect(gapThresholdMs([{ t: '2026-01-01T00:00:00Z', v: 1 }])).toBe(Infinity)
  })
})

describe('segmentByGap', () => {
  it('keeps contiguous samples in one segment', () => {
    const samples: HistorySamplePoint[] = [
      { t: '2026-01-01T00:00:00Z', v: 1 },
      { t: '2026-01-01T00:00:05Z', v: 2 },
      { t: '2026-01-01T00:00:10Z', v: 3 },
    ]
    const segments = segmentByGap(samples, 6_000)
    expect(segments).toHaveLength(1)
    expect(segments[0]).toHaveLength(3)
  })

  it('breaks the path where a bucket window is missing', () => {
    const samples: HistorySamplePoint[] = [
      { t: '2026-01-01T00:00:00Z', v: 1 },
      { t: '2026-01-01T00:00:30Z', v: 2 },
      // gap: bucket omitted at 00:01:00
      { t: '2026-01-01T00:01:30Z', v: 3 },
      { t: '2026-01-01T00:02:00Z', v: 4 },
    ]
    const segments = segmentByGap(samples, 45_000) // 1.5x a 30s bucket
    expect(segments).toHaveLength(2)
    expect(segments[0]).toHaveLength(2)
    expect(segments[1]).toHaveLength(2)
  })

  it('returns no segments for empty input', () => {
    expect(segmentByGap([], 1000)).toEqual([])
  })
})

describe('boolRenderMode', () => {
  it('renders raw bool as a square wave', () => {
    expect(boolRenderMode(false)).toBe('square')
  })

  it('renders bucketed bool as duty cycle', () => {
    expect(boolRenderMode(true)).toBe('duty')
  })
})

describe('rangePreset', () => {
  it('picks a bucket that yields roughly 200-400 points per preset', () => {
    for (const id of ['1h', '6h', '24h', '7d'] as const) {
      const preset = rangePreset(id)
      const bucketMs = parseDurationMs(preset.bucket)
      const points = preset.spanMs / bucketMs
      expect(points).toBeGreaterThanOrEqual(200)
      expect(points).toBeLessThanOrEqual(400)
    }
  })

  it('falls back to the 1h preset for an unknown id', () => {
    expect(rangePreset('bogus').id).toBe('1h')
  })
})
