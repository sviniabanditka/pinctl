// Table-driven node catalog knowledge for the editor — the one place the UI
// encodes docs/08-node-catalog.md outside the docs (docs/11-web-ui.md
// "Config panel": "kept intentionally small and table-driven so adding a
// node type later means adding one table entry, not a bespoke form
// component").
//
// v0.4 wires in the engine's full node catalog (internal/engine/
// {nodes.go,nodes_sources.go,nodes_transforms.go,nodes_sinks.go} +
// registry.go) — 15 types total. Keep this table's config field names in
// exact sync with each NodeType's Go config struct's json tags (not just
// the docs prose), since the editor writes these keys straight into
// pipeline.json. web/src/lib/nodeSchemas.test.ts asserts every
// registry-registered type name has an entry here.

import type { Device, DeviceRole } from '../api/types'

export type PortValueType = 'bool' | 'float64' | 'any'

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

/** Either a fixed port list, or one derived from the node's current config (e.g. and/or's inputCount). */
export type PortListDef = PortDef[] | ((config: Record<string, unknown>) => PortDef[])

export type FieldKind = 'string' | 'number' | 'bool' | 'enum' | 'deviceRef' | 'weekdays' | 'expression' | 'inputList'

export interface FieldDef {
  key: string
  label: string
  kind: FieldKind
  default: unknown
  options?: { value: string; label: string }[]
  /** For deviceRef fields: which device roles are selectable. */
  deviceRoles?: DeviceRole[]
  /** For number fields: HTML min/step, purely a UI nicety (server is the source of truth for validation). */
  min?: number
  step?: number
}

/** `expression` node's config.inputs entry shape — one identifier + its type. */
export interface ExpressionInputDef {
  name: string
  type: 'float64' | 'bool'
}

/** Operators/builtins accepted by internal/expr (ast.go's builtinArity + lexer.go's operator tokens) — surfaced as a helper line in the config panel, not re-derived client-side. */
export const EXPR_OPERATORS = '+ - * / %  < <= > >= == !=  && || !'

/** internal/expr/ast.go's builtinArity keys — the parser rejects an identifier whose name matches one of these. */
export const EXPR_BUILTIN_NAMES = ['min', 'max', 'abs', 'clamp', 'floor', 'ceil', 'round', 'sqrt'] as const
export const EXPR_BUILTINS = EXPR_BUILTIN_NAMES.join(' ')

/** internal/expr/lexer.go lexes these as tTrue/tFalse keyword tokens, never as tIdent — so they can never be a valid identifier name either. */
export const EXPR_KEYWORDS = ['true', 'false'] as const

/** Single source of truth for client-side "is this name reserved" checks (docs/11-web-ui.md mirroring): builtins ∪ keywords. */
export const EXPR_RESERVED_NAMES: ReadonlySet<string> = new Set<string>([...EXPR_BUILTIN_NAMES, ...EXPR_KEYWORDS])

export interface NodeSchema {
  type: string
  displayName: string
  group: 'sources' | 'transforms' | 'sinks'
  description: string
  inputs: PortListDef
  outputs: PortListDef
  fields: FieldDef[]
  defaultConfig: () => Record<string, unknown>
  /** Compact one-line summary of the node's config, for the node card. */
  summary: (config: Record<string, unknown>, devices: Device[]) => string
}

export const PORT_COLORS: Record<PortValueType, string> = {
  bool: '#d97706', // amber
  float64: '#0d9488', // teal
  any: '#6b7280', // neutral gray — log's AnyType input, accepts either type
}

/** All seven weekday tokens accepted by clock.config.days, in `internal/engine/nodes_sources.go`'s allWeekdays order. */
export const WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const

function deviceName(devices: Device[], id: unknown): string | undefined {
  return devices.find((d) => d.id === id)?.name
}

/** `delay`/`debounce` share config.kind ("bool" | "float", default "bool") fixing their in/out port type. */
function kindPortType(config: Record<string, unknown>): PortValueType {
  return config.kind === 'float' ? 'float64' : 'bool'
}

