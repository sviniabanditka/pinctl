<script setup lang="ts">
import { computed } from 'vue'
import { useDevicesStore } from '../../stores/devices'
import { useEditorStore } from '../../stores/editor'
import {
  EXPR_BUILTIN_NAMES,
  EXPR_BUILTINS,
  EXPR_OPERATORS,
  EXPR_RESERVED_NAMES,
  NODE_SCHEMAS,
  WEEKDAYS,
  type ExpressionInputDef,
} from '../../lib/nodeSchemas'

const props = defineProps<{ selectedNodeId: string | null }>()
const emit = defineEmits<{ delete: [id: string] }>()

const editor = useEditorStore()
const devices = useDevicesStore()

const selectedNode = computed(() => editor.nodes.find((n) => n.id === props.selectedNodeId) ?? null)
const schema = computed(() => (selectedNode.value ? NODE_SCHEMAS[selectedNode.value.data.nodeType] : undefined))

const errors = computed(() => (props.selectedNodeId ? editor.serverErrors[props.selectedNodeId] ?? [] : []))
const missing = computed(() => (props.selectedNodeId ? editor.clientMissingInputs[props.selectedNodeId] ?? [] : []))

function devicesFor(roles: string[] | undefined) {
  if (!roles) return devices.devices
  return devices.devices.filter((d) => roles.includes(d.role))
}

function fieldValue(key: string): unknown {
  return selectedNode.value?.data.config[key]
}

/**
 * `constant.value` / `delay.initial` / `debounce.initial` all have an
 * effective input kind that depends on a sibling `kind` config field (bool
 * vs. float) — the schema table only encodes a static kind per field, so
 * this "value-like field mirrors sibling kind" case is special-cased here
 * rather than growing the table's field-kind union for what's really one
 * pattern repeated across three node types.
 */
function effectiveKind(f: { key: string; kind: string }): string {
  if ((f.key === 'value' || f.key === 'initial') && selectedNode.value && 'kind' in selectedNode.value.data.config) {
    return selectedNode.value.data.config.kind === 'bool' ? 'bool' : 'number'
  }
  return f.kind
}

function onFieldInput(key: string, kind: string, raw: unknown) {
  if (!selectedNode.value) return
  const config = { ...selectedNode.value.data.config }
  if (kind === 'number') {
    config[key] = Number(raw)
  } else if (kind === 'bool') {
    config[key] = !!raw
  } else {
    config[key] = raw
  }
  editor.updateNodeConfig(selectedNode.value.id, config)
}

function isWeekdaySelected(key: string, day: string, def: unknown): boolean {
  const raw = fieldValue(key)
  const days = Array.isArray(raw) ? (raw as string[]) : Array.isArray(def) ? (def as string[]) : []
  return days.includes(day)
}

function onWeekdayToggle(key: string, day: string, def: unknown, checked: boolean) {
  if (!selectedNode.value) return
  const raw = fieldValue(key)
  const current = Array.isArray(raw) ? [...(raw as string[])] : Array.isArray(def) ? [...(def as string[])] : []
  const next = checked ? [...new Set([...current, day])] : current.filter((d) => d !== day)
  const config = { ...selectedNode.value.data.config, [key]: next }
  editor.updateNodeConfig(selectedNode.value.id, config)
}

// -- expression node: inputList + expression field kinds --------------------

const IDENT_RE = /^[A-Za-z_][A-Za-z0-9_]*$/

function expressionInputRows(key: string): ExpressionInputDef[] {
  const raw = fieldValue(key)
  return Array.isArray(raw) ? (raw as ExpressionInputDef[]) : []
}

function onInputRowChange(key: string, index: number, patch: Partial<ExpressionInputDef>) {
  if (!selectedNode.value) return
  const rows = expressionInputRows(key).map((r, i) => (i === index ? { ...r, ...patch } : r))
  editor.updateNodeConfig(selectedNode.value.id, { ...selectedNode.value.data.config, [key]: rows })
}

function onInputRowAdd(key: string) {
  if (!selectedNode.value) return
  const rows = [...expressionInputRows(key), { name: '', type: 'float64' as const }]
  editor.updateNodeConfig(selectedNode.value.id, { ...selectedNode.value.data.config, [key]: rows })
}

function onInputRowRemove(key: string, index: number) {
  if (!selectedNode.value) return
  const rows = expressionInputRows(key).filter((_, i) => i !== index)
  editor.updateNodeConfig(selectedNode.value.id, { ...selectedNode.value.data.config, [key]: rows })
}

