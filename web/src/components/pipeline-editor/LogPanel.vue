<script setup lang="ts">
// Collapsible bottom panel showing the live `log` node stream while
// debugging (docs/08-node-catalog.md "log", docs/10-api.md's
// "pipeline:{id}" topic's optional `log` array). Entries accumulate in
// editor.liveLog (capped at 200, newest last); rendered newest-first here.
import { computed, ref } from 'vue'
import { useEditorStore } from '../../stores/editor'

const editor = useEditorStore()
const collapsed = ref(false)

const entries = computed(() => [...editor.liveLog].reverse())

function formatTime(ts: string): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function formatValue(v: boolean | number): string {
  return typeof v === 'boolean' ? (v ? 'true' : 'false') : String(v)
}
</script>

<template>
  <div class="log-panel" :class="{ collapsed }">
    <div class="log-panel-header">
      <button class="log-toggle" @click="collapsed = !collapsed">
        {{ collapsed ? '▸' : '▾' }} Log
        <span v-if="entries.length > 0" class="log-count">({{ entries.length }})</span>
      </button>
      <span class="spacer" />
      <button :disabled="entries.length === 0" @click="editor.clearLog()">Clear</button>
    </div>

    <div v-if="!collapsed" class="log-panel-body">
      <div v-if="entries.length === 0" class="log-empty">
        No log entries yet. Add a <code>log</code> node and enable the pipeline to see live values here.
      </div>
      <table v-else class="log-table">
        <thead>
          <tr>
            <th>Time</th>
            <th>Label</th>
            <th>Value</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(e, i) in entries" :key="i">
            <td class="log-time">{{ formatTime(e.timestamp) }}</td>
            <td>{{ e.label }}</td>
            <td class="log-value">{{ formatValue(e.value) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.log-panel {
  flex-shrink: 0;
  border-top: 1px solid var(--color-border);
  background: var(--color-bg-alt);
  display: flex;
  flex-direction: column;
  max-height: 220px;
}

.log-panel.collapsed {
  max-height: none;
}

.log-panel-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
}

.log-toggle {
  background: none;
  border: none;
  padding: 2px 4px;
  font-size: 12px;
  font-weight: 600;
}

.log-count {
  color: var(--color-text-dim);
  font-weight: 400;
}

.spacer {
  flex: 1;
}

.log-panel-body {
  overflow-y: auto;
  padding: 0 10px 8px;
}

.log-empty {
  color: var(--color-text-dim);
  font-size: 12px;
  padding: 8px 0;
}

.log-table {
  font-size: 12px;
}

.log-table th,
.log-table td {
  padding: 3px 8px;
}

.log-time {
  font-variant-numeric: tabular-nums;
  color: var(--color-text-dim);
  white-space: nowrap;
}

.log-value {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}
</style>
