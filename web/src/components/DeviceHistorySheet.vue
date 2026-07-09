<script setup lang="ts">
// A dialog, not a route (docs/16-history.md's query API is a pure detail
// view over one device — it doesn't warrant its own URL). Modelled on
// ConfirmDialog/DeviceFormDialog's dialog-backdrop pattern but wider, and on
// CommandPalette's focus-trap/restore-on-close handling since this is the
// first *tall* dialog in the app (bottom sheet below 900px).
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import type { Device } from '../api/types'
import { useHistoryStore } from '../stores/history'
import { pushErrorToast } from '../composables/toast'
import { RANGE_PRESETS, type RangePreset } from '../lib/chart'
import HistoryChart from './HistoryChart.vue'

const props = defineProps<{ device: Device }>()
const emit = defineEmits<{ close: [] }>()

const history = useHistoryStore()
const rangeId = ref<RangePreset['id']>('1h')
const panelEl = ref<HTMLElement | null>(null)
let previouslyFocused: HTMLElement | null = null

async function load() {
  try {
    await history.fetch(props.device.id, rangeId.value)
  } catch (err) {
    // history.disabled is set internally for 503 history_disabled and does
    // not throw — a genuine error (network, 500, ...) reaches here and uses
    // the app's normal toast path, per the contract's "errors otherwise use
    // the existing toast/error-envelope path".
    pushErrorToast(err)
  }
}

function selectRange(id: RangePreset['id']) {
  if (id === rangeId.value) return
  rangeId.value = id
  load()
}

function close() {
  history.abort()
  emit('close')
}

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape') {
    ev.preventDefault()
    close()
    return
  }
  if (ev.key === 'ArrowLeft' || ev.key === 'ArrowRight') {
    // Roving focus within the range segmented control only, when it has
    // focus — arrow keys inside the chart itself are handled by the chart.
    const target = ev.target as HTMLElement
    if (!target.classList.contains('range-btn')) return
    ev.preventDefault()
    const idx = RANGE_PRESETS.findIndex((r) => r.id === rangeId.value)
    const dir = ev.key === 'ArrowRight' ? 1 : -1
    const next = RANGE_PRESETS[(idx + dir + RANGE_PRESETS.length) % RANGE_PRESETS.length]!
    selectRange(next.id)
    nextTick(() => {
      panelEl.value?.querySelector<HTMLElement>(`[data-range="${next.id}"]`)?.focus()
    })
    return
  }
  if (ev.key === 'Tab') {
    const focusables = panelEl.value?.querySelectorAll<HTMLElement>(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
    )
    if (!focusables || focusables.length === 0) return
    const first = focusables[0]!
    const last = focusables[focusables.length - 1]!
    if (ev.shiftKey && document.activeElement === first) {
      ev.preventDefault()
      last.focus()
    } else if (!ev.shiftKey && document.activeElement === last) {
      ev.preventDefault()
      first.focus()
    }
  }
}

onMounted(async () => {
  previouslyFocused = document.activeElement as HTMLElement | null
  await load()
  await nextTick()
  panelEl.value?.querySelector<HTMLElement>(`[data-range="${rangeId.value}"]`)?.focus()
})

onUnmounted(() => {
  history.abort()
  previouslyFocused?.focus()
})

// Re-fetch defensively if the device prop identity changes while mounted
// (shouldn't happen given how HomeView opens this, but keeps the component
// correct if reused elsewhere).
watch(
  () => props.device.id,
  () => load(),
)

const isBool = computed(() => props.device.role === 'digital-in' || props.device.role === 'digital-out')

const latest = computed(() => (history.samples.length > 0 ? history.samples[history.samples.length - 1]! : null))
const summaryMinMax = computed(() => {
  if (history.samples.length === 0) return null
  let lo = Infinity
  let hi = -Infinity
  for (const s of history.samples) {
    lo = Math.min(lo, s.min ?? s.v)
    hi = Math.max(hi, s.max ?? s.v)
  }
  return { lo, hi }
})

