<script setup lang="ts">
import { computed } from 'vue'
import { useEditorStore } from '../../stores/editor'

const emit = defineEmits<{
  save: []
  toggleEnabled: []
  selectErrorNode: [id: string]
}>()

const props = defineProps<{
  toggleBusy: boolean
}>()

const editor = useEditorStore()

const saveDisabled = computed(() => editor.saving || editor.hasClientErrors || !editor.dirty)
const saveTooltip = computed(() => {
  if (editor.hasClientErrors) {
    if (editor.clientCycleNodes.size > 0) return 'Fix: the graph contains a cycle.'
    const first = Object.entries(editor.clientMissingInputs)[0]
    if (first) return `Fix: node ${first[0]} has an unconnected required input (${first[1].join(', ')}).`
  }
  if (!editor.dirty) return 'No unsaved changes.'
  return 'Save changes'
})

const enableDisabled = computed(() => editor.dirty || editor.simulating)

function toggleSimulate() {
  if (editor.simulating) editor.exitSimulate()
  else editor.enterSimulate()
}

function formatSimAt(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toISOString().replace('T', ' ').replace('Z', '')
}

// A pipeline whose board is away keeps ticking and stays enabled
// (docs/07-pipeline-engine.md "Degraded ticks") — the enable toggle alone
// doesn't say that, so a user staring at a graph whose values have stopped
// moving gets told plainly, right next to the toggle, rather than left to
// guess whether the pipeline is broken.
const statusNote = computed(() => {
  if (editor.status === 'paused') return 'Board unreachable — this pipeline resumes on its own when it returns.'
  if (editor.status === 'error') return editor.detail || 'Stopped after repeated node failures — needs a manual re-enable.'
  return ''
})
const statusNoteClass = computed(() => (editor.status === 'error' ? 'alert' : 'warn'))

/** Union of nodes flagged by a server validation error or part of a client-detected cycle. */
const errorNodeIds = computed(() => {
  const ids = new Set<string>()
  for (const [id, issues] of Object.entries(editor.serverErrors)) {
    if (issues.length > 0) ids.add(id)
  }
  for (const id of editor.clientCycleNodes) ids.add(id)
  return ids
})

function selectFirstError() {
  const first = errorNodeIds.value.values().next().value
  if (first) emit('selectErrorNode', first)
}
</script>

<template>
  <div class="toolbar">
    <input
      class="name-field"
      :value="editor.name"
      aria-label="Pipeline name"
      @input="editor.setName(($event.target as HTMLInputElement).value)"
    />

    <label class="tick-field">
      <span class="label">tick</span>
      <input
        type="number"
        min="1"
        class="mono"
        :value="editor.tickMs"
        aria-label="Tick rate in milliseconds"
        @input="editor.setTickMs(Number(($event.target as HTMLInputElement).value))"
      />
      <span class="label">ms</span>
    </label>

    <span v-if="!editor.simulating && editor.enabled && editor.liveTick !== null" class="tick-counter mono num">tick {{ editor.liveTick }}</span>
    <span v-if="editor.simulating && editor.simCurrentFrame" class="tick-counter mono num sim-tick-counter">
      tick {{ editor.simCurrentFrame.tick }} <span class="sim-at">{{ formatSimAt(editor.simCurrentFrame.at) }}</span>
    </span>

    <button v-if="errorNodeIds.size > 0" class="quiet error-count" @click="selectFirstError">
      {{ errorNodeIds.size }} error{{ errorNodeIds.size > 1 ? 's' : '' }}
    </button>

    <span class="spacer" />

    <span v-if="statusNote" class="status-note mono" :class="statusNoteClass" :title="editor.detail || undefined">
      {{ statusNote }}
    </span>

    <span v-if="editor.dirty" class="dot dirty-dot" title="Unsaved changes" />

    <button
      class="simulate-toggle"
      :class="{ active: editor.simulating }"
      :aria-pressed="editor.simulating"
      title="Simulate: run this graph against a fake clock and fake devices — never touches hardware or the store."
      @click="toggleSimulate"
    >
      {{ editor.simulating ? 'Exit simulate' : 'Simulate' }}
    </button>

    <button
      class="enable-toggle"
      :disabled="props.toggleBusy || enableDisabled"
      :title="editor.simulating ? 'Exit simulate mode first' : enableDisabled ? 'Save first' : ''"
      @click="emit('toggleEnabled')"
    >
      <span class="dot" :class="{ running: editor.enabled && editor.status !== 'paused', warn: editor.status === 'paused' }" />
      {{ props.toggleBusy ? '…' : editor.enabled ? 'Disable' : 'Enable' }}
    </button>

    <button class="primary" :disabled="saveDisabled" :title="saveTooltip" @click="emit('save')">
      {{ editor.saving ? 'Saving…' : 'Save' }}
    </button>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-4);
  border-bottom: 1px solid var(--hairline);
  background: var(--surface);
  flex-shrink: 0;
}

.name-field {
  font-weight: 600;
  min-width: 180px;
  background: transparent;
  border-color: transparent;
}

.name-field:hover,
.name-field:focus-visible {
  border-color: var(--hairline);
}

.tick-field {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.tick-field input {
  width: 64px;
}

.tick-counter {
  color: var(--ink-dim);
}

.error-count {
  color: var(--signal-alert);
  border-color: color-mix(in srgb, var(--signal-alert) 40%, transparent);
}

.error-count:hover {
  background: color-mix(in srgb, var(--signal-alert) 10%, transparent);
  border-color: var(--signal-alert);
}

.enable-toggle {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.status-note {
  font-size: 0.75rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 36ch;
}

.status-note.warn {
  color: var(--signal-warn);
}

.status-note.alert {
  color: var(--signal-alert);
}

.spacer {
  flex: 1;
}

.dirty-dot {
  background: var(--signal-warn);
}

.simulate-toggle.active {
  border-color: var(--hairline-strong);
  color: var(--ink);
  background: var(--raised);
}

.sim-tick-counter {
  display: inline-flex;
  gap: var(--space-2);
}

.sim-at {
  color: var(--ink-faint);
}
</style>
