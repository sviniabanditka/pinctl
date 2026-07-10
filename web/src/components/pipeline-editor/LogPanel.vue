<script setup lang="ts">
import { formatNumber } from '../../lib/format'
// Bottom drawer showing the live `log` node stream while debugging
// (docs/08-node-catalog.md "log", docs/10-api.md's "pipeline:{id}" topic's
// optional `log` array). Entries accumulate in editor.liveLog (capped at
// 200, newest last); rendered newest-first here. Hidden by default — the
// parent view owns the open/closed state (persisted to localStorage) and
// toggles it from EditorToolbar; this component just renders the drawer
// body and answers Escape when focus is inside it.
import { computed } from 'vue'
import { useEditorStore } from '../../stores/editor'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const editor = useEditorStore()

const entries = computed(() => [...editor.liveLog].reverse())

function formatTime(ts: string): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function formatValue(v: boolean | number): string {
  return typeof v === 'boolean' ? (v ? 'true' : 'false') : formatNumber(v)
}

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape') {
    ev.preventDefault()
    emit('close')
  }
}
</script>

<template>
  <div v-if="props.open" id="editor-log-panel" class="log-panel" role="region" aria-label="Log" @keydown="onKeydown">
    <div class="log-panel-header">
      <span class="label">Log</span>
      <span class="spacer" />
      <button :disabled="entries.length === 0" @click="editor.clearLog()">Clear</button>
    </div>

    <div class="log-panel-body">
      <div v-if="entries.length === 0" class="log-empty">
        No log entries yet. Add a <code class="mono">log</code> node and enable the pipeline to see live values here.
      </div>
      <table v-else class="log-table">
        <thead>
          <tr>
            <th>Time</th>
            <th>Node</th>
            <th>Value</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(e, i) in entries" :key="i">
            <td class="log-time mono num">{{ formatTime(e.timestamp) }}</td>
            <td class="label log-label">{{ e.label }}</td>
            <td class="log-value mono num" :class="typeof e.value === 'boolean' ? 'value-bool' : 'value-float'">
              {{ formatValue(e.value) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.log-panel {
  flex-shrink: 0;
  height: 200px;
  border-top: 1px solid var(--hairline);
  background: var(--surface);
  display: flex;
  flex-direction: column;
}

.log-panel-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  flex-shrink: 0;
}

.spacer {
  flex: 1;
}

.log-panel-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 0 var(--space-3) var(--space-2);
}

.log-empty {
  color: var(--ink-dim);
  font-size: 0.75rem;
  padding: var(--space-2) 0;
}

.log-table {
  font-size: 0.75rem;
}

.log-table th,
.log-table td {
  padding: 3px var(--space-2);
}

.log-time {
  color: var(--ink-faint);
  white-space: nowrap;
}

.log-label {
  white-space: nowrap;
}

.log-value {
  font-weight: 600;
}
</style>
