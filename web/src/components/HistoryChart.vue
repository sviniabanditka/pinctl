<script setup lang="ts">
// Hand-rolled inline SVG, same spirit as Sparkline.vue but with real axes,
// a hover/keyboard crosshair, and gap-aware paths — Sparkline draws a live
// rolling buffer with no known time axis; this draws a queried [from, to)
// range where a missing bucket is a real, drawable absence.
import { computed, ref } from 'vue'
import type { DeviceRole } from '../api/types'
import {
  analogYDomain,
  boolRenderMode,
  boolYDomain,
  formatTimeTick,
  gapThresholdMs,
  niceValueTicks,
  parseDurationMs,
  scaleX,
  scaleY,
  segmentByGap,
  timeTicks,
  type HistorySamplePoint,
  type YDomain,
} from '../lib/chart'

const props = withDefaults(
  defineProps<{
    samples: HistorySamplePoint[]
    role: DeviceRole
    from: string
    to: string
    bucket?: string
    loading?: boolean
    error?: string | null
    /** Used only for the aria-label summary; falls back to `role`. */
    deviceName?: string
  }>(),
  {
    bucket: undefined,
    loading: false,
    error: null,
    deviceName: undefined,
  },
)

const VB_W = 640
const VB_H = 220
const MARGIN = { top: 12, right: 16, bottom: 28, left: 48 }
const PLOT_W = VB_W - MARGIN.left - MARGIN.right
const PLOT_H = VB_H - MARGIN.top - MARGIN.bottom

const isBool = computed(() => props.role === 'digital-in' || props.role === 'digital-out')
const isBucketed = computed(() => !!props.bucket)
const bucketMs = computed(() => (props.bucket ? parseDurationMs(props.bucket) : undefined))
const renderMode = computed(() => (isBool.value ? boolRenderMode(isBucketed.value) : 'analog'))

const yDomain = computed<YDomain>(() => (isBool.value ? boolYDomain() : analogYDomain(props.samples)))

const fromMs = computed(() => Date.parse(props.from))
const toMs = computed(() => Date.parse(props.to))
const spanMs = computed(() => Math.max(toMs.value - fromMs.value, 1))

function px(t: string): number {
  return MARGIN.left + scaleX(t, props.from, props.to, PLOT_W)
}
function py(v: number): number {
  return MARGIN.top + scaleY(v, yDomain.value, PLOT_H)
}
const baselineY = computed(() => py(0))

// -- gap segmentation --------------------------------------------------------
const segments = computed<HistorySamplePoint[][]>(() => {
  const threshold = gapThresholdMs(props.samples, bucketMs.value)
  return segmentByGap(props.samples, threshold)
})

function pathFor(seg: HistorySamplePoint[]): string {
  if (seg.length === 0) return ''
  return seg.map((s, i) => `${i === 0 ? 'M' : 'L'}${px(s.t).toFixed(2)} ${py(s.v).toFixed(2)}`).join(' ')
}

/** Step (hold-then-jump) path for a raw bool square wave. */
function squarePathFor(seg: HistorySamplePoint[]): string {
  if (seg.length === 0) return ''
  let d = `M${px(seg[0]!.t).toFixed(2)} ${py(seg[0]!.v).toFixed(2)}`
  for (let i = 1; i < seg.length; i++) {
    const prev = seg[i - 1]!
    const cur = seg[i]!
    d += ` L${px(cur.t).toFixed(2)} ${py(prev.v).toFixed(2)} L${px(cur.t).toFixed(2)} ${py(cur.v).toFixed(2)}`
  }
  return d
}

/** Step area from the 0 baseline — the duty-cycle rendering. Deliberately a
 * stepped fill, not a smooth polyline: a bucketed bool average is a duty
 * cycle, a different kind of quantity from an analog reading, and must not
 * read as one. */
function dutyAreaFor(seg: HistorySamplePoint[]): string {
  if (seg.length === 0) return ''
  const x0 = px(seg[0]!.t)
  let d = `M${x0.toFixed(2)} ${baselineY.value.toFixed(2)} L${x0.toFixed(2)} ${py(seg[0]!.v).toFixed(2)}`
  for (let i = 1; i < seg.length; i++) {
    const prev = seg[i - 1]!
    const cur = seg[i]!
    d += ` L${px(cur.t).toFixed(2)} ${py(prev.v).toFixed(2)} L${px(cur.t).toFixed(2)} ${py(cur.v).toFixed(2)}`
  }
  const xLast = px(seg[seg.length - 1]!.t)
  d += ` L${xLast.toFixed(2)} ${baselineY.value.toFixed(2)} Z`
  return d
}

/** Min/max band path for a bucketed analog segment — drawn behind the
 * average line so a spike the average smooths away is still visible. */
