<script setup lang="ts">
// Node configuration popover — anchored beside the selected node by the
// parent view (PipelineEditorView owns all Vue Flow viewport math; this
// component only renders content + owns its own focus/keyboard behaviour).
import { computed, nextTick, ref, watch } from 'vue'
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

const props = withDefaults(
  defineProps<{
    selectedNodeId: string | null
    anchorStyle?: Record<string, string>
    side?: 'left' | 'right' | 'sheet'
  }>(),
  { anchorStyle: () => ({}), side: 'right' },
)
const emit = defineEmits<{ delete: [id: string]; close: [] }>()

const editor = useEditorStore()
const devices = useDevicesStore()

const rootEl = ref<HTMLElement | null>(null)

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

// -- open/close focus management --------------------------------------------

function focusFirstField() {
  const el = rootEl.value?.querySelector<HTMLElement>('.popover-body input, .popover-body select, .popover-body textarea')
  el?.focus()
}

watch(
  () => props.selectedNodeId,
  async (id) => {
    if (!id) return
    await nextTick()
    focusFirstField()
  },
  { immediate: true },
)

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape') {
    ev.stopPropagation()
    emit('close')
    return
  }
  if (ev.key === 'Tab') trapFocus(ev)
}

/** Minimal focus trap: cycles Tab/Shift+Tab within the popover's focusable elements. */
function trapFocus(ev: KeyboardEvent) {
  const root = rootEl.value
  if (!root) return
  const focusable = Array.from(
    root.querySelectorAll<HTMLElement>('button, input, select, textarea, [tabindex]:not([tabindex="-1"])'),
  ).filter((el) => !el.hasAttribute('disabled'))
  if (focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (ev.shiftKey && document.activeElement === first) {
    ev.preventDefault()
    last.focus()
  } else if (!ev.shiftKey && document.activeElement === last) {
    ev.preventDefault()
    first.focus()
  }
}
</script>

<template>
  <div
    v-if="selectedNode"
    ref="rootEl"
    class="config-popover"
    :class="[`side-${side}`]"
    :style="anchorStyle"
    role="dialog"
    :aria-label="`${schema?.displayName ?? selectedNode.data.nodeType} settings`"
    @keydown="onKeydown"
  >
    <span v-if="side !== 'sheet'" class="popover-caret" aria-hidden="true" />

    <div class="popover-header">
      <span class="label">{{ schema?.displayName ?? selectedNode.data.nodeType }}</span>
      <div class="popover-header-actions">
        <button class="danger" @click="emit('delete', selectedNode.id)">Delete</button>
        <button class="quiet popover-close" title="Close" aria-label="Close" @click="emit('close')">×</button>
      </div>
    </div>

    <div class="popover-body">
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
          class="mono"
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
            <span class="mono">{{ day }}</span>
          </label>
        </div>

        <div v-else-if="f.kind === 'inputList'" class="input-list-field">
          <div v-for="(row, i) in expressionInputRows(f.key)" :key="i" class="input-list-row">
            <input
              type="text"
              class="input-list-name mono"
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
          <button type="button" class="quiet add-input" @click="onInputRowAdd(f.key)">+ Add input</button>
        </div>

        <div v-else-if="f.kind === 'expression'" class="expression-field">
          <textarea
            :id="`f-${f.key}`"
            class="expression-textarea mono"
            rows="3"
            spellcheck="false"
            :value="String(fieldValue(f.key) ?? f.default)"
            @input="onFieldInput(f.key, 'string', ($event.target as HTMLTextAreaElement).value)"
          />
          <p class="field-hint expression-hint">
            Operators: <code class="mono">{{ EXPR_OPERATORS }}</code><br />
            Builtins: <code class="mono">{{ EXPR_BUILTINS }}</code>
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
          class="mono"
          :min="f.min"
          :step="f.step"
          :value="Number(fieldValue(f.key) ?? f.default)"
          @input="onFieldInput(f.key, 'number', ($event.target as HTMLInputElement).value)"
        />

        <input
          v-else
          :id="`f-${f.key}`"
          type="text"
          class="mono"
          :value="String(fieldValue(f.key) ?? f.default)"
          @input="onFieldInput(f.key, 'string', ($event.target as HTMLInputElement).value)"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.config-popover {
  position: fixed;
  z-index: 40;
  width: 320px;
  max-height: min(70vh, 560px);
  display: flex;
  flex-direction: column;
  background: var(--raised);
  border: 1px solid var(--hairline-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-popover);
}

@media (prefers-reduced-motion: no-preference) {
  .config-popover {
    animation: popover-in 0.12s ease;
  }
}

@keyframes popover-in {
  from {
    opacity: 0;
    transform: translateY(2px) scale(0.98);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.config-popover.side-sheet {
  width: 100%;
  max-height: 60vh;
  border-radius: var(--radius-lg) var(--radius-lg) 0 0;
  border-bottom: none;
}

.popover-caret {
  position: absolute;
  top: 20px;
  width: 9px;
  height: 9px;
  background: var(--raised);
  border: 1px solid var(--hairline-strong);
  transform: rotate(45deg);
}

.side-right .popover-caret {
  left: -5px;
  border-right: none;
  border-top: none;
}

.side-left .popover-caret {
  right: -5px;
  border-left: none;
  border-bottom: none;
}

.popover-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--hairline);
}

.popover-header-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.popover-close {
  font-size: 1rem;
  line-height: 1;
  padding: 2px 8px;
}

.popover-body {
  overflow-y: auto;
  padding: var(--space-4);
}

.issue-list {
  font-size: 0.75rem;
  border-radius: var(--radius-sm);
  padding: var(--space-2);
  margin-bottom: var(--space-3);
  border: 1px solid transparent;
}

.issue-list.error {
  border-color: color-mix(in srgb, var(--signal-alert) 40%, transparent);
  color: var(--signal-alert);
}

.issue-list.warning {
  border-color: color-mix(in srgb, var(--signal-warn) 40%, transparent);
  color: var(--signal-warn);
}

.weekdays-field {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.weekday-toggle {
  font-size: 0.75rem;
  text-transform: capitalize;
}

.input-list-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.input-list-row {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.input-list-name {
  flex: 1;
  min-width: 0;
}

.danger-icon {
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  padding: 0;
  border-color: transparent;
  background: transparent;
  color: var(--ink-faint);
}

.danger-icon:hover {
  color: var(--signal-alert);
  background: color-mix(in srgb, var(--signal-alert) 10%, transparent);
  border-color: transparent;
}

.add-input {
  align-self: flex-start;
  font-size: 0.75rem;
  border-style: dashed;
}

.expression-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.expression-textarea {
  font-size: 0.75rem;
  resize: vertical;
  min-height: 3.5em;
}

.expression-hint {
  line-height: 1.5;
}
</style>