/**
 * Cheap client-side checks that need no expression parser (docs/11-web-ui.md
 * client-side mirroring: parser-dependent checks are deferred to the
 * server's POST /pipelines/validate dry run). Only covers: empty
 * expression text, empty/duplicate/malformed-identifier/reserved-name input
 * names. Reserved names (builtins + true/false) are the parser's own
 * rejection list (internal/expr/ast.go's builtinArity, lexer.go's tTrue/
 * tFalse) — mirrored here via EXPR_RESERVED_NAMES so a name like "min"
 * fails fast instead of round-tripping through the debounced server validate.
 */
const expressionClientErrors = computed<string[]>(() => {
  if (!selectedNode.value || selectedNode.value.data.nodeType !== 'expression') return []
  const config = selectedNode.value.data.config
  const out: string[] = []
  const text = String(config.expression ?? '').trim()
  if (!text) out.push('Expression is empty.')

  const rows = expressionInputRows('inputs')
  const seen = new Set<string>()
  for (const row of rows) {
    const name = row.name ?? ''
    if (!name) {
      out.push('An input has no name.')
      continue
    }
    if (!IDENT_RE.test(name)) {
      out.push(`Input name "${name}" is not a valid identifier.`)
      continue
    }
    if (EXPR_RESERVED_NAMES.has(name)) {
      const kind = (EXPR_BUILTIN_NAMES as readonly string[]).includes(name) ? 'builtin' : 'keyword'
      out.push(`"${name}" is a reserved ${kind} name.`)
      continue
    }
    if (seen.has(name)) {
      out.push(`Duplicate input name "${name}".`)
      continue
    }
    seen.add(name)
  }
  return out
})
</script>

