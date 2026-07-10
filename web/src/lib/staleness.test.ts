import { describe, expect, it } from 'vitest'
import { isStaleTickValue, isStaleValue } from './staleness'

describe('isStaleValue', () => {
  it('is false with no timestamp', () => {
    expect(isStaleValue(undefined)).toBe(false)
  })

  it('is false just under the threshold', () => {
    const now = Date.parse('2026-01-01T00:00:03.000Z') // < 3000ms
    expect(isStaleValue('2026-01-01T00:00:00.000Z', now)).toBe(false)
  })

  it('is true once older than the threshold', () => {
    const now = Date.parse('2026-01-01T00:00:03.001Z') // > 3000ms
    expect(isStaleValue('2026-01-01T00:00:00.000Z', now)).toBe(true)
  })
})

describe('isStaleTickValue', () => {
  const tickMs = 20

  it('is false when the node has never received a value', () => {
    expect(isStaleTickValue(undefined, tickMs, Date.now())).toBe(false)
  })

  it('is false within a few tick periods of its last update', () => {
    const updatedAt = 1_000_000
    const now = updatedAt + tickMs * 5 // exactly at the multiplier, not over it
    expect(isStaleTickValue(updatedAt, tickMs, now)).toBe(false)
  })

  it('is true once it has gone stale for more than a few tick periods', () => {
    const updatedAt = 1_000_000
    const now = updatedAt + tickMs * 5 + 1
    expect(isStaleTickValue(updatedAt, tickMs, now)).toBe(true)
  })

  it('scales the stale window with a slower pipeline tick rate', () => {
    const slowTickMs = 1000
    const updatedAt = 1_000_000
    // Would be stale for a 20ms pipeline, but not for a 1000ms one.
    const now = updatedAt + 20 * 5 + 1
    expect(isStaleTickValue(updatedAt, slowTickMs, now)).toBe(false)
  })
})
