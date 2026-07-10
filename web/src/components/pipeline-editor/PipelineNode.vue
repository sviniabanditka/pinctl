<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { NodeProps } from '@vue-flow/core'
import { Handle, Position } from '@vue-flow/core'
import { useDevicesStore } from '../../stores/devices'
import { useEditorStore } from '../../stores/editor'
import { NODE_SCHEMAS, nodeInputs, nodeOutputs, PORT_COLORS, resolvePortType } from '../../lib/nodeSchemas'
import { portTypeContext } from '../../lib/clientValidation'
import { isStaleTickValue } from '../../lib/staleness'
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
const isEmptySummary = computed(() => summary.value === '' || summary.value === '(empty)' || summary.value === '(no device)')

/** Small monochrome glyph distinguishing source/transform/sink at a glance. */
const glyph = computed(() => {
  switch (schema.value?.group) {
    case 'sources':
      return '▷'
    case 'sinks':
      return '■'
    default:
      return '◆'
  }
})

const errors = computed(() => editor.serverErrors[props.id] ?? [])
const missingInputs = computed(() => editor.clientMissingInputs?.[props.id] ?? [])
const hasCycle = computed(() => editor.clientCycleNodes?.has(props.id) ?? false)

const liveActive = computed(() => editor.enabled && editor.liveTick !== null)
const outputValue = computed(() => {
  if (!liveActive.value) return undefined
  return editor.liveNodeValues[props.id]
})

/** While the pipeline is paused (docs/07-pipeline-engine.md "Degraded
 * ticks"), a degraded tick abandons evaluation at the unreachable read, so
 * a node downstream of it simply stops gaining fresh entries in the tick
 * snapshot rather than erroring — this node's last-shown value can go
 * stale silently. Depending on `editor.liveTick` (rather than only
 * `liveNodeUpdatedAt`) means this recomputes every tick even when *this*
 * node's own entry didn't refresh, so the dimming kicks in the moment
 * enough tick periods have passed, not only when some other value change
 * happens to re-render the node. */
const stale = computed(() => {
  void editor.liveTick
  return editor.status === 'paused' && isStaleTickValue(editor.liveNodeUpdatedAt[props.id], editor.tickMs)
})

/** True when the live value's rendered text is identical to the config
 * summary's (e.g. `constant`: summary is the literal value, live value is
 * that same literal echoed back tick after tick) — in that case the
 * summary line is pure duplication and only the (more informative,
 * animating) live value renders. */
const summaryDuplicatesLive = computed(
  () => liveActive.value && outputValue.value !== undefined && formatValue(outputValue.value) === summary.value,
)

const pulsing = ref(false)
let pulseTimer: ReturnType<typeof setTimeout> | null = null
watch(outputValue, (next, prev) => {
  if (next === undefined || prev === undefined || next === prev) return
  pulsing.value = false
  requestAnimationFrame(() => {
    pulsing.value = true
    if (pulseTimer) clearTimeout(pulseTimer)
    pulseTimer = setTimeout(() => (pulsing.value = false), 300)
  })
})

// -- port layout ------------------------------------------------------------
// Each side's ports are distributed evenly across the *node's* full height
// (inputs and outputs independently), centred as a group: the i-th of n
// ports on a side sits at `(i + 0.5) / n * 100%`. n=1 -> dead centre; n=2 ->
// 25%/75%; n=3 -> one centred with one symmetrically above and below. This
// is expressed as a CSS percentage (clamped a few px in from the top/bottom
// so a port on a busy side never lands on the card's rounded corner) so it
// stays correct as the node resizes, without any JS measurement.
// The node's height is otherwise content-driven (`.node-content` is
// `align-self: center`, see below), with `min-height` only guaranteeing at
// least ~24px of vertical room per port on the busier side, plus a fixed
// allowance for the header row + the content column's own top/bottom
// padding (var(--space-3) * 2 + the header line) so ports never encroach on
// the title.
const PORT_MIN_PITCH = 24
const HEADER_RESERVE = 40
const PORT_EDGE_INSET = '12px'

function portTop(i: number, total: number): string {
  const pct = ((i + 0.5) / total) * 100
  return `clamp(${PORT_EDGE_INSET}, ${pct}%, calc(100% - ${PORT_EDGE_INSET}))`
}

const minHeight = computed(
  () => Math.max(inputs.value.length, outputs.value.length, 1) * PORT_MIN_PITCH + HEADER_RESERVE,
)

function formatValue(v: boolean | number | undefined): string {
  if (v === undefined) return ''
  return typeof v === 'boolean' ? (v ? 'true' : 'false') : String(Math.round(v * 100) / 100)
}

function valueClass(v: boolean | number | undefined): string {
  return typeof v === 'boolean' ? 'value-bool' : 'value-float'
}
</script>

