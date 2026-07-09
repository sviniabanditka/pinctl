<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { usePipelinesStore } from '../stores/pipelines'
import { pushErrorToast, pushToast } from '../composables/toast'
import StatusBadge from '../components/StatusBadge.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import type { PipelineSummary } from '../api/types'

const pipelines = usePipelinesStore()
const router = useRouter()

let unsubLive: (() => void) | null = null

onMounted(async () => {
  unsubLive = pipelines.subscribeLive()
  if (!pipelines.loaded) {
    try {
      await pipelines.fetch()
    } catch (err) {
      pushErrorToast(err)
    }
  }
})

onUnmounted(() => unsubLive?.())

const showCreate = ref(false)
const newName = ref('')
const newTickMs = ref(20)
const createBusy = ref(false)

const deleting = ref<PipelineSummary | null>(null)
const deleteBusy = ref(false)

const togglingId = ref<string | null>(null)

async function createPipeline() {
  createBusy.value = true
  try {
    const created = await pipelines.create(newName.value, newTickMs.value)
    pushToast('Pipeline created', 'info')
    showCreate.value = false
    newName.value = ''
    newTickMs.value = 20
    router.push(`/pipelines/${created.id}`)
  } catch (err) {
    pushErrorToast(err)
  } finally {
    createBusy.value = false
  }
}

async function toggleEnabled(p: PipelineSummary) {
  togglingId.value = p.id
  try {
    if (p.enabled) {
      await pipelines.disable(p.id)
    } else {
      await pipelines.enable(p.id)
    }
  } catch (err) {
    pushErrorToast(err)
  } finally {
    togglingId.value = null
  }
}

function askDelete(p: PipelineSummary) {
  deleting.value = p
}

async function confirmDelete() {
  if (!deleting.value) return
  deleteBusy.value = true
  try {
    await pipelines.remove(deleting.value.id)
    pushToast('Pipeline deleted', 'info')
    deleting.value = null
  } catch (err) {
    pushErrorToast(err)
  } finally {
    deleteBusy.value = false
  }
}

function openEditor(p: PipelineSummary) {
  router.push(`/pipelines/${p.id}`)
}
</script>

<template>
  <div class="header-row">
    <h1>Pipelines</h1>
    <button class="primary" @click="showCreate = true">New pipeline</button>
  </div>

  <div v-if="pipelines.loading && !pipelines.loaded" class="skeleton">Loading…</div>

  <div v-else-if="pipelines.pipelines.length === 0" class="empty-state card">
    <h3>No pipelines yet</h3>
    <p>Create one to start wiring devices together.</p>
    <button class="primary" @click="showCreate = true">New pipeline</button>
  </div>

  <table v-else>
    <thead>
      <tr>
        <th>Name</th>
        <th>Enabled</th>
        <th>Nodes</th>
        <th>Tick (ms)</th>
        <th>Status</th>
        <th></th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="p in pipelines.pipelines" :key="p.id">
        <td class="link-cell" @click="openEditor(p)">{{ p.name }}</td>
        <td>
          <button :disabled="togglingId === p.id" @click="toggleEnabled(p)">
            {{ togglingId === p.id ? '…' : p.enabled ? 'Disable' : 'Enable' }}
          </button>
        </td>
        <td>{{ p.nodeCount }}</td>
        <td>{{ p.tickMs }}</td>
        <td><StatusBadge :status="p.status" /></td>
        <td class="actions">
          <button @click="openEditor(p)">Open</button>
          <button class="danger" @click="askDelete(p)">Delete</button>
        </td>
      </tr>
    </tbody>
  </table>

  <div v-if="showCreate" class="dialog-backdrop" @click.self="showCreate = false">
    <div class="dialog">
      <h2>New pipeline</h2>
      <form @submit.prevent="createPipeline">
        <div class="field">
          <label for="pl-name">Name</label>
          <input id="pl-name" v-model="newName" required maxlength="80" />
        </div>
        <div class="field">
          <label for="pl-tick">Tick rate (ms)</label>
          <input id="pl-tick" v-model.number="newTickMs" type="number" min="1" required />
        </div>
        <div class="dialog-actions">
          <button type="button" @click="showCreate = false">Cancel</button>
          <button type="submit" class="primary" :disabled="createBusy">
            {{ createBusy ? 'Creating…' : 'Create' }}
          </button>
        </div>
      </form>
    </div>
  </div>

  <ConfirmDialog
    v-if="deleting"
    title="Delete pipeline"
    :message="`Delete '${deleting.name}'? This can't be undone.`"
    :busy="deleteBusy"
    @cancel="deleting = null"
    @confirm="confirmDelete"
  />
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

.link-cell {
  cursor: pointer;
  color: var(--color-accent);
}
</style>
