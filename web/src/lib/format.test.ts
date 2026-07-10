import { describe, expect, it } from 'vitest'
import { formatNumber } from './format'

describe('formatNumber', () => {
  it('leaves integers whole', () => {
    expect(formatNumber(90)).toBe('90')
    expect(formatNumber(0)).toBe('0')
    expect(formatNumber(-5)).toBe('-5')
  })

  it('clips a float to two decimals', () => {
    // A servo angle computed by an expression node, as it really arrives.
    expect(formatNumber(88.67826086956522)).toBe('88.68')
    expect(formatNumber(0.005)).toBe('0.01')
  })

  it('drops trailing zeros rather than padding', () => {
    expect(formatNumber(1.5)).toBe('1.5')
    expect(formatNumber(1.10)).toBe('1.1')
    expect(formatNumber(2.0001)).toBe('2')
  })

  it('passes non-finite values through rather than inventing a number', () => {
    expect(formatNumber(NaN)).toBe('NaN')
    expect(formatNumber(Infinity)).toBe('Infinity')
  })
})