<template>
  <div
    class="pipeline-node"
    :class="{
      selected: props.selected,
      'has-error': errors.length > 0,
      'has-warning': errors.length === 0 && (missingInputs.length > 0 || hasCycle),
      'is-expression': isExpression,
    }"
    :style="{ minHeight: `${minHeight}px` }"
  >
    <!-- Left gutter: input handles + their mono labels, never entered by content. -->
    <div class="node-gutter node-gutter-left" aria-hidden="true">
      <span
        v-for="(p, i) in inputs"
        :key="`in-label-${p.id}`"
        class="port-label port-label-left mono"
        :style="{ top: portTop(i, inputs.length) }"
        :title="p.id"
        >{{ p.id }}</span
      >
    </div>
    <Handle
      v-for="(p, i) in inputs"
      :key="`in-${p.id}`"
      :id="p.id"
      type="target"
      :position="Position.Left"
      class="port"
      :class="`port-${p.valueType}`"
      :style="{ top: portTop(i, inputs.length), background: PORT_COLORS[p.valueType] }"
    />

    <div class="node-content">
      <div class="node-header">
        <span class="node-glyph" aria-hidden="true">{{ glyph }}</span>
        <span class="label node-title">{{ schema?.displayName ?? data.nodeType }}</span>
      </div>
      <div
        v-if="!summaryDuplicatesLive"
        class="node-summary mono"
        :class="{ 'node-summary-empty': isEmptySummary }"
        :title="isEmptySummary ? undefined : summary"
      >
        {{ isEmptySummary ? '—' : summary }}
      </div>

      <div
        v-if="liveActive && outputs.length > 0 && outputValue !== undefined"
        class="node-live-value"
        :class="{ 'node-live-value-stale': stale }"
        :title="stale ? 'Board unreachable — this value stopped updating.' : undefined"
      >
        <span class="mono num" :class="[valueClass(outputValue), { pulse: pulsing }]">{{ formatValue(outputValue) }}</span>
      </div>
    </div>

    <div v-if="errors.length > 0" class="node-status-dot error" :title="errors.map((e) => e.message).join('\n')" />
    <div
      v-else-if="missingInputs.length > 0 || hasCycle"
      class="node-status-dot warning"
      :title="hasCycle ? 'Part of a cycle' : `Missing required input: ${missingInputs.join(', ')}`"
    />

    <!-- Right gutter: output handles + their mono labels, never entered by content. -->
    <div class="node-gutter node-gutter-right" aria-hidden="true">
      <span
        v-for="(p, i) in outputs"
        :key="`out-label-${p.id}`"
        class="port-label port-label-right mono"
        :style="{ top: portTop(i, outputs.length) }"
        :title="p.id"
        >{{ p.id }}</span
      >
    </div>
    <Handle
      v-for="(p, i) in outputs"
      :key="`out-${p.id}`"
      :id="p.id"
      type="source"
      :position="Position.Right"
      class="port"
      :class="`port-${p.valueType}`"
      :style="{ top: portTop(i, outputs.length), background: PORT_COLORS[p.valueType] }"
    />
  </div>
</template>

<style scoped>
.pipeline-node {
  position: relative;
  min-width: 140px;
  max-width: 280px;
  display: grid;
  grid-template-columns: 28px 1fr 28px;
  background: var(--surface);
  border: 1px solid var(--hairline);
  border-radius: var(--radius-lg);
  font-size: 0.8125rem;
  transition: border-color 0.12s ease;
}

.pipeline-node.selected {
  border: 1px solid var(--ink);
}

.pipeline-node.has-error {
  border-color: var(--signal-alert);
}

.pipeline-node.has-warning {
  border-color: var(--signal-warn);
}

.node-content {
  grid-column: 2 / 3;
  min-width: 0;
  padding: var(--space-3) 0;
  /* Centre the content column within whatever height the node ends up at
     (port-count-driven or content-driven, see minHeight above) — the
     gutters stay `align-self: stretch` (the grid default) so their ports
     still span the node's full height, only the content block itself is
     centred. */
  align-self: center;
}

.node-header {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-bottom: 2px;
}

.node-glyph {
  color: var(--ink-faint);
  font-size: 0.625rem;
}

.node-title {
  color: var(--ink);
}

.node-summary {
  color: var(--ink-dim);
  font-size: 0.75rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.node-summary-empty {
  color: var(--ink-faint);
}

.node-live-value {
  margin-top: var(--space-1);
  display: flex;
}

/* Board unreachable (docs/07-pipeline-engine.md "Degraded ticks") — this
   node's value stopped refreshing; dim it rather than let a frozen number
   keep looking current. The toolbar carries the one-time "why" copy, so
   this is deliberately just a visual cue, not another repeated string. */
.node-live-value-stale .mono {
  opacity: 0.4;
}


.pulse {
  animation: value-pulse 0.3s ease;
}

@keyframes value-pulse {
  0% {
    opacity: 0.35;
  }
  100% {
    opacity: 1;
  }
}

.node-status-dot {
  position: absolute;
  top: -4px;
  right: -4px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  border: 1px solid var(--surface);
}

.node-status-dot.error {
  background: var(--signal-alert);
}

.node-status-dot.warning {
  background: var(--signal-warn);
}

.port {
  width: 8px;
  height: 8px;
  border: none;
  z-index: 1;
}

/* Left/right gutters: 28px reserved lanes that content never enters. Handles
   (rendered as siblings, positioned by Vue Flow at the node's outer edge)
   and their labels share the same `portTop()` offset (a percentage of the
   node's full height, see the script block) so a label is always vertically
   centred on its handle, evenly distributed with its side's other ports. */
.node-gutter {
  position: relative;
  grid-row: 1 / -1;
}

.node-gutter-left {
  grid-column: 1 / 2;
}

.node-gutter-right {
  grid-column: 3 / 4;
}

.port-label {
  position: absolute;
  transform: translateY(-50%);
  max-width: 24px;
  font-size: 0.625rem;
  color: var(--ink-faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  pointer-events: none;
}

.port-label-left {
  left: 4px;
  text-align: left;
}

.port-label-right {
  right: 4px;
  text-align: right;
}
</style>
