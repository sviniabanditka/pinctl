import { describe, expect, it } from 'vitest'
import {
  flowEdgeToModelEdge,
  flowNodeToModelNode,
  mapValidationIssuesToNodes,
  modelEdgeToFlowEdge,
  modelNodeToFlowNode,
  nextNodeId,
  parseEndpoint,
} from './graphMapping'
import type { Edge as ModelEdge, Node as ModelNode } from '../api/types'

describe('graphMapping: edge <-> API mapping', () => {
  it('parseEndpoint splits "n1.out" into node + port', () => {
    expect(parseEndpoint('n1.out')).toEqual({ node: 'n1', port: 'out' })
    expect(parseEndpoint('n12.threshold')).toEqual({ node: 'n12', port: 'threshold' })
  })

  it('modelEdgeToFlowEdge parses "n1.out" -> "n2.in" into a Vue Flow edge', () => {
    const modelEdge: ModelEdge = { from: 'n1.out', to: 'n2.in' }
    const flowEdge = modelEdgeToFlowEdge(modelEdge)
    expect(flowEdge).toMatchObject({
      id: 'n1.out->n2.in',
      source: 'n1',
      sourceHandle: 'out',
      target: 'n2',
      targetHandle: 'in',
    })
  })

  it('flowEdgeToModelEdge round-trips back to the "n1.out" -> "n2.in" model shape', () => {
    const modelEdge: ModelEdge = { from: 'n1.out', to: 'n2.threshold' }
    const roundTripped = flowEdgeToModelEdge(modelEdgeToFlowEdge(modelEdge))
    expect(roundTripped).toEqual(modelEdge)
  })

  it('modelNodeToFlowNode / flowNodeToModelNode round-trip a node', () => {
    const modelNode: ModelNode = {
      id: 'n3',
      type: 'compare',
      config: { op: 'lt', threshold: 300 },
      position: { x: 40, y: 120 },
    }
    const flowNode = modelNodeToFlowNode(modelNode)
    expect(flowNode.data.nodeType).toBe('compare')
    expect(flowNodeToModelNode(flowNode)).toEqual(modelNode)
  })
})

describe('graphMapping: node id generation', () => {
  it('returns n1 for an empty graph', () => {
    expect(nextNodeId([])).toBe('n1')
  })

  it('returns the first unused sequential id', () => {
    expect(nextNodeId(['n1', 'n2', 'n4'])).toBe('n3')
  })

  it('ignores ids not matching the n<N> scheme', () => {
    expect(nextNodeId(['foo', 'n1', 'bar-2'])).toBe('n2')
  })

  it('picks up after the highest existing id when the sequence is dense', () => {
    expect(nextNodeId(['n1', 'n2', 'n3'])).toBe('n4')
  })
})

describe('graphMapping: validation-error mapping', () => {
  it('maps issues onto matching node ids', () => {
    const issues = [
      { field: 'n1', code: 'validation_error', message: 'device role mismatch' },
      { field: 'n2', code: 'validation_error', message: 'dangling edge' },
    ]
    const result = mapValidationIssuesToNodes(issues, ['n1', 'n2', 'n3'])
    expect(result.n1).toHaveLength(1)
    expect(result.n1![0]!.message).toBe('device role mismatch')
    expect(result.n2).toHaveLength(1)
    expect(result.n3).toBeUndefined()
  })

  it('buckets issues with no field, or a field not matching a current node id, under the pipeline-level key', () => {
    const issues = [
      { code: 'validation_error', message: 'pipeline-wide: cycle detected' },
      { field: 'deleted-node', code: 'validation_error', message: 'stale reference' },
    ]
    const result = mapValidationIssuesToNodes(issues, ['n1'])
    expect(result['']).toHaveLength(2)
    expect(result.n1).toBeUndefined()
  })

  it('returns an empty object for undefined issues', () => {
    expect(mapValidationIssuesToNodes(undefined, ['n1'])).toEqual({})
  })
})
