import { describe, expect, it } from 'vitest'
import { checkConnection, findCycleNodes, findUnconnectedRequiredInputs } from './clientValidation'
import type { FlowEdge, FlowNode } from './graphMapping'
import type { Device } from '../api/types'

function node(id: string, nodeType: string, config: Record<string, unknown> = {}): FlowNode {
  return { id, type: nodeType, position: { x: 0, y: 0 }, data: { nodeType, config } }
}

function edge(source: string, sourceHandle: string, target: string, targetHandle: string): FlowEdge {
  return { id: `${source}.${sourceHandle}->${target}.${targetHandle}`, source, sourceHandle, target, targetHandle }
}

const analogDevice: Device = {
  id: 'dev_ldr',
  name: 'LDR',
  boardId: 'b1',
  pin: 0,
  role: 'analog-in',
  options: {},
  createdAt: '',
  updatedAt: '',
}

const digitalOutDevice: Device = {
  id: 'dev_led',
  name: 'LED',
  boardId: 'b1',
  pin: 13,
  role: 'digital-out',
  options: {},
  createdAt: '',
  updatedAt: '',
}

describe('checkConnection', () => {
  it('rejects a self-loop', () => {
    const nodes = [node('n1', 'compare')]
    const result = checkConnection({ source: 'n1', sourceHandle: 'out', target: 'n1', targetHandle: 'in' }, nodes, [], [])
    expect(result.ok).toBe(false)
  })

  it('accepts a matching bool->bool or float->float connection', () => {
    const nodes = [node('n1', 'device-read', { deviceId: 'dev_ldr' }), node('n2', 'compare')]
    const result = checkConnection(
      { source: 'n1', sourceHandle: 'out', target: 'n2', targetHandle: 'in' },
      nodes,
      [],
      [analogDevice],
    )
    expect(result.ok).toBe(true)
  })

  it('rejects a bool -> float64 type mismatch (device-read digital-in feeding device-write pwm-out)', () => {
    const digitalIn: Device = { ...analogDevice, id: 'dev_pir', role: 'digital-in' }
    const nodes = [
      node('n1', 'device-read', { deviceId: 'dev_pir' }),
      node('n2', 'device-write', { deviceId: 'dev_led' }),
    ]
    // n2's device is digital-out (bool), n1's device is digital-in (bool) - actually matches; force a mismatch
    // by pointing n2 at a pwm-out device instead.
    const pwmDevice: Device = { ...digitalOutDevice, id: 'dev_pwm', role: 'pwm-out' }
    nodes[1]!.data.config = { deviceId: 'dev_pwm' }
    const result = checkConnection(
      { source: 'n1', sourceHandle: 'out', target: 'n2', targetHandle: 'in' },
      nodes,
      [],
      [digitalIn, pwmDevice],
    )
    expect(result.ok).toBe(false)
    expect(result.reason).toMatch(/type mismatch/i)
  })

  it('rejects a connection to a target port that already has an incoming edge', () => {
    const otherAnalog: Device = { ...analogDevice, id: 'dev_ldr2' }
    const nodes = [
      node('n1', 'device-read', { deviceId: 'dev_ldr' }),
      node('n2', 'compare'),
      node('n3', 'device-read', { deviceId: 'dev_ldr2' }),
    ]
    const edges = [edge('n1', 'out', 'n2', 'in')]
    const result = checkConnection(
      { source: 'n3', sourceHandle: 'out', target: 'n2', targetHandle: 'in' },
      nodes,
      edges,
      [analogDevice, otherAnalog],
    )
    expect(result.ok).toBe(false)
    expect(result.reason).toMatch(/already has an incoming/i)
  })

  it('rejects a duplicate edge', () => {
    const nodes = [node('n1', 'device-read', { deviceId: 'dev_ldr' }), node('n2', 'compare')]
    const edges = [edge('n1', 'out', 'n2', 'in')]
    const result = checkConnection(
      { source: 'n1', sourceHandle: 'out', target: 'n2', targetHandle: 'in' },
      nodes,
      edges,
      [analogDevice],
    )
    expect(result.ok).toBe(false)
  })
})

describe('findCycleNodes', () => {
  it('returns an empty set for an acyclic graph', () => {
    const nodes = [node('n1', 'device-read'), node('n2', 'compare'), node('n3', 'device-write')]
    const edges = [edge('n1', 'out', 'n2', 'in'), edge('n2', 'out', 'n3', 'in')]
    expect(findCycleNodes(nodes, edges).size).toBe(0)
  })

  it('detects a cycle spanning multiple nodes', () => {
    const nodes = [node('n1', 'compare'), node('n2', 'compare')]
    const edges = [edge('n1', 'out', 'n2', 'in'), edge('n2', 'out', 'n1', 'in')]
    const cyclic = findCycleNodes(nodes, edges)
    expect(cyclic.has('n1')).toBe(true)
    expect(cyclic.has('n2')).toBe(true)
  })
})

describe('findUnconnectedRequiredInputs', () => {
  it('flags a compare node with no incoming edge on its required "in" port', () => {
    const nodes = [node('n1', 'compare')]
    const result = findUnconnectedRequiredInputs(nodes, [])
    expect(result.n1).toEqual(['in'])
  })

  it('does not flag the optional "threshold" port', () => {
    const nodes = [node('n1', 'device-read', { deviceId: 'dev_ldr' }), node('n2', 'compare')]
    const edges = [edge('n1', 'out', 'n2', 'in')]
    const result = findUnconnectedRequiredInputs(nodes, edges)
    expect(result.n2).toBeUndefined()
  })
})
