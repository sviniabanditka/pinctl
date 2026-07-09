<script setup lang="ts">
// The signature element: a tiny rolling trace drawn the way an instrument
// draws a signal. Analog values get a smooth polyline; bool values get a
// square wave — digital signals should look digital, not smoothed into an
// analog shape.
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    values: number[]
    min?: number
    max?: number
    color?: string
    height?: number
    width?: number
    bool?: boolean
    label?: string
  }>(),
  {
    min: 0,
    max: 1023,
    color: 'var(--signal-float)',
    height: 28,
    width: 96,
    bool: false,
    label: '',
  },
)

const VIEW_W = 100

function normY(v: number): number {
  const span = props.max - props.min || 1
  const clamped = Math.min(props.max, Math.max(props.min, v))
  const t = (clamped - props.min) / span
  // SVG y grows downward — invert so higher values draw higher.
  return props.height - t * props.height
}

const midY = computed(() => props.height / 2)

/** Empty state: a flat hairline at the midpoint, not a blank box. */
const flatPath = computed(() => `M0 ${midY.value} L${VIEW_W} ${midY.value}`)

const points = computed(() => {
  const n = props.values.length
  if (n === 0) return []
  if (n === 1) return [{ x: VIEW_W, y: normY(props.values[0]!) }]
  const step = VIEW_W / (n - 1)
  return props.values.map((v, i) => ({ x: i * step, y: normY(v) }))
})

/** Smooth analog trace: a plain polyline through each sample. */
const smoothPath = computed(() => {
  const pts = points.value
  if (pts.length === 0) return flatPath.value
  return pts.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x.toFixed(2)} ${p.y.toFixed(2)}`).join(' ')
})

/** Digital trace: step between samples, then hold — a square wave. */
const squarePath = computed(() => {
  const pts = points.value
  if (pts.length === 0) return flatPath.value
  let d = `M${pts[0]!.x.toFixed(2)} ${pts[0]!.y.toFixed(2)}`
  for (let i = 1; i < pts.length; i++) {
    const prev = pts[i - 1]!
    const cur = pts[i]!
    // Hold the previous level up to the new sample's x, then step.
    d += ` L${cur.x.toFixed(2)} ${prev.y.toFixed(2)} L${cur.x.toFixed(2)} ${cur.y.toFixed(2)}`
  }
  return d
})

const path = computed(() => (props.bool ? squarePath.value : smoothPath.value))

const fillPath = computed(() => {
  if (points.value.length === 0) return ''
  return `${path.value} L${VIEW_W} ${props.height} L0 ${props.height} Z`
})

const current = computed(() => (props.values.length > 0 ? props.values[props.values.length - 1] : undefined))

const ariaLabel = computed(() => {
  const who = props.label || 'signal'
  if (current.value === undefined) return `${who}, no samples yet`
  const val = props.bool ? (current.value ? 'true' : 'false') : current.value
  return `${who}, last ${props.values.length} samples, currently ${val}`
})

const gradientId = computed(() => `spark-fill-${Math.random().toString(36).slice(2, 9)}`)
</script>

<template>
  <svg
    class="sparkline"
    role="img"
    :aria-label="ariaLabel"
    :viewBox="`0 0 ${VIEW_W} ${height}`"
    :width="width"
    :height="height"
    preserveAspectRatio="none"
  >
    <defs>
      <linearGradient :id="gradientId" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" :stop-color="color" stop-opacity="0.08" />
        <stop offset="100%" :stop-color="color" stop-opacity="0" />
      </linearGradient>
    </defs>
    <path v-if="fillPath" :d="fillPath" :fill="`url(#${gradientId})`" stroke="none" />
    <path
      :d="path"
      fill="none"
      :stroke="color"
      stroke-width="1.5"
      vector-effect="non-scaling-stroke"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</template>

<style scoped>
.sparkline {
  display: block;
  overflow: visible;
}
</style>
