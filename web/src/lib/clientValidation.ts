// Client-side mirror of a subset of the engine's validation rules
// (docs/11-web-ui.md "Client-side validation mirroring"). Only the rules
// marked "Yes" in that table are implemented here; everything else is
// deferred to POST /pipelines/validate.

import type { Device } from '../api/types'
import type { FlowEdge, FlowNode } from './graphMapping'
import { type PortTypeContext, type PortValueType, resolvePortType, schemaFor } from './nodeSchemas'

export function portTypeContext(devices: Device[]): PortTypeContext {
  return {
    deviceRole: (deviceId: string) => devices.find((d) => d.id === deviceId)?.role,
  }
}

/** Resolves the value type of a specific node's port, or undefined if the node/port/schema is unknown. */
export function nodePortType(
  node: FlowNode | undefined,
  portId: string,
  direction: 'in' | 'out',
  ctx: PortTypeContext,
): PortValueType | undefined {
  if (!node) return undefined
  const schema = schemaFor(node.data.nodeType)
  if (!schema) return undefined
  const ports = direction === 'in' ? schema.inputs : schema.outputs
  const port = ports.find((p) => p.id === portId)
  if (!port) return undefined
  return resolvePortType(port, node.data.config, ctx)
}

export interface ConnectionCandidate {
  source: string
  sourceHandle: string
  target: string
  targetHandle: string
}

export interface ConnectionCheckResult {
  ok: boolean
  reason?: string
}

/**
 * Mirrors: port type match, self-loop rejection, target-already-connected,
 * duplicate (from,to) edge — all "purely local" per the mirroring table.
 */
export function checkConnection(
  candidate: ConnectionCandidate,
  nodes: FlowNode[],
  edges: FlowEdge[],
  devices: Device[],
): ConnectionCheckResult {
  if (candidate.source === candidate.target) {
    return { ok: false, reason: 'A node cannot connect to itself.' }
  }

  const ctx = portTypeContext(devices)
  const sourceNode = nodes.find((n) => n.id === candidate.source)
  const targetNode = nodes.find((n) => n.id === candidate.target)
  const sourceType = nodePortType(sourceNode, candidate.sourceHandle, 'out', ctx)
  const targetType = nodePortType(targetNode, candidate.targetHandle, 'in', ctx)

  if (sourceType && targetType && sourceType !== targetType) {
    return { ok: false, reason: `Type mismatch: ${sourceType} → ${targetType}` }
  }

  const targetTaken = edges.some(
    (e) => e.target === candidate.target && e.targetHandle === candidate.targetHandle,
  )
  if (targetTaken) {
    return { ok: false, reason: 'Target port already has an incoming connection.' }
  }

  const duplicate = edges.some(
    (e) =>
      e.source === candidate.source &&
      e.sourceHandle === candidate.sourceHandle &&
      e.target === candidate.target &&
      e.targetHandle === candidate.targetHandle,
  )
  if (duplicate) {
    return { ok: false, reason: 'This connection already exists.' }
  }

  return { ok: true }
}

/** Topological-sort based cycle detection over the current graph. Returns the set of node ids involved in a cycle. */
export function findCycleNodes(nodes: FlowNode[], edges: FlowEdge[]): Set<string> {
  const adjacency = new Map<string, string[]>()
  const indegree = new Map<string, number>()
  for (const n of nodes) {
    adjacency.set(n.id, [])
    indegree.set(n.id, 0)
  }
  for (const e of edges) {
    if (!adjacency.has(e.source) || !indegree.has(e.target)) continue
    adjacency.get(e.source)!.push(e.target)
    indegree.set(e.target, (indegree.get(e.target) ?? 0) + 1)
  }

  const queue: string[] = []
  for (const [id, deg] of indegree) if (deg === 0) queue.push(id)

  const visited = new Set<string>()
  while (queue.length > 0) {
    const id = queue.shift()!
    visited.add(id)
    for (const next of adjacency.get(id) ?? []) {
      const deg = (indegree.get(next) ?? 0) - 1
      indegree.set(next, deg)
      if (deg === 0) queue.push(next)
    }
  }

  const cyclic = new Set<string>()
  for (const n of nodes) if (!visited.has(n.id)) cyclic.add(n.id)
  return cyclic
}

/** Required-input-port-unconnected check, per node, from the static schema + current edges. */
export function findUnconnectedRequiredInputs(nodes: FlowNode[], edges: FlowEdge[]): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  for (const node of nodes) {
    const schema = schemaFor(node.data.nodeType)
    if (!schema) continue
    const missing: string[] = []
    for (const input of schema.inputs) {
      if (input.optional) continue
      const connected = edges.some((e) => e.target === node.id && e.targetHandle === input.id)
      if (!connected) missing.push(input.id)
    }
    if (missing.length > 0) out[node.id] = missing
  }
  return out
}