function boolPort(id: string, optional = false): PortDef {
  return { id, type: 'bool', optional }
}
function floatPort(id: string, optional = false): PortDef {
  return { id, type: 'float64', optional }
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

  timer: {
    type: 'timer',
    displayName: 'Timer',
    group: 'sources',
    description: 'Emits a one-tick pulse at a fixed period.',
    inputs: [],
    outputs: [boolPort('out')],
    fields: [{ key: 'periodMs', label: 'Period (ms)', kind: 'number', default: 1000, min: 0 }],
    defaultConfig: () => ({ periodMs: 1000 }),
    summary: (config) => `every ${config.periodMs ?? 1000}ms`,
  },

  clock: {
    type: 'clock',
    displayName: 'Clock',
    group: 'sources',
    description: 'Emits true while the current time-of-day falls within a configured window.',
    inputs: [],
    outputs: [boolPort('out')],
    fields: [
      { key: 'start', label: 'Start (HH:MM)', kind: 'string', default: '00:00' },
      { key: 'end', label: 'End (HH:MM)', kind: 'string', default: '23:59' },
      { key: 'days', label: 'Days', kind: 'weekdays', default: [...WEEKDAYS] },
    ],
    defaultConfig: () => ({ start: '00:00', end: '23:59', days: [...WEEKDAYS] }),
    summary: (config) => {
      const days = Array.isArray(config.days) ? (config.days as string[]) : [...WEEKDAYS]
      const dayLabel = days.length === 7 ? 'daily' : days.join(',')
      return `${config.start ?? '00:00'}–${config.end ?? '23:59'} (${dayLabel})`
    },
  },

  compare: {
    type: 'compare',
    displayName: 'Compare',
    group: 'transforms',
    description: 'Compares an input value against a threshold, producing a bool.',
    inputs: [floatPort('in'), floatPort('threshold', true)],
    outputs: [boolPort('out')],
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

  'threshold-hysteresis': {
    type: 'threshold-hysteresis',
    displayName: 'Threshold (Hysteresis)',
    group: 'transforms',
    description: 'Schmitt-trigger comparator: on above onLevel, off below offLevel, holds between.',
    inputs: [floatPort('in')],
    outputs: [boolPort('out')],
    fields: [
      { key: 'onLevel', label: 'On level', kind: 'number', default: 600 },
      { key: 'offLevel', label: 'Off level', kind: 'number', default: 400 },
      { key: 'initial', label: 'Initial state', kind: 'bool', default: false },
    ],
    defaultConfig: () => ({ onLevel: 600, offLevel: 400, initial: false }),
    summary: (config) => `on>${config.onLevel ?? 600} off<${config.offLevel ?? 400}`,
  },

  scale: {
    type: 'scale',
    displayName: 'Scale',
    group: 'transforms',
    description: 'Linearly maps an input value from one range to another.',
    inputs: [floatPort('in')],
    outputs: [floatPort('out')],
    fields: [
      { key: 'inMin', label: 'In min', kind: 'number', default: 0 },
      { key: 'inMax', label: 'In max', kind: 'number', default: 1023 },
      { key: 'outMin', label: 'Out min', kind: 'number', default: 0 },
      { key: 'outMax', label: 'Out max', kind: 'number', default: 1 },
      { key: 'clamp', label: 'Clamp output', kind: 'bool', default: true },
    ],
    defaultConfig: () => ({ inMin: 0, inMax: 1023, outMin: 0, outMax: 1, clamp: true }),
    summary: (config) =>
      `[${config.inMin ?? 0}, ${config.inMax ?? 1023}] → [${config.outMin ?? 0}, ${config.outMax ?? 1}]`,
  },

  and: {
    type: 'and',
    displayName: 'AND',
    group: 'transforms',
    description: 'Boolean AND over two or more inputs.',
    // Port naming ("in1".."inN") matches internal/engine/nodes_transforms.go's logicInputPorts.
    inputs: (config) => logicInputPorts(config),
    outputs: [boolPort('out')],
    fields: [{ key: 'inputCount', label: 'Input count', kind: 'number', default: 2, min: 2 }],
    defaultConfig: () => ({ inputCount: 2 }),
    summary: (config) => `AND(${normalizeInputCount(config.inputCount)})`,
  },

  or: {
    type: 'or',
    displayName: 'OR',
    group: 'transforms',
    description: 'Boolean OR over two or more inputs.',
    inputs: (config) => logicInputPorts(config),
    outputs: [boolPort('out')],
    fields: [{ key: 'inputCount', label: 'Input count', kind: 'number', default: 2, min: 2 }],
    defaultConfig: () => ({ inputCount: 2 }),
    summary: (config) => `OR(${normalizeInputCount(config.inputCount)})`,
  },

  not: {
    type: 'not',
    displayName: 'NOT',
    group: 'transforms',
    description: 'Boolean negation.',
    inputs: [boolPort('in')],
    outputs: [boolPort('out')],
    fields: [],
    defaultConfig: () => ({}),
    summary: () => 'NOT',
  },

  delay: {
    type: 'delay',
    displayName: 'Delay',
    group: 'transforms',
    description: 'Propagates a value to its output after a fixed delay.',
    inputs: [{ id: 'in', type: (config) => kindPortType(config) }],
    outputs: [{ id: 'out', type: (config) => kindPortType(config) }],
    fields: [
      {
        key: 'kind',
        label: 'Type',
        kind: 'enum',
        default: 'bool',
        options: [
          { value: 'bool', label: 'Boolean' },
          { value: 'float', label: 'Number' },
        ],
      },
      { key: 'delayMs', label: 'Delay (ms)', kind: 'number', default: 1000, min: 0 },
      { key: 'initial', label: 'Initial value', kind: 'number', default: 0 },
    ],
    defaultConfig: () => ({ kind: 'bool', delayMs: 1000, initial: false }),
    summary: (config) => `${config.delayMs ?? 1000}ms (${config.kind === 'float' ? 'number' : 'bool'})`,
  },

  debounce: {
    type: 'debounce',
    displayName: 'Debounce',
    group: 'transforms',
    description: 'Passes a value through only after it has been stable for a minimum duration.',
    inputs: [{ id: 'in', type: (config) => kindPortType(config) }],
    outputs: [{ id: 'out', type: (config) => kindPortType(config) }],
    fields: [
      {
        key: 'kind',
        label: 'Type',
        kind: 'enum',
        default: 'bool',
        options: [
          { value: 'bool', label: 'Boolean' },
          { value: 'float', label: 'Number' },
        ],
      },
      { key: 'stableMs', label: 'Stable for (ms)', kind: 'number', default: 50, min: 0 },
      { key: 'initial', label: 'Initial value', kind: 'number', default: 0 },
    ],
    defaultConfig: () => ({ kind: 'bool', stableMs: 50, initial: false }),
    summary: (config) => `${config.stableMs ?? 50}ms stable (${config.kind === 'float' ? 'number' : 'bool'})`,
  },

  'edge-detect': {
    type: 'edge-detect',
    displayName: 'Edge Detect',
    group: 'transforms',
    description: 'Emits a one-tick pulse when a boolean input transitions.',
    inputs: [boolPort('in')],
    outputs: [boolPort('out')],
    fields: [
      {
        key: 'edge',
        label: 'Edge',
        kind: 'enum',
        default: 'rising',
        options: [
          { value: 'rising', label: 'Rising (false→true)' },
          { value: 'falling', label: 'Falling (true→false)' },
          { value: 'both', label: 'Both' },
        ],
      },
    ],
    defaultConfig: () => ({ edge: 'rising' }),
    summary: (config) => `edge: ${config.edge ?? 'rising'}`,
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

  expression: {
    type: 'expression',
    displayName: 'Expression',
    group: 'transforms',
    description: 'Evaluates a small typed expression over its named inputs (internal/expr).',
    // Port ids come straight from config.inputs[i].name, in declared order —
    // no client-side expression parser needed, since ports are config-driven
    // rather than derived from the expression text itself.
    inputs: (config) => expressionInputPorts(config),
    outputs: [{ id: 'out', type: (config) => (config.outputType === 'float64' ? 'float64' : 'bool') }],
    fields: [
      { key: 'inputs', label: 'Inputs', kind: 'inputList', default: [{ name: 'a', type: 'float64' }] },
      {
        key: 'outputType',
        label: 'Output type',
        kind: 'enum',
        default: 'bool',
        options: [
          { value: 'bool', label: 'Boolean' },
          { value: 'float64', label: 'Number' },
        ],
      },
      { key: 'expression', label: 'Expression', kind: 'expression', default: '' },
    ],
    defaultConfig: () => ({ expression: '', inputs: [{ name: 'a', type: 'float64' }], outputType: 'bool' }),
    summary: (config) => {
      const text = String(config.expression ?? '').trim()
      if (!text) return '(empty)'
      return text.length > 28 ? `${text.slice(0, 28)}…` : text
    },
  },

  log: {
    type: 'log',
    displayName: 'Log',
    group: 'sinks',
    description: 'Writes the current input value to the pipeline’s live log stream. Purely observational.',
    // AnyType: log "accepts either type, logs whichever arrives" per
    // docs/08-node-catalog.md — see internal/engine/nodes_sinks.go's
    // logType.Ports and Port.AnyType's doc comment for why this needs a
    // dedicated escape hatch from the otherwise-strict edge type-checking.
    inputs: [{ id: 'in', type: 'any' }],
    outputs: [],
    fields: [
      { key: 'label', label: 'Label (blank = node id)', kind: 'string', default: '' },
      { key: 'onChangeOnly', label: 'Log on change only', kind: 'bool', default: true },
    ],
    // label intentionally omitted (not just empty string) so the server's
    // *string-is-nil default (deps.NodeID) applies — see logType.NewInstance.
    defaultConfig: () => ({ onChangeOnly: true }),
    summary: (config) => `"${(config.label as string) || '(node id)'}"${config.onChangeOnly === false ? '' : ' · on change'}`,
  },
}

/** Engine default: 0/omitted inputCount means 2; `internal/engine/nodes_transforms.go`'s parseLogicConfig. */
function normalizeInputCount(raw: unknown): number {
  const n = Number(raw)
  return Number.isFinite(n) && n >= 2 ? Math.floor(n) : 2
}

function logicInputPorts(config: Record<string, unknown>): PortDef[] {
  const n = normalizeInputCount(config.inputCount)
  return Array.from({ length: n }, (_, i) => boolPort(`in${i + 1}`, false))
}

/**
 * Derives `expression`'s input ports from config.inputs, in declared order; port id == identifier name.
 * Deduped by name (first occurrence wins) — while the user is mid-edit two rows can briefly share a
 * name, and two ports with the same id would make Vue Flow's handle rendering/edge targeting ambiguous.
 * The duplicate itself is still surfaced as a client validation error in the config panel.
 */
function expressionInputPorts(config: Record<string, unknown>): PortDef[] {
  const raw = Array.isArray(config.inputs) ? (config.inputs as Partial<ExpressionInputDef>[]) : []
  const seen = new Set<string>()
  const out: PortDef[] = []
  for (const i of raw) {
    if (typeof i?.name !== 'string' || i.name.length === 0) continue
    if (seen.has(i.name)) continue
    seen.add(i.name)
    out.push({ id: i.name, type: i.type === 'bool' ? 'bool' : 'float64' })
  }
  return out
}

export const PALETTE_GROUPS: { key: NodeSchema['group']; label: string }[] = [
  { key: 'sources', label: 'Sources' },
  { key: 'transforms', label: 'Transforms' },
  { key: 'sinks', label: 'Sinks' },
]

export function schemaFor(type: string): NodeSchema | undefined {
  return NODE_SCHEMAS[type]
}

/** Resolves a schema's (possibly config-dependent) input port list. */
export function nodeInputs(schema: NodeSchema, config: Record<string, unknown>): PortDef[] {
  return typeof schema.inputs === 'function' ? schema.inputs(config) : schema.inputs
}

/** Resolves a schema's (possibly config-dependent) output port list. */
export function nodeOutputs(schema: NodeSchema, config: Record<string, unknown>): PortDef[] {
  return typeof schema.outputs === 'function' ? schema.outputs(config) : schema.outputs
}

export function resolvePortType(
  port: PortDef,
  config: Record<string, unknown>,
  ctx: PortTypeContext,
): PortValueType {
  return typeof port.type === 'function' ? port.type(config, ctx) : port.type
}