<template>
  <aside class="config-panel">
    <template v-if="!selectedNode">
      <h3>Pipeline settings</h3>
      <div class="field">
        <label for="pl-name">Name</label>
        <input id="pl-name" :value="editor.name" @input="editor.setName(($event.target as HTMLInputElement).value)" />
      </div>
      <div class="field">
        <label for="pl-tick">Tick rate (ms)</label>
        <input
          id="pl-tick"
          type="number"
          min="1"
          :value="editor.tickMs"
          @input="editor.setTickMs(Number(($event.target as HTMLInputElement).value))"
        />
      </div>
      <p class="field-hint">
        Enable/disable is a separate action from Save, in the toolbar above.
      </p>
    </template>

    <template v-else>
      <div class="node-config-header">
        <h3>{{ schema?.displayName ?? selectedNode.data.nodeType }}</h3>
        <button class="danger" @click="emit('delete', selectedNode.id)">Delete node</button>
      </div>

      <div v-if="errors.length > 0 && selectedNode.data.nodeType !== 'expression'" class="issue-list error">
        <div v-for="(e, i) in errors" :key="i">{{ e.message }}</div>
      </div>
      <div v-if="missing.length > 0" class="issue-list warning">
        Required input{{ missing.length > 1 ? 's' : '' }} not connected: {{ missing.join(', ') }}
      </div>

      <div v-for="f in schema?.fields ?? []" :key="f.key" class="field">
        <label :for="`f-${f.key}`">{{ f.label }}</label>

        <select
          v-if="effectiveKind(f) === 'enum'"
          :id="`f-${f.key}`"
          :value="String(fieldValue(f.key) ?? f.default)"
          @change="onFieldInput(f.key, 'string', ($event.target as HTMLSelectElement).value)"
        >
          <option v-for="opt in f.options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
        </select>

        <select
          v-else-if="effectiveKind(f) === 'deviceRef'"
          :id="`f-${f.key}`"
          :value="String(fieldValue(f.key) ?? '')"
          @change="onFieldInput(f.key, 'string', ($event.target as HTMLSelectElement).value)"
        >
          <option value="" disabled>Select a device…</option>
          <option v-for="d in devicesFor(f.deviceRoles)" :key="d.id" :value="d.id">{{ d.name }} ({{ d.role }})</option>
        </select>

        <label v-else-if="effectiveKind(f) === 'bool'" class="field-inline">
          <input
            type="checkbox"
            :checked="!!fieldValue(f.key)"
            @change="onFieldInput(f.key, 'bool', ($event.target as HTMLInputElement).checked)"
          />
        </label>

        <div v-else-if="f.kind === 'weekdays'" class="weekdays-field">
          <label v-for="day in WEEKDAYS" :key="day" class="field-inline weekday-toggle">
            <input
              type="checkbox"
              :checked="isWeekdaySelected(f.key, day, f.default)"
              @change="onWeekdayToggle(f.key, day, f.default, ($event.target as HTMLInputElement).checked)"
            />
            {{ day }}
          </label>
        </div>

        <div v-else-if="f.kind === 'inputList'" class="input-list-field">
          <div v-for="(row, i) in expressionInputRows(f.key)" :key="i" class="input-list-row">
            <input
              type="text"
              class="input-list-name"
              placeholder="name"
              :value="row.name"
              @input="onInputRowChange(f.key, i, { name: ($event.target as HTMLInputElement).value })"
            />
            <select
              :value="row.type"
              @change="onInputRowChange(f.key, i, { type: ($event.target as HTMLSelectElement).value as 'float64' | 'bool' })"
            >
              <option value="float64">float64</option>
              <option value="bool">bool</option>
            </select>
            <button type="button" class="danger-icon" title="Remove input" @click="onInputRowRemove(f.key, i)">×</button>
          </div>
          <button type="button" class="add-input" @click="onInputRowAdd(f.key)">+ Add input</button>
        </div>

        <div v-else-if="f.kind === 'expression'" class="expression-field">
          <textarea
            :id="`f-${f.key}`"
            class="expression-textarea"
            rows="3"
            spellcheck="false"
            :value="String(fieldValue(f.key) ?? f.default)"
            @input="onFieldInput(f.key, 'string', ($event.target as HTMLTextAreaElement).value)"
          />
          <p class="field-hint expression-hint">
            Operators: <code>{{ EXPR_OPERATORS }}</code><br />
            Builtins: <code>{{ EXPR_BUILTINS }}</code>
          </p>
          <div v-if="expressionClientErrors.length > 0" class="issue-list error">
            <div v-for="(e, i) in expressionClientErrors" :key="i">{{ e }}</div>
          </div>
          <div v-if="errors.length > 0" class="issue-list error">
            <div v-for="(e, i) in errors" :key="i">{{ e.message }}</div>
          </div>
        </div>

        <input
          v-else-if="effectiveKind(f) === 'number'"
          :id="`f-${f.key}`"
          type="number"
          :min="f.min"
          :step="f.step"
          :value="Number(fieldValue(f.key) ?? f.default)"
          @input="onFieldInput(f.key, 'number', ($event.target as HTMLInputElement).value)"
        />

        <input
          v-else
          :id="`f-${f.key}`"
          type="text"
          :value="String(fieldValue(f.key) ?? f.default)"
          @input="onFieldInput(f.key, 'string', ($event.target as HTMLInputElement).value)"
        />
      </div>
    </template>
  </aside>
</template>

<style scoped>
.config-panel {
  width: 260px;
  flex-shrink: 0;
  border-left: 1px solid var(--color-border);
  padding: 12px;
  overflow-y: auto;
}

.config-panel h3 {
  font-size: 13px;
  margin: 0 0 12px;
}

.node-config-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.node-config-header h3 {
  margin: 0;
}

.issue-list {
  font-size: 12px;
  border-radius: var(--radius);
  padding: 6px 8px;
  margin-bottom: 10px;
}

.issue-list.error {
  background: var(--color-danger-bg);
  color: var(--color-danger);
}

.issue-list.warning {
  background: var(--color-warning-bg);
  color: var(--color-warning);
}

.field-inline {
  display: flex;
  align-items: center;
  gap: 6px;
}

.weekdays-field {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.weekday-toggle {
  font-size: 11px;
  text-transform: capitalize;
}

.input-list-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.input-list-row {
  display: flex;
  align-items: center;
  gap: 4px;
}

.input-list-name {
  flex: 1;
  min-width: 0;
}

.danger-icon {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  line-height: 1;
  border: none;
  border-radius: var(--radius);
  background: var(--color-danger-bg);
  color: var(--color-danger);
  cursor: pointer;
}

.add-input {
  align-self: flex-start;
  font-size: 12px;
  background: none;
  border: 1px dashed var(--color-border);
  border-radius: var(--radius);
  padding: 4px 8px;
  cursor: pointer;
}

.expression-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.expression-textarea {
  font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace);
  font-size: 12px;
  resize: vertical;
  min-height: 3.5em;
}

.expression-hint {
  line-height: 1.5;
}

.expression-hint code {
  font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace);
}
</style>