function fmt(v: number): string {
  if (isBool.value && !history.bucket) return v ? 'true' : 'false'
  return Number.isInteger(v) ? String(v) : v.toFixed(2)
}
</script>

<template>
  <div class="dialog-backdrop sheet-backdrop" @click.self="close">
    <div
      ref="panelEl"
      class="dialog sheet-panel"
      role="dialog"
      aria-modal="true"
      :aria-label="`${device.name} history`"
      @keydown="onKeydown"
    >
      <div class="sheet-head">
        <div class="sheet-title">
          <h2>{{ device.name }}</h2>
          <span class="label">{{ device.role }}</span>
          <span class="mono ink-faint">pin {{ device.pin }}</span>
        </div>
        <button type="button" class="quiet" aria-label="Close" @click="close">Close</button>
      </div>

      <div class="sheet-controls">
        <div class="label range-caption">Range</div>
        <div class="range-group" role="radiogroup" aria-label="Time range">
          <button
            v-for="r in RANGE_PRESETS"
            :key="r.id"
            type="button"
            class="label range-btn"
            :data-range="r.id"
            role="radio"
            :aria-checked="rangeId === r.id"
            :tabindex="rangeId === r.id ? 0 : -1"
            :class="{ active: rangeId === r.id }"
            @click="selectRange(r.id)"
          >
            {{ r.label }}
          </button>
        </div>
        <span v-if="history.bucket" class="mono num bucket-note">bucket {{ history.bucket }}</span>
      </div>

      <div v-if="history.disabled" class="disabled-note">
        <p class="label">History disabled</p>
        <p>
          This instance keeps no value history. Set <code class="mono">history.enabled: true</code> in
          <code class="mono">config.yaml</code> to start recording.
        </p>
      </div>

      <template v-else>
        <HistoryChart
          :samples="history.samples"
          :role="device.role"
          :from="history.from || new Date(Date.now() - RANGE_PRESETS[0]!.spanMs).toISOString()"
          :to="history.to || new Date().toISOString()"
          :bucket="history.bucket"
          :loading="history.loading"
          :device-name="device.name"
        />

        <div class="mono num summary-line">
          <template v-if="latest && summaryMinMax">
            latest {{ fmt(latest.v) }} · min {{ fmt(summaryMinMax.lo) }} · max {{ fmt(summaryMinMax.hi) }} · {{ history.samples.length }}
            sample{{ history.samples.length === 1 ? '' : 's' }}
          </template>
          <template v-else-if="!history.loading"> 0 samples </template>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.sheet-backdrop {
  align-items: center;
}

.sheet-panel {
  width: min(760px, 94vw);
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.sheet-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}

.sheet-title {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.sheet-title h2 {
  font-size: 1.125rem;
}

.sheet-controls {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.range-caption {
  color: var(--ink-faint);
}

.range-group {
  display: inline-flex;
  border: 1px solid var(--hairline);
  border-radius: var(--radius-sm);
  overflow: hidden;
}

.range-btn {
  background: transparent;
  border: none;
  border-radius: 0;
  padding: 6px 12px;
  color: var(--ink-dim);
}

.range-btn + .range-btn {
  border-left: 1px solid var(--hairline);
}

.range-btn.active {
  background: var(--raised);
  color: var(--ink);
}

.bucket-note {
  color: var(--ink-faint);
}

.disabled-note {
  padding: var(--space-6) var(--space-4);
  text-align: center;
  color: var(--ink-dim);
}

.disabled-note p:first-child {
  margin-bottom: var(--space-2);
}

.summary-line {
  color: var(--ink-dim);
  font-size: 0.8125rem;
}

/* Below 900px: full-width bottom sheet rather than a centered dialog. */
@media (max-width: 900px) {
  .sheet-backdrop {
    align-items: flex-end;
    padding: 0;
  }

  .sheet-panel {
    width: 100%;
    max-width: none;
    max-height: 92vh;
    border-radius: var(--radius-lg) var(--radius-lg) 0 0;
  }
}
</style>
