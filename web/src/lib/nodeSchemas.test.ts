import { describe, expect, it } from 'vitest'
import { EXPR_BUILTIN_NAMES, EXPR_RESERVED_NAMES, NODE_SCHEMAS, nodeInputs, nodeOutputs, PORT_COLORS } from './nodeSchemas'

// Hardcoded mirror of internal/engine/registry.go's init() registrations —
// keep this list in sync by hand whenever a node type is added/removed
// there. Failing this test is the trip wire for "engine grew a node type
// the editor doesn't know about yet".
const ENGINE_REGISTERED_TYPES = [
  // Sources
  'device-read',
  'constant',
  'timer',
  'clock',
  // Transforms
  'compare',
  'threshold-hysteresis',
  'scale',
  'and',
  'or',
  'not',
  'delay',
  'debounce',
  'edge-detect',
  'expression',
  // Sinks
  'device-write',
  'log',
]

describe('nodeSchemas: catalog completeness', () => {
  it('has a NODE_SCHEMAS entry for every engine-registered node type', () => {
    for (const type of ENGINE_REGISTERED_TYPES) {
      expect(NODE_SCHEMAS[type], `missing schema for engine type ${type}`).toBeDefined()
    }
  })

  it('has no schema entries for types the engine does not register', () => {
    expect(Object.keys(NODE_SCHEMAS).sort()).toEqual([...ENGINE_REGISTERED_TYPES].sort())
  })

  it('every schema declares a group and a non-empty displayName/description', () => {
    for (const schema of Object.values(NODE_SCHEMAS)) {
      expect(['sources', 'transforms', 'sinks']).toContain(schema.group)
      expect(schema.displayName.length).toBeGreaterThan(0)
      expect(schema.description.length).toBeGreaterThan(0)
    }
  })

  it('every port type used by a schema has a PORT_COLORS entry', () => {
    const ctx = { deviceRole: () => undefined }
    for (const schema of Object.values(NODE_SCHEMAS)) {
      const config = schema.defaultConfig()
      for (const port of [...nodeInputs(schema, config), ...nodeOutputs(schema, config)]) {
        const t = typeof port.type === 'function' ? port.type(config, ctx) : port.type
        expect(PORT_COLORS[t]).toBeDefined()
      }
    }
  })
})

describe('nodeSchemas: and/or dynamic port count', () => {
  it('"and" renders inputCount ports named in1..inN, matching logicInputPorts', () => {
    const schema = NODE_SCHEMAS.and!
    const ports = nodeInputs(schema, { inputCount: 4 })
    expect(ports.map((p) => p.id)).toEqual(['in1', 'in2', 'in3', 'in4'])
  })

  it('defaults to 2 input ports when inputCount is omitted', () => {
    const schema = NODE_SCHEMAS.or!
    const ports = nodeInputs(schema, {})
    expect(ports.map((p) => p.id)).toEqual(['in1', 'in2'])
  })
})

describe('nodeSchemas: log AnyType port', () => {
  it('log\'s "in" port type resolves to "any"', () => {
    const schema = NODE_SCHEMAS.log!
    const ports = nodeInputs(schema, schema.defaultConfig())
    expect(ports).toHaveLength(1)
    expect(ports[0]!.type).toBe('any')
  })

  it('log has no output ports (sink)', () => {
    const schema = NODE_SCHEMAS.log!
    expect(nodeOutputs(schema, schema.defaultConfig())).toHaveLength(0)
  })

  it('log defaultConfig omits `label` so the server default (node id) applies', () => {
    expect('label' in NODE_SCHEMAS.log!.defaultConfig()).toBe(false)
  })
})

