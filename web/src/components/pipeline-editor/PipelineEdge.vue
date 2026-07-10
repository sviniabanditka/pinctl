<script setup lang="ts">
import { computed } from 'vue'
import type { EdgeProps } from '@vue-flow/core'
import { BaseEdge, EdgeLabelRenderer, getBezierPath } from '@vue-flow/core'
import { useEditorStore } from '../../stores/editor'
import type { PortValueType } from '../../lib/nodeSchemas'

const props = defineProps<EdgeProps<{ valueType?: PortValueType }>>()

const editor = useEditorStore()

const path = computed(() =>
  getBezierPath({
    sourceX: props.sourceX,
    sourceY: props.sourceY,
    sourcePosition: props.sourcePosition,
    targetX: props.targetX,
    targetY: props.targetY,
    targetPosition: props.targetPosition,
  }),
)

// The server's validation error field is a nodeId, not an edge reference
// (internal/api/pipelines.go's validationErrorOut: "Field is populated
// with NodeID... the full detail lives in Message" — a deliberate
// deviation from the edges[N].to shape docs/11-web-ui.md's dashed-red-edge
// design assumed). An edge whose source or target node is flagged gets the
// dashed overlay as the closest available approximation.
const isFlagged = computed(
  () =>
    (editor.serverErrors[props.source]?.length ?? 0) > 0 ||
    (editor.serverErrors[props.target]?.length ?? 0) > 0 ||
    (editor.clientCycleNodes.has(props.source) && editor.clientCycleNodes.has(props.target)) ||
    // Simulate mode: a run that stopped on this edge's source or target
    // node is implicated the same way a server validation error would be.
    (editor.simulating && (editor.simResult?.error?.nodeId === props.source || editor.simResult?.error?.nodeId === props.target)),
)

// editor.displayActive/displayNodeValues: see PipelineNode.vue's identical
// comment — this is the same live/simulated branch point, reused so an
// edge's rendering can't tell which mode fed it.
const liveActive = computed(() => editor.displayActive)
const liveValue = computed(() => (liveActive.value ? editor.displayNodeValues[props.source] : undefined))

/** Chrome is monochrome; colour + the travelling-dash flourish are reserved for a running pipeline's live value. */
const signalColor = computed(() => {
  if (props.data?.valueType === 'bool') return 'var(--signal-bool)'
  if (props.data?.valueType === 'float64') return 'var(--signal-float)'
  return 'var(--hairline-strong)'
})

const flowing = computed(() => liveActive.value && liveValue.value !== undefined && !isFlagged.value)

const edgeStyle = computed(() => {
  if (isFlagged.value) {
    return { stroke: 'var(--signal-alert)', strokeDasharray: '5,3', strokeWidth: 1.5 }
  }
  if (flowing.value) {
    return {
      stroke: signalColor.value,
      strokeWidth: 1.5,
      strokeDasharray: '6 6',
      animation: 'pinctl-edge-flow 2s linear infinite',
    }
  }
  return { stroke: 'var(--hairline-strong)', strokeWidth: 1.5 }
})

const isBool = computed(() => typeof liveValue.value === 'boolean')

function formatValue(v: boolean | number | undefined): string {
  if (v === undefined) return ''
  return typeof v === 'boolean' ? (v ? 'true' : 'false') : String(Math.round(v * 100) / 100)
}
</script>

<template>
  <BaseEdge :id="id" :path="path[0]" :marker-end="markerEnd" :style="edgeStyle" />
  <EdgeLabelRenderer v-if="liveActive && liveValue !== undefined">
    <!-- bool: a small ring/dot glyph in place of a "true"/"false" pill -->
    <svg
      v-if="isBool"
      class="edge-bool-glyph"
      width="14"
      height="14"
      viewBox="0 0 14 14"
      role="img"
      :aria-label="liveValue ? 'true' : 'false'"
      :style="{ transform: `translate(-50%, -50%) translate(${path[1]}px, ${path[2]}px)` }"
    >
      <title>{{ liveValue ? 'true' : 'false' }}</title>
      <circle class="bool-backing" cx="7" cy="7" r="7" />
      <circle class="bool-ring" cx="7" cy="7" r="5" />
      <circle class="bool-inner" :class="{ 'is-true': liveValue === true }" cx="7" cy="7" />
    </svg>
    <div
      v-else
      class="edge-live-label mono num value-float"
      :style="{ transform: `translate(-50%, -50%) translate(${path[1]}px, ${path[2]}px)` }"
    >
      {{ formatValue(liveValue) }}
    </div>
  </EdgeLabelRenderer>
</template>

<style>
/* Unscoped: referenced via inline `animation` so it must live in the global
   stylesheet, not under this component's scoped data-v attribute. Disabled
   automatically by base.css's blanket prefers-reduced-motion rule. */
@keyframes pinctl-edge-flow {
  to {
    stroke-dashoffset: -24;
  }
}
</style>

<style scoped>
.edge-live-label {
  position: absolute;
  pointer-events: none;
  font-size: 0.6875rem;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--raised);
  border: 1px solid var(--hairline-strong);
  transition: opacity 0.15s ease;
}

/* bool glyph: a --panel-coloured backing disc (so the edge's travelling
   dashes don't show through and muddy the ring), a hollow ring in
   --signal-bool, and — only when true — a smaller filled disc concentric
   inside it, with a clear gap to the ring's inner edge. false -> true is a
   change of the inner disc's radius/opacity (transition below), not a
   flash; base.css's blanket prefers-reduced-motion rule zeroes the
   transition duration, so this is inert under reduced motion for free. */
.edge-bool-glyph {
  position: absolute;
  pointer-events: none;
  overflow: visible;
}

.bool-backing {
  fill: var(--panel);
}

.bool-ring {
  fill: none;
  stroke: var(--signal-bool);
  stroke-width: 2;
}

.bool-inner {
  fill: var(--signal-bool);
  r: 0;
  opacity: 0;
  transition:
    r 0.15s ease,
    opacity 0.15s ease;
}

.bool-inner.is-true {
  r: 2px;
  opacity: 1;
}

</style>
