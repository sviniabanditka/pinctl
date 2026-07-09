<script setup lang="ts">
import { computed } from 'vue'
import { useEditorStore } from '../../stores/editor'

const emit = defineEmits<{ save: []; toggleEnabled: [] }>()

const props = defineProps<{ toggleBusy: boolean }>()

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
</script>

<template>
  <div class="toolbar">
    <input
      class="name-field"
      :value="editor.name"
      @input="editor.setName(($event.target as HTMLInputElement).value)"
    />
    <label class="tick-field">
      tick
      <input
        type="number"
        min="1"
        :value="editor.tickMs"
        @input="editor.setTickMs(Number(($event.target as HTMLInputElement).value))"
      />
      ms
    </label>

    <button
      :disabled="props.toggleBusy || enableDisabled"
      :title="enableDisabled ? 'Save first' : ''"
      @click="emit('toggleEnabled')"
    >
      {{ props.toggleBusy ? '…' : editor.enabled ? 'Disable' : 'Enable' }}
    </button>

    <span v-if="editor.enabled && editor.liveTick !== null" class="tick-counter">tick {{ editor.liveTick }}</span>

    <span class="spacer" />

    <span v-if="editor.dirty" class="dirty-dot" title="Unsaved changes" />
    <button class="primary" :disabled="saveDisabled" :title="saveTooltip" @click="emit('save')">
      {{ editor.saving ? 'Saving…' : 'Save' }}
    </button>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  border-top: 1px solid var(--color-border);
  background: var(--color-bg-alt);
}

.name-field {
  font-weight: 600;
  min-width: 180px;
}

.tick-field {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--color-text-dim);
}

.tick-field input {
  width: 70px;
}

.tick-counter {
  font-size: 12px;
  color: var(--color-text-dim);
}

.spacer {
  flex: 1;
}

.dirty-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-warning);
}
</style>