describe('nodeSchemas: delay/debounce kind-dependent port type', () => {
  const ctx = { deviceRole: () => undefined }

  it('delay is bool by default and float when kind=float', () => {
    const schema = NODE_SCHEMAS.delay!
    const boolPort = nodeInputs(schema, { kind: 'bool' })[0]!
    const floatPort = nodeInputs(schema, { kind: 'float' })[0]!
    expect(typeof boolPort.type === 'function' ? boolPort.type({ kind: 'bool' }, ctx) : boolPort.type).toBe('bool')
    expect(typeof floatPort.type === 'function' ? floatPort.type({ kind: 'float' }, ctx) : floatPort.type).toBe(
      'float64',
    )
  })

  it('debounce mirrors the same kind-dependent typing on its output port', () => {
    const schema = NODE_SCHEMAS.debounce!
    const port = nodeOutputs(schema, { kind: 'float' })[0]!
    expect(typeof port.type === 'function' ? port.type({ kind: 'float' }, ctx) : port.type).toBe('float64')
  })
})

describe('nodeSchemas: expression config-driven ports', () => {
  const ctx = { deviceRole: () => undefined }

  it('derives one input port per config.inputs entry, in declared order, with mapped types', () => {
    const schema = NODE_SCHEMAS.expression!
    const config = {
      inputs: [
        { name: 'a', type: 'float64' },
        { name: 'b', type: 'bool' },
      ],
      outputType: 'bool',
    }
    const ports = nodeInputs(schema, config)
    expect(ports.map((p) => p.id)).toEqual(['a', 'b'])
    expect(ports.map((p) => (typeof p.type === 'function' ? p.type(config, ctx) : p.type))).toEqual([
      'float64',
      'bool',
    ])
  })

  it('output port type follows config.outputType', () => {
    const schema = NODE_SCHEMAS.expression!
    const boolOut = nodeOutputs(schema, { outputType: 'bool' })[0]!
    const floatOut = nodeOutputs(schema, { outputType: 'float64' })[0]!
    expect(typeof boolOut.type === 'function' ? boolOut.type({ outputType: 'bool' }, ctx) : boolOut.type).toBe(
      'bool',
    )
    expect(
      typeof floatOut.type === 'function' ? floatOut.type({ outputType: 'float64' }, ctx) : floatOut.type,
    ).toBe('float64')
  })

  it('ignores malformed/empty-named entries in config.inputs', () => {
    const schema = NODE_SCHEMAS.expression!
    const config = { inputs: [{ name: '', type: 'float64' }, { name: 'x', type: 'bool' }, {}], outputType: 'bool' }
    const ports = nodeInputs(schema, config)
    expect(ports.map((p) => p.id)).toEqual(['x'])
  })

  it('defaultConfig seeds one float64 input named "a" and a bool output', () => {
    const schema = NODE_SCHEMAS.expression!
    const config = schema.defaultConfig()
    expect(config).toEqual({ expression: '', inputs: [{ name: 'a', type: 'float64' }], outputType: 'bool' })
  })

  it('dedupes input ports by name, keeping the first occurrence, so duplicate rows never yield duplicate port ids', () => {
    const schema = NODE_SCHEMAS.expression!
    const config = {
      inputs: [
        { name: 'a', type: 'float64' },
        { name: 'a', type: 'bool' },
        { name: 'b', type: 'bool' },
      ],
      outputType: 'bool',
    }
    const ports = nodeInputs(schema, config)
    expect(ports.map((p) => p.id)).toEqual(['a', 'b'])
    // First occurrence wins: 'a' keeps its float64 type, not the second row's bool.
    expect(ports.map((p) => (typeof p.type === 'function' ? p.type(config, ctx) : p.type))).toEqual([
      'float64',
      'bool',
    ])
  })
})

describe('nodeSchemas: reserved expression names', () => {
  it('matches internal/expr/ast.go builtinArity exactly', () => {
    expect([...EXPR_BUILTIN_NAMES].sort()).toEqual(['abs', 'ceil', 'clamp', 'floor', 'max', 'min', 'round', 'sqrt'])
  })

  it('EXPR_RESERVED_NAMES is builtins plus the true/false keywords lexed by internal/expr/lexer.go', () => {
    for (const name of EXPR_BUILTIN_NAMES) expect(EXPR_RESERVED_NAMES.has(name)).toBe(true)
    expect(EXPR_RESERVED_NAMES.has('true')).toBe(true)
    expect(EXPR_RESERVED_NAMES.has('false')).toBe(true)
    expect(EXPR_RESERVED_NAMES.has('temperature')).toBe(false)
  })
})
