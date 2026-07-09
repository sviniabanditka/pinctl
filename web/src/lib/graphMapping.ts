// Pure functions mapping between the API's model shapes (docs/09-data-model.md:
// nodes with {id, type, config, position}, edges as "n1.out" -> "n2.in"
// strings) and Vue Flow's node/edge model. Kept dependency-free so it's
// trivially unit-testable (docs/13's "keep component tests minimal";
// mapping logic is exactly the kind of thing to push into a plain function).

import type { Edge as ModelEdge, Node as ModelNode, ValidationIssue } from '../api/types'
import type { PortValueType } from './nodeSchemas'

export interface FlowNodeData {
  nodeType: string
  config: Record<string, unknown>
}

/** Vue Flow node shape this editor uses — position + our own data payload. */
export interface FlowNode {
  id: string
  type: string
  position: { x: number; y: number }
  data: FlowNodeData
}

export interface FlowEdge {
  id: string
  source: string
  sourceHandle: string
  target: string
  targetHandle: string
  /** Vue Flow edge-type key, always 'pinctl' (routes to PipelineEdge.vue). */
  type?: string
  data?: { valueType?: PortValueType }
}

/** Splits "n1.out" into { node: "n1", port: "out" }. Mirrors internal/model.Endpoint. */
export function parseEndpoint(s: string): { node: string; port: string } {
  const idx = s.indexOf('.')
  if (idx === -1) return { node: s, port: '' }
  return { node: s.slice(0, idx), port: s.slice(idx + 1) }
}

export function endpoint(node: string, port: string): string {
  return `${node}.${port}`
}

export function modelNodeToFlowNode(n: ModelNode): FlowNode {
  return {
    id: n.id,
    type: n.type,
    position: { x: n.position.x, y: n.position.y },
    data: { nodeType: n.type, config: (n.config ?? {}) as Record<string, unknown> },
  }
}

export function flowNodeToModelNode(n: FlowNode): ModelNode {
  return {
    id: n.id,
    type: n.data.nodeType,
    config: n.data.config,
    position: { x: Math.round(n.position.x), y: Math.round(n.position.y) },
  }
}

export function modelEdgeToFlowEdge(e: ModelEdge): FlowEdge {
  const from = parseEndpoint(e.from)
  const to = parseEndpoint(e.to)
  return {
    id: `${e.from}->${e.to}`,
    source: from.node,
    sourceHandle: from.port,
    target: to.node,
    targetHandle: to.port,
  }
}

export function flowEdgeToModelEdge(e: FlowEdge): ModelEdge {
  return {
    from: endpoint(e.source, e.sourceHandle),
    to: endpoint(e.target, e.targetHandle),
  }
}

/** First unused "n<N>" id, matching docs/09-data-model.md's ID strategy. */
export function nextNodeId(existingIds: string[]): string {
  const used = new Set<number>()
  for (const id of existingIds) {
    const m = /^n(\d+)$/.exec(id)
    if (m) used.add(Number(m[1]))
  }
  let n = 1
  while (used.has(n)) n++
  return `n${n}`
}

/**
 * Maps server-side validation errors/warnings (field = nodeId, per
 * internal/api/pipelines.go's validationErrorOut) onto the node/edge ids
 * they concern. Errors whose field doesn't match any current node id are
 * treated as pipeline-level (returned under the empty-string key).
 */
export function mapValidationIssuesToNodes(
  issues: ValidationIssue[] | undefined,
  nodeIds: string[],
): Record<string, ValidationIssue[]> {
  const out: Record<string, ValidationIssue[]> = {}
  if (!issues) return out
  const known = new Set(nodeIds)
  for (const issue of issues) {
    const key = issue.field && known.has(issue.field) ? issue.field : ''
    if (!out[key]) out[key] = []
    out[key].push(issue)
  }
  return out
}
