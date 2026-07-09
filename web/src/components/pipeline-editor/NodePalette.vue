<script setup lang="ts">
import { NODE_SCHEMAS, PALETTE_GROUPS } from '../../lib/nodeSchemas'

const emit = defineEmits<{ add: [type: string] }>()

function nodesInGroup(group: string) {
  return Object.values(NODE_SCHEMAS).filter((s) => s.group === group)
}

function onDragStart(ev: DragEvent, type: string) {
  ev.dataTransfer?.setData('application/pinctl-node-type', type)
  if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'move'
}
</script>

<template>
  <aside class="palette">
    <div v-for="group in PALETTE_GROUPS" :key="group.key" class="palette-group">
      <h3>{{ group.label }}</h3>
      <div
        v-for="schema in nodesInGroup(group.key)"
        :key="schema.type"
        class="palette-card"
        draggable="true"
        @dragstart="onDragStart($event, schema.type)"
        @click="emit('add', schema.type)"
      >
        <div class="palette-card-title">{{ schema.displayName }}</div>
        <div class="palette-card-desc">{{ schema.description }}</div>
      </div>
    </div>
  </aside>
</template>

<style scoped>
.palette {
  width: 200px;
  flex-shrink: 0;
  border-right: 1px solid var(--color-border);
  padding: 12px;
  overflow-y: auto;
}

.palette-group {
  margin-bottom: 16px;
}

.palette-group h3 {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.03em;
  color: var(--color-text-dim);
  margin: 0 0 6px;
}

.palette-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius);
  padding: 8px;
  margin-bottom: 6px;
  cursor: grab;
}

.palette-card:hover {
  border-color: var(--color-accent);
}

.palette-card-title {
  font-weight: 600;
  font-size: 12px;
}

.palette-card-desc {
  font-size: 11px;
  color: var(--color-text-dim);
  margin-top: 2px;
}
</style>
