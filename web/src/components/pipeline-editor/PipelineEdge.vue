<script setup lang="ts">
import { computed } from 'vue'
import type { EdgeProps } from '@vue-flow/core'
import { BaseEdge, EdgeLabelRenderer, getBezierPath } from '@vue-flow/core'
import { useEditorStore } from '../../stores/editor'
import { PORT_COLORS, type PortValueType } from '../../lib/nodeSchemas'

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

const color = computed(() => (props.data?.valueType ? PORT_COLORS[props.data.valueType] : 'var(--color-text-dim)'))

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
    (editor.clientCycleNodes.has(props.source) && editor.clientCycleNodes.has(props.target)),
)

const liveActive = computed(() => editor.enabled && editor.liveTick !== null)
const liveValue = computed(() => (liveActive.value ? editor.liveNodeValues[props.source] : undefined))

function formatValue(v: boolean | number | undefined): string {
  if (v === undefined) return ''
  return typeof v === 'boolean' ? (v ? '●' : '○') : String(Math.round(v * 100) / 100)
}
</script>

<template>
  <BaseEdge
    :id="id"
    :path="path[0]"
    :marker-end="markerEnd"
    :style="{ stroke: color, strokeDasharray: isFlagged ? '5,3' : undefined, strokeWidth: 1.5 }"
  />
  <EdgeLabelRenderer v-if="liveActive && liveValue !== undefined">
    <div
      class="edge-live-label"
      :style="{ transform: `translate(-50%, -50%) translate(${path[1]}px, ${path[2]}px)` }"
    >
      {{ formatValue(liveValue) }}
    </div>
  </EdgeLabelRenderer>
</template>

<style scoped>
.edge-live-label {
  position: absolute;
  pointer-events: none;
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 999px;
  background: var(--color-bg-alt);
  border: 1px solid var(--color-border);
  color: var(--color-text);
}
</style>
