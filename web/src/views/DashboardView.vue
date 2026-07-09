<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { RouterLink } from 'vue-router'
import { useBoardStore } from '../stores/board'
import { useDevicesStore } from '../stores/devices'
import { usePipelinesStore } from '../stores/pipelines'
import { pushErrorToast } from '../composables/toast'
import StatusBadge from '../components/StatusBadge.vue'
import { isStaleValue } from '../lib/staleness'

const board = useBoardStore()
const devices = useDevicesStore()
const pipelines = usePipelinesStore()

let unsubDevices: (() => void) | null = null
let unsubPipelines: (() => void) | null = null

onMounted(async () => {
  unsubDevices = devices.subscribeLive()
  unsubPipelines = pipelines.subscribeLive()
  try {
    await Promise.all([board.fetch(), devices.loaded ? Promise.resolve() : devices.fetch(), pipelines.fetch()])
  } catch (err) {
    pushErrorToast(err)
  }
})

onUnmounted(() => {
  unsubDevices?.()
  unsubPipelines?.()
})

const initialLoading = computed(() => board.loading && !board.loaded && devices.loading && !devices.loaded)

const nothingConfigured = computed(
  () => board.loaded && !board.board?.connected && devices.devices.length === 0 && pipelines.pipelines.length === 0,
)
</script>

<template>
  <h1>Dashboard</h1>

  <div v-if="initialLoading" class="skeleton">Loading…</div>

  <div v-else-if="nothingConfigured" class="empty-state card">
    <h3>Nothing set up yet</h3>
    <p>Get started in three steps:</p>
    <ol style="text-align: left; display: inline-block">
      <li>Connect a board (see the runtime log for detection).</li>
      <li><RouterLink to="/devices">Add a device</RouterLink> bound to a pin.</li>
      <li><RouterLink to="/pipelines">Create a pipeline</RouterLink> wiring devices together.</li>
    </ol>
  </div>

  <div v-else class="panels">
    <section class="card">
      <h2>Board</h2>
      <div v-if="board.loading && !board.loaded">Loading…</div>
      <div v-else-if="!board.board" class="empty-state">No board information available.</div>
      <div v-else class="board-summary">
        <span class="badge" :class="board.board.connected ? 'running' : 'error'">
          {{ board.board.connected ? 'connected' : 'disconnected' }}
        </span>
        <span>{{ board.board.firmware.name }} v{{ board.board.firmware.major }}.{{ board.board.firmware.minor }}</span>
        <span class="dim">{{ board.board.port }}</span>
      </div>
    </section>

    <section class="card">
      <h2>Devices</h2>
      <div v-if="devices.devices.length === 0" class="empty-state">
        No devices yet. <RouterLink to="/devices">Add one</RouterLink>.
      </div>
      <ul v-else class="compact-list">
        <li v-for="d in devices.devices" :key="d.id">
          <RouterLink to="/devices">{{ d.name }}</RouterLink>
          <span class="dim">{{ d.role }}</span>
          <span class="value" :class="{ stale: isStaleValue(devices.liveValues[d.id]?.timestamp) }">
            {{ devices.liveValues[d.id] !== undefined ? devices.liveValues[d.id]!.value : '—' }}
          </span>
        </li>
      </ul>
    </section>

    <section class="card">
      <h2>Pipelines</h2>
      <div v-if="pipelines.pipelines.length === 0" class="empty-state">
        No pipelines yet. <RouterLink to="/pipelines">Create one</RouterLink>.
      </div>
      <ul v-else class="compact-list">
        <li v-for="p in pipelines.pipelines" :key="p.id">
          <RouterLink :to="`/pipelines/${p.id}`">{{ p.name }}</RouterLink>
          <span class="dim">{{ p.enabled ? 'enabled' : 'disabled' }}</span>
          <StatusBadge :status="p.status" />
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.panels {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 16px;
}

.board-summary {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.compact-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.compact-list li {
  display: flex;
  align-items: center;
  gap: 10px;
  justify-content: space-between;
}

.dim {
  color: var(--color-text-dim);
  font-size: 12px;
}

.value {
  font-variant-numeric: tabular-nums;
}
</style>
