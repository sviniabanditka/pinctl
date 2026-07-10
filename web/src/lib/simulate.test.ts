import { describe, expect, it } from 'vitest'
import {
  buildSimulateRequest,
  carryForwardWrites,
  clampFrameIndex,
  clampSimSteps,
  mapUnseededWarnings,
  readDeviceIds,
  stepFrameIndex,
  writeDeviceIds,
  SIM_MAX_STEPS,
} from './simulate'
import type { Device, Node as ModelNode } from '../api/types'

describe('clampSimSteps', () => {
  it('clamps to the 2000 cap', () => {
    expect(clampSimSteps(5000)).toBe(SIM_MAX_STEPS)
  })

  it('floors fractional input', () => {
    expect(clampSimSteps(12.9)).toBe(12)
  })

  it('clamps negative/NaN to 0', () => {
    expect(clampSimSteps(-5)).toBe(0)
    expect(clampSimSteps(NaN)).toBe(0)
  })

  it('passes a normal value through unchanged', () => {
    expect(clampSimSteps(100)).toBe(100)
  })
})

describe('buildSimulateRequest', () => {
  const pipeline = {
    name: 'Test',
    tickMs: 20,
    nodes: [{ id: 'n1', type: 'constant', config: { kind: 'float', value: 1 }, position: { x: 0, y: 0 } }] as ModelNode[],
    edges: [],
  }

  it('matches the endpoint shape and clamps steps', () => {
    const req = buildSimulateRequest(pipeline, { dev_1: 512 }, [{ tick: 10, deviceId: 'dev_1', value: 0 }], 5000)
    expect(req).toEqual({
      pipeline: { name: 'Test', tickMs: 20, nodes: pipeline.nodes, edges: [] },
      steps: SIM_MAX_STEPS,
      inputs: { dev_1: 512 },
      script: [{ tick: 10, deviceId: 'dev_1', value: 0 }],
    })
  })

  it('defensively copies inputs/script so later mutation of the source objects does not leak in', () => {
    const inputs = { dev_1: 1 }
    const script = [{ tick: 1, deviceId: 'dev_1', value: 0 }]
    const req = buildSimulateRequest(pipeline, inputs, script, 100)
    inputs.dev_1 = 999
    script[0].value = 999
    expect(req.inputs.dev_1).toBe(1)
    expect(req.script[0].value).toBe(0)
  })
})

describe('readDeviceIds / writeDeviceIds', () => {
  const nodes: ModelNode[] = [
    { id: 'n1', type: 'device-read', config: { deviceId: 'dev_a' }, position: { x: 0, y: 0 } },
    { id: 'n2', type: 'device-read', config: { deviceId: 'dev_b' }, position: { x: 0, y: 0 } },
    { id: 'n3', type: 'device-read', config: { deviceId: 'dev_a' }, position: { x: 0, y: 0 } }, // duplicate device, two nodes
    { id: 'n4', type: 'device-write', config: { deviceId: 'dev_c' }, position: { x: 0, y: 0 } },
    { id: 'n5', type: 'constant', config: { kind: 'float', value: 1 }, position: { x: 0, y: 0 } },
    { id: 'n6', type: 'device-read', config: {}, position: { x: 0, y: 0 } }, // unbound, no deviceId yet
  ]

  it('collects unique read device ids in first-seen order', () => {
    expect(readDeviceIds(nodes)).toEqual(['dev_a', 'dev_b'])
  })

  it('collects unique write device ids', () => {
    expect(writeDeviceIds(nodes)).toEqual(['dev_c'])
  })
})

describe('carryForwardWrites', () => {
  it('carries a device absent from a frame forward from its last known value', () => {
    const carried = carryForwardWrites([
      { writes: { dev_a: 1 } },
      { writes: {} },
      { writes: { dev_a: 2 } },
    ])
    expect(carried[0]).toEqual({ dev_a: { value: 1, changed: true } })
    expect(carried[1]).toEqual({ dev_a: { value: 1, changed: false } })
    expect(carried[2]).toEqual({ dev_a: { value: 2, changed: true } })
  })

  it('sets the changed marker only on the frame(s) a device actually appears in', () => {
    const carried = carryForwardWrites([{ writes: { dev_a: true } }, { writes: { dev_b: 5 } }, { writes: {} }])
    expect(carried[0]).toEqual({ dev_a: { value: true, changed: true } })
    expect(carried[1]).toEqual({ dev_a: { value: true, changed: false }, dev_b: { value: 5, changed: true } })
    expect(carried[2]).toEqual({ dev_a: { value: true, changed: false }, dev_b: { value: 5, changed: false } })
  })

  it('a device that never appears has no entry at all, not a zero placeholder', () => {
    const carried = carryForwardWrites([{ writes: {} }])
    expect(carried[0]).toEqual({})
  })
})

describe('frame index math', () => {
  it('clampFrameIndex clamps into range, and to 0 when there are no frames', () => {
    expect(clampFrameIndex(5, 10)).toBe(5)
    expect(clampFrameIndex(-1, 10)).toBe(0)
    expect(clampFrameIndex(20, 10)).toBe(9)
    expect(clampFrameIndex(3, 0)).toBe(0)
  })

  it('stepFrameIndex steps and clamps at both ends', () => {
    expect(stepFrameIndex(5, 10, 1)).toBe(6)
    expect(stepFrameIndex(9, 10, 1)).toBe(9)
    expect(stepFrameIndex(0, 10, -1)).toBe(0)
    expect(stepFrameIndex(0, 10, 100)).toBe(9)
  })

  it('a run truncated by an error (fewer frames than requested steps) still clamps correctly', () => {
    // simulate.Run stops emitting frames the moment a node errors, so the
    // scrubber's effective frame count is frames.length, not the requested
    // steps — this models that: 200 requested, only 12 frames returned.
    const frameCount = 12
    expect(clampFrameIndex(199, frameCount)).toBe(11)
    expect(stepFrameIndex(11, frameCount, 1)).toBe(11)
  })
})

describe('mapUnseededWarnings', () => {
  const devices: Device[] = [
    {
      id: 'dev_1',
      name: 'Soil moisture',
      boardId: 'brd_1',
      pin: 2,
      role: 'analog-in',
      options: {},
      createdAt: '',
      updatedAt: '',
    },
  ]

  it('maps a warning to its device name, ignoring non-unseeded_input codes', () => {
    const out = mapUnseededWarnings(
      [
        { code: 'unseeded_input', deviceId: 'dev_1' },
        { code: 'something_else', deviceId: 'dev_2' },
      ],
      devices,
    )
    expect(out).toEqual([{ deviceId: 'dev_1', deviceName: 'Soil moisture' }])
  })

  it('dedupes repeated warnings for the same device', () => {
    const out = mapUnseededWarnings(
      [
        { code: 'unseeded_input', deviceId: 'dev_1' },
        { code: 'unseeded_input', deviceId: 'dev_1' },
      ],
      devices,
    )
    expect(out).toHaveLength(1)
  })

  it('falls back to the raw id when the device is unknown', () => {
    const out = mapUnseededWarnings([{ code: 'unseeded_input', deviceId: 'dev_missing' }], devices)
    expect(out).toEqual([{ deviceId: 'dev_missing', deviceName: 'dev_missing' }])
  })
})