function bandPathFor(seg: HistorySamplePoint[]): string {
  if (seg.length === 0) return ''
  const top = seg.map((s) => `${px(s.t).toFixed(2)} ${py(s.max ?? s.v).toFixed(2)}`)
  const bottom = seg
    .slice()
    .reverse()
    .map((s) => `${px(s.t).toFixed(2)} ${py(s.min ?? s.v).toFixed(2)}`)
  return `M${top.join(' L')} L${bottom.join(' L')} Z`
}

const analogSegmentPaths = computed(() => (renderMode.value === 'analog' ? segments.value.map(pathFor) : []))
const analogBandPaths = computed(() =>
  renderMode.value === 'analog' && isBucketed.value ? segments.value.map(bandPathFor) : [],
)
const squareSegmentPaths = computed(() => (renderMode.value === 'square' ? segments.value.map(squarePathFor) : []))
const dutySegmentPaths = computed(() => (renderMode.value === 'duty' ? segments.value.map(dutyAreaFor) : []))

// -- axes --------------------------------------------------------------------
const valueTicks = computed(() => (isBool.value ? [0, 1] : niceValueTicks(yDomain.value, 4)))
const timeTickMs = computed(() => timeTicks(fromMs.value, toMs.value, 5))

function formatValueTick(v: number): string {
  if (isBool.value) return v === 1 ? '1' : '0'
  return Number.isInteger(v) ? String(v) : v.toFixed(2)
}

// -- crosshair / hover / keyboard --------------------------------------------
const hoverIndex = ref<number | null>(null)
const focusIndex = ref<number | null>(null)
const activeIndex = computed(() => hoverIndex.value ?? focusIndex.value)
const activeSample = computed(() => (activeIndex.value !== null ? props.samples[activeIndex.value] ?? null : null))

function nearestIndex(clientX: number, svg: SVGSVGElement): number | null {
  if (props.samples.length === 0) return null
  const rect = svg.getBoundingClientRect()
  const relX = ((clientX - rect.left) / rect.width) * VB_W
  let best = 0
  let bestDist = Infinity
  for (let i = 0; i < props.samples.length; i++) {
    const d = Math.abs(px(props.samples[i]!.t) - relX)
    if (d < bestDist) {
      bestDist = d
      best = i
    }
  }
  return best
}

function onMouseMove(ev: MouseEvent) {
  const svg = ev.currentTarget as SVGSVGElement
  hoverIndex.value = nearestIndex(ev.clientX, svg)
}
function onMouseLeave() {
  hoverIndex.value = null
}

function onKeydown(ev: KeyboardEvent) {
  if (props.samples.length === 0) return
  if (ev.key === 'ArrowRight') {
    ev.preventDefault()
    focusIndex.value = Math.min((focusIndex.value ?? -1) + 1, props.samples.length - 1)
  } else if (ev.key === 'ArrowLeft') {
    ev.preventDefault()
    focusIndex.value = Math.max((focusIndex.value ?? props.samples.length) - 1, 0)
  } else if (ev.key === 'Home') {
    ev.preventDefault()
    focusIndex.value = 0
  } else if (ev.key === 'End') {
    ev.preventDefault()
    focusIndex.value = props.samples.length - 1
  }
}

