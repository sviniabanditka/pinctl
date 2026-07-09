<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { useBoardsStore } from '../stores/boards'
import { pushErrorToast, pushToast } from '../composables/toast'
import StatusBadge from '../components/StatusBadge.vue'
import type { BoardEntry } from '../api/types'

const boards = useBoardsStore()

let unsubLive: (() => void) | null = null

onMounted(async () => {
  unsubLive = boards.subscribeLive()
  if (!boards.loaded) {
    await boards.fetch()
    if (boards.error) pushErrorToast(new Error('Failed to load boards'))
  }
})

onUnmounted(() => unsubLive?.())

const editingId = ref<string | null>(null)
const renameBusy = ref<Record<string, boolean>>({})
const draft = reactive({ name: '' })

function startRename(b: BoardEntry) {
  editingId.value = b.id
  draft.name = b.name
}

function cancelRename() {
  editingId.value = null
}

async function confirmRename(b: BoardEntry) {
  const name = draft.name.trim()
  if (!name || name === b.name) {
    editingId.value = null
    return
  }
  renameBusy.value[b.id] = true
  try {
    await boards.rename(b.id, name)
    pushToast('Board renamed', 'info')
    editingId.value = null
  } catch (err) {
    pushErrorToast(err)
  } finally {
    renameBusy.value[b.id] = false
  }
}

async function onRescan() {
  try {
    await boards.rescan()
    pushToast('Rescan triggered — new boards will appear shortly.', 'info')
  } catch (err) {
    pushErrorToast(err)
  }
}
</script>

<template>
  <div class="header-row">
    <h1>Boards</h1>
    <button class="primary" :disabled="boards.rescanning" @click="onRescan">
      {{ boards.rescanning ? 'Scanning…' : 'Rescan' }}
    </button>
  </div>

  <div v-if="boards.loading && !boards.loaded" class="skeleton">Loading…</div>

  <div v-else-if="boards.boards.length === 0" class="empty-state card">
    <h3>No boards found</h3>
    <p>Connect a board and click Rescan, or check the runtime log for detection.</p>
  </div>

  <table v-else>
    <thead>
      <tr>
        <th>Name</th>
        <th>Identity</th>
        <th>Port</th>
        <th>Status</th>
        <th>Firmware</th>
        <th></th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="b in boards.boards" :key="b.id">
        <td>
          <div v-if="editingId === b.id" class="rename-row">
            <input
              v-model="draft.name"
              autofocus
              maxlength="80"
              :disabled="renameBusy[b.id]"
              @keyup.enter="confirmRename(b)"
              @keyup.escape="cancelRename"
            />
            <button :disabled="renameBusy[b.id]" @click="confirmRename(b)">Save</button>
            <button :disabled="renameBusy[b.id]" @click="cancelRename">Cancel</button>
          </div>
          <button v-else class="link-button" @click="startRename(b)">{{ b.name }}</button>
        </td>
        <td class="dim">{{ b.identity.kind }}: {{ b.identity.key }}</td>
        <td class="dim">{{ b.port ?? b.portHint ?? '—' }}</td>
        <td><StatusBadge :status="b.status" /></td>
        <td class="dim">
          {{ b.capabilities ? `${b.capabilities.digitalPins.length}D / ${b.capabilities.analogPins.length}A` : '—' }}
        </td>
        <td class="actions">
          <button v-if="editingId !== b.id" @click="startRename(b)">Rename</button>
        </td>
      </tr>
    </tbody>
  </table>
</template>

<style scoped>
.header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.actions {
  display: flex;
  gap: 6px;
  justify-content: flex-end;
}

.rename-row {
  display: flex;
  gap: 6px;
  align-items: center;
}

.link-button {
  background: none;
  border: none;
  padding: 0;
  font: inherit;
  color: inherit;
  cursor: pointer;
  text-align: left;
}

.link-button:hover {
  color: var(--color-accent);
}

.dim {
  color: var(--color-text-dim);
  font-size: 12px;
}
</style>
