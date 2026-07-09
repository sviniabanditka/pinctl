<script setup lang="ts">
import { computed } from 'vue'
import { useDevicesStore } from '../../stores/devices'
import { useEditorStore } from '../../stores/editor'
import { NODE_SCHEMAS, WEEKDAYS } from '../../lib/nodeSchemas'

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

      <div v-if="errors.length > 0" class="issue-list error">
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
</style>