function fmtTimestamp(t: string): string {
  const d = new Date(t)
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`
}
function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

function fmtVal(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(2)
}

const crosshairReadout = computed(() => {
  const s = activeSample.value
  if (!s) return ''
  const base = `${fmtTimestamp(s.t)}  ${isBool.value ? (isBucketed.value ? `duty ${fmtVal(s.v)}` : s.v ? '1' : '0') : fmtVal(s.v)}`
  if (isBucketed.value && s.min !== undefined && s.max !== undefined) {
    return `${base}  min ${fmtVal(s.min)}  max ${fmtVal(s.max)}`
  }
  return base
})

// -- accessibility summary ----------------------------------------------------
const summary = computed(() => {
  const who = props.deviceName ?? props.role
  if (props.samples.length === 0) return `${who} history, no samples in range`
  const latest = props.samples[props.samples.length - 1]!
  let lo = Infinity
  let hi = -Infinity
  for (const s of props.samples) {
    lo = Math.min(lo, s.min ?? s.v)
    hi = Math.max(hi, s.max ?? s.v)
  }
  const latestStr = isBool.value && !isBucketed.value ? (latest.v ? 'true' : 'false') : fmtVal(latest.v)
  return `${who} history from ${props.from} to ${props.to}, latest ${latestStr}, min ${fmtVal(lo)}, max ${fmtVal(hi)}`
})
</script>

<template>
  <div class="history-chart">
    <svg
      class="chart-svg"
      role="img"
      tabindex="0"
      :aria-label="summary"
      :viewBox="`0 0 ${VB_W} ${VB_H}`"
      preserveAspectRatio="none"
      @mousemove="onMouseMove"
      @mouseleave="onMouseLeave"
      @keydown="onKeydown"
    >
      <!-- gridlines -->
      <g class="grid">
        <line
          v-for="v in valueTicks"
          :key="`gy-${v}`"
          :x1="MARGIN.left"
          :x2="VB_W - MARGIN.right"
          :y1="py(v)"
          :y2="py(v)"
        />
        <line
          v-for="ms in timeTickMs"
          :key="`gx-${ms}`"
          :x1="MARGIN.left + scaleX(new Date(ms).toISOString(), from, to, PLOT_W)"
          :x2="MARGIN.left + scaleX(new Date(ms).toISOString(), from, to, PLOT_W)"
          :y1="MARGIN.top"
          :y2="VB_H - MARGIN.bottom"
        />
      </g>

      <!-- empty state -->
      <text v-if="samples.length === 0 && !loading" class="label empty-label" :x="VB_W / 2" :y="VB_H / 2" text-anchor="middle">
        No samples in this range
      </text>
      <text v-else-if="loading" class="label empty-label" :x="VB_W / 2" :y="VB_H / 2" text-anchor="middle">Loading…</text>

      <template v-else>
        <!-- analog: min/max band, then average line -->
        <template v-if="renderMode === 'analog'">
          <path v-for="(d, i) in analogBandPaths" :key="`band-${i}`" :d="d" class="band" />
          <path v-for="(d, i) in analogSegmentPaths" :key="`line-${i}`" :d="d" class="line line-analog" fill="none" />
        </template>

        <!-- raw bool: square wave -->
        <template v-else-if="renderMode === 'square'">
          <path v-for="(d, i) in squareSegmentPaths" :key="`sq-${i}`" :d="d" class="line line-bool" fill="none" />
        </template>

        <!-- bucketed bool: duty-cycle filled area -->
        <template v-else>
          <path v-for="(d, i) in dutySegmentPaths" :key="`duty-${i}`" :d="d" class="duty" />
        </template>

        <!-- crosshair -->
        <line
          v-if="activeSample"
          class="crosshair"
          :x1="px(activeSample.t)"
          :x2="px(activeSample.t)"
          :y1="MARGIN.top"
          :y2="VB_H - MARGIN.bottom"
        />
        <circle v-if="activeSample" class="crosshair-dot" :cx="px(activeSample.t)" :cy="py(activeSample.v)" r="3" />
      </template>

      <!-- axis labels -->
      <g class="axis-labels">
        <text
          v-for="v in valueTicks"
          :key="`ly-${v}`"
          class="mono num tick-label"
          :x="MARGIN.left - 6"
          :y="py(v)"
          text-anchor="end"
          dominant-baseline="middle"
        >
          {{ formatValueTick(v) }}
        </text>
        <text
          v-for="ms in timeTickMs"
          :key="`lx-${ms}`"
          class="mono num tick-label"
          :x="MARGIN.left + scaleX(new Date(ms).toISOString(), from, to, PLOT_W)"
          :y="VB_H - MARGIN.bottom + 16"
          text-anchor="middle"
        >
          {{ formatTimeTick(ms, spanMs) }}
        </text>
      </g>

      <text v-if="isBucketed && isBool" class="label duty-caption" :x="MARGIN.left" :y="MARGIN.top - 2">duty cycle</text>
    </svg>

    <div class="mono crosshair-readout" aria-live="polite">{{ crosshairReadout }}</div>
  </div>
</template>

<style scoped>
.history-chart {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.chart-svg {
  display: block;
  width: 100%;
  height: 220px;
  overflow: visible;
}

.chart-svg:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
  border-radius: var(--radius-sm);
}

.grid line {
  stroke: var(--ink-faint);
  stroke-width: 1;
  opacity: 0.25;
  vector-effect: non-scaling-stroke;
}

.empty-label {
  fill: var(--ink-faint);
}

.line {
  stroke-width: 1.5;
  stroke-linecap: round;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}

.line-analog {
  stroke: var(--signal-float);
}

.line-bool {
  stroke: var(--signal-bool);
}

.band {
  fill: var(--signal-float);
  fill-opacity: 0.12;
  stroke: none;
}

.duty {
  fill: var(--signal-bool);
  fill-opacity: 0.35;
  stroke: var(--signal-bool);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}

.duty-caption {
  fill: var(--ink-faint);
}

.crosshair {
  stroke: var(--ink-dim);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}

.crosshair-dot {
  fill: var(--ink);
  stroke: var(--panel);
  stroke-width: 1;
}

.tick-label {
  fill: var(--ink-faint);
  font-size: 9px;
}

.crosshair-readout {
  min-height: 1.2em;
  font-size: 0.75rem;
  color: var(--ink-dim);
}
</style>
