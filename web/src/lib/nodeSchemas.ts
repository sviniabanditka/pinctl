// Table-driven node catalog knowledge for the editor — the one place the UI
// encodes docs/08-node-catalog.md outside the docs (docs/11-web-ui.md
// "Config panel": "kept intentionally small and table-driven so adding a
// node type later means adding one table entry, not a bespoke form
// component").
//
// Scope note: pinctl v0.3's editor ships only the 4 node types the engine
// currently implements (internal/engine/nodes.go) — device-read, constant,
// compare, device-write. The other catalog entries (timer, clock,
// threshold-hysteresis, scale, and/or/not, delay, debounce, edge-detect,
// log) are documented in 08-node-catalog.md as roadmap-adjacent but are not
// wired into the engine yet, so they're intentionally left out of this
// table rather than added as dead UI.

import type { Device, DeviceRole } from '../api/types'

export type PortValueType = 'bool' | 'float64'

export interface PortDef {
  id: string
  /** Fixed type, or a function deriving the type from the node's current config/context. */
  type: PortValueType | ((config: Record<string, unknown>, ctx: PortTypeContext) => PortValueType)
  optional?: boolean
}

export interface PortTypeContext {
  /** Resolves a device's port type from its role, for device-read/device-write. */
  deviceRole: (deviceId: string) => DeviceRole | undefined
}

export type FieldKind = 'string' | 'number' | 'bool' | 'enum' | 'deviceRef'

export interface FieldDef {
  key: string
  label: string
  kind: FieldKind
  default: unknown
  options?: { value: string; label: string }[]
  /** For deviceRef fields: which device roles are selectable. */
  deviceRoles?: DeviceRole[]
}

export interface NodeSchema {
  type: string
  displayName: string
  group: 'sources' | 'transforms' | 'sinks'
  description: string
  inputs: PortDef[]
  outputs: PortDef[]
  fields: FieldDef[]
  defaultConfig: () => Record<string, unknown>
  /** Compact one-line summary of the node's config, for the node card. */
  summary: (config: Record<string, unknown>, devices: Device[]) => string
}

export const PORT_COLORS: Record<PortValueType, string> = {
  bool: '#d97706', // amber
  float64: '#0d9488', // teal
}

function deviceName(devices: Device[], id: unknown): string | undefined {
  return devices.find((d) => d.id === id)?.name
}

export const NODE_SCHEMAS: Record<string, NodeSchema> = {
  'device-read': {
    type: 'device-read',
    displayName: 'Device Read',
    group: 'sources',
    description: 'Reads the current value of a bound device on every tick.',
    inputs: [],
    outputs: [
      {
        id: 'out',
        type: (config, ctx) => {
          const role = ctx.deviceRole(String(config.deviceId ?? ''))
          return role === 'digital-in' ? 'bool' : 'float64'
        },
      },
    ],
    fields: [
      { key: 'deviceId', label: 'Device', kind: 'deviceRef', default: '', deviceRoles: ['digital-in', 'analog-in'] },
    ],
    defaultConfig: () => ({ deviceId: '' }),
    summary: (config, devices) => deviceName(devices, config.deviceId) ?? '(no device)',
  },

  constant: {
    type: 'constant',
    displayName: 'Constant',
    group: 'sources',
    description: 'Emits a fixed, user-configured value every tick.',
    inputs: [],
    outputs: [{ id: 'out', type: (config) => (config.kind === 'bool' ? 'bool' : 'float64') }],
    fields: [
      {
        key: 'kind',
        label: 'Type',
        kind: 'enum',
        default: 'float',
        options: [
          { value: 'float', label: 'Number' },
          { value: 'bool', label: 'Boolean' },
        ],
      },
      { key: 'value', label: 'Value', kind: 'number', default: 0 },
    ],
    defaultConfig: () => ({ kind: 'float', value: 0 }),
    summary: (config) => `${config.kind === 'bool' ? (config.value ? 'true' : 'false') : String(config.value ?? 0)}`,
  },

  compare: {
    type: 'compare',
    displayName: 'Compare',
    group: 'transforms',
    description: 'Compares an input value against a threshold, producing a bool.',
    inputs: [
      { id: 'in', type: 'float64' },
      { id: 'threshold', type: 'float64', optional: true },
    ],
    outputs: [{ id: 'out', type: 'bool' }],
    fields: [
      {
        key: 'op',
        label: 'Operator',
        kind: 'enum',
        default: 'gt',
        options: [
          { value: 'gt', label: '>' },
          { value: 'gte', label: '>=' },
          { value: 'lt', label: '<' },
          { value: 'lte', label: '<=' },
          { value: 'eq', label: '==' },
          { value: 'neq', label: '!=' },
        ],
      },
      { key: 'threshold', label: 'Threshold', kind: 'number', default: 0 },
    ],
    defaultConfig: () => ({ op: 'gt', threshold: 0 }),
    summary: (config) => `${config.op ?? 'gt'} ${config.threshold ?? 0}`,
  },

  'device-write': {
    type: 'device-write',
    displayName: 'Device Write',
    group: 'sinks',
    description: 'Writes a value to a bound device on every tick.',
    inputs: [
      {
        id: 'in',
        type: (config, ctx) => {
          const role = ctx.deviceRole(String(config.deviceId ?? ''))
          return role === 'digital-out' ? 'bool' : 'float64'
        },
      },
    ],
    outputs: [],
    fields: [
      { key: 'deviceId', label: 'Device', kind: 'deviceRef', default: '', deviceRoles: ['digital-out', 'pwm-out'] },
    ],
    defaultConfig: () => ({ deviceId: '' }),
    summary: (config, devices) => deviceName(devices, config.deviceId) ?? '(no device)',
  },
}

export const PALETTE_GROUPS: { key: NodeSchema['group']; label: string }[] = [
  { key: 'sources', label: 'Sources' },
  { key: 'transforms', label: 'Transforms' },
  { key: 'sinks', label: 'Sinks' },
]

export function schemaFor(type: string): NodeSchema | undefined {
  return NODE_SCHEMAS[type]
}

export function resolvePortType(
  port: PortDef,
  config: Record<string, unknown>,
  ctx: PortTypeContext,
): PortValueType {
  return typeof port.type === 'function' ? port.type(config, ctx) : port.type
}
