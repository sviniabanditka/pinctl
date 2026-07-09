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

const enableDisabled = computed(() => editor.dirty)

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

    <span v-if="editor.enabled && editor.liveTick !== null" class="tick-counter mono num">tick {{ editor.liveTick }}</span>

    <button v-if="errorNodeIds.size > 0" class="quiet error-count" @click="selectFirstError">
      {{ errorNodeIds.size }} error{{ errorNodeIds.size > 1 ? 's' : '' }}
    </button>

    <span class="spacer" />

    <span v-if="editor.dirty" class="dot dirty-dot" title="Unsaved changes" />

    <button
      class="enable-toggle"
      :disabled="props.toggleBusy || enableDisabled"
      :title="enableDisabled ? 'Save first' : ''"
      @click="emit('toggleEnabled')"
    >
      <span class="dot" :class="{ running: editor.enabled }" />
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

.spacer {
  flex: 1;
}

.dirty-dot {
  background: var(--signal-warn);
}
</style>
