<script setup lang="ts">
import { computed } from 'vue'
import type { NodeProps } from '@vue-flow/core'
import { Handle, Position } from '@vue-flow/core'
import { useDevicesStore } from '../../stores/devices'
import { useEditorStore } from '../../stores/editor'
import { NODE_SCHEMAS, nodeInputs, nodeOutputs, PORT_COLORS, resolvePortType } from '../../lib/nodeSchemas'
import { portTypeContext } from '../../lib/clientValidation'
import type { FlowNodeData } from '../../lib/graphMapping'

const props = defineProps<NodeProps<FlowNodeData>>()

const devices = useDevicesStore()
const editor = useEditorStore()

const schema = computed(() => NODE_SCHEMAS[props.data.nodeType])
const ctx = computed(() => portTypeContext(devices.devices))

const inputs = computed(
  () =>
    (schema.value ? nodeInputs(schema.value, props.data.config) : []).map((p) => ({
      ...p,
      valueType: resolvePortType(p, props.data.config, ctx.value),
    })) ?? [],
)
const outputs = computed(
  () =>
    (schema.value ? nodeOutputs(schema.value, props.data.config) : []).map((p) => ({
      ...p,
      valueType: resolvePortType(p, props.data.config, ctx.value),
    })) ?? [],
)

const summary = computed(() => schema.value?.summary(props.data.config, devices.devices) ?? '')
const isExpression = computed(() => props.data.nodeType === 'expression')

const errors = computed(() => editor.serverErrors[props.id] ?? [])
const missingInputs = computed(() => editor.clientMissingInputs?.[props.id] ?? [])
const hasCycle = computed(() => editor.clientCycleNodes?.has(props.id) ?? false)

const liveActive = computed(() => editor.enabled && editor.liveTick !== null)
const outputValue = computed(() => {
  if (!liveActive.value) return undefined
  const v = editor.liveNodeValues[props.id]
  return v
})

function offset(i: number, total: number): string {
  return `${((i + 1) / (total + 1)) * 100}%`
}

function formatValue(v: boolean | number | undefined): string {
  if (v === undefined) return ''
  return typeof v === 'boolean' ? (v ? 'true' : 'false') : String(Math.round(v * 100) / 100)
}
</script>

<template>
  <div
    class="pipeline-node"
    :class="{ 'has-error': errors.length > 0, 'has-warning': missingInputs.length > 0 || hasCycle }"
  >
    <Handle
      v-for="(p, i) in inputs"
      :key="`in-${p.id}`"
      :id="p.id"
      type="target"
      :position="Position.Left"
      :title="p.id"
      :style="{ top: offset(i, inputs.length), background: PORT_COLORS[p.valueType] }"
    />

    <div class="node-title">{{ schema?.displayName ?? data.nodeType }}</div>
    <div
      class="node-summary"
      :class="{ 'node-summary-code': isExpression, 'node-summary-empty': isExpression && summary === '(empty)' }"
    >
      {{ summary }}
    </div>

    <div v-if="liveActive && outputs.length > 0" class="node-live-badge" :title="`tick ${editor.liveTick}`">
      <span class="live-dot" />
      {{ formatValue(outputValue) }}
    </div>

    <div v-if="errors.length > 0" class="node-error-icon" :title="errors.map((e) => e.message).join('\n')">!</div>
    <div
      v-else-if="missingInputs.length > 0 || hasCycle"
      class="node-warning-icon"
      :title="hasCycle ? 'Part of a cycle' : `Missing required input: ${missingInputs.join(', ')}`"
    >
      ⚠
    </div>

    <Handle
      v-for="(p, i) in outputs"
      :key="`out-${p.id}`"
      :id="p.id"
      type="source"
      :position="Position.Right"
      :title="p.id"
      :style="{ top: offset(i, outputs.length), background: PORT_COLORS[p.valueType] }"
    />
  </div>
</template>

<style scoped>
.pipeline-node {
  position: relative;
  min-width: 150px;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius);
  padding: 8px 10px;
  font-size: 12px;
}

.pipeline-node.has-error {
  border-color: var(--color-danger);
  box-shadow: 0 0 0 1px var(--color-danger);
}

.pipeline-node.has-warning {
  border-color: var(--color-warning);
}

.node-title {
  font-weight: 600;
  margin-bottom: 2px;
}

.node-summary {
  color: var(--color-text-dim);
  font-size: 11px;
}

.node-summary-code {
  font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace);
}

.node-summary-empty {
  font-style: italic;
  opacity: 0.6;
}

.node-live-badge {
  margin-top: 4px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: var(--color-text-dim);
}

.live-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-success);
}

.node-error-icon,
.node-warning-icon {
  position: absolute;
  top: -8px;
  right: -8px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  font-weight: 700;
  color: #fff;
}

.node-error-icon {
  background: var(--color-danger);
}

.node-warning-icon {
  background: var(--color-warning);
}
</style>
