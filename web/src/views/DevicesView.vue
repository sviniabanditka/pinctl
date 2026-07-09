<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useDevicesStore } from '../stores/devices'
import { pushErrorToast, pushToast } from '../composables/toast'
import type { Device, DeviceOptions, DeviceRole } from '../api/types'
import DeviceFormDialog from '../components/DeviceFormDialog.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import { isStaleValue } from '../lib/staleness'

const devices = useDevicesStore()

let unsubLive: (() => void) | null = null

onMounted(async () => {
  unsubLive = devices.subscribeLive()
  if (!devices.loaded) {
    try {
      await devices.fetch()
    } catch (err) {
      pushErrorToast(err)
    }
  }
})

onUnmounted(() => unsubLive?.())

const showForm = ref(false)
const editing = ref<Device | null>(null)
const deleting = ref<Device | null>(null)
const formBusy = ref(false)
const deleteBusy = ref(false)
const writeBusy = ref<Record<string, boolean>>({})

function openCreate() {
  editing.value = null
  showForm.value = true
}

function openEdit(d: Device) {
  editing.value = d
  showForm.value = true
}

async function onFormSubmit(payload: { name: string; pin: number; role: DeviceRole; options: DeviceOptions }) {
  formBusy.value = true
  try {
    if (editing.value) {
      await devices.update(editing.value.id, { name: payload.name, options: payload.options })
      pushToast('Device updated', 'info')
    } else {
      await devices.create({ name: payload.name, boardId: '', pin: payload.pin, role: payload.role, options: payload.options })
      pushToast('Device created', 'info')
    }
    showForm.value = false
  } catch (err) {
    pushErrorToast(err)
  } finally {
    formBusy.value = false
  }
}

function askDelete(d: Device) {
  deleting.value = d
}

async function confirmDelete() {
  if (!deleting.value) return
  deleteBusy.value = true
  try {
    await devices.remove(deleting.value.id)
    pushToast('Device deleted', 'info')
    deleting.value = null
  } catch (err) {
    pushErrorToast(err)
  } finally {
    deleteBusy.value = false
  }
}

function canWrite(d: Device): boolean {
  return d.role === 'digital-out' || d.role === 'pwm-out'
}

async function toggleDigital(d: Device) {
  const current = devices.liveValues[d.id]?.value
  const next = !(typeof current === 'boolean' ? current : false)
  writeBusy.value[d.id] = true
  try {
    await devices.manualWrite(d.id, next)
  } catch (err) {
    pushErrorToast(err)
  } finally {
    writeBusy.value[d.id] = false
  }
}

async function writePwm(d: Device, value: number) {
  writeBusy.value[d.id] = true
  try {
    await devices.manualWrite(d.id, value)
  } catch (err) {
    pushErrorToast(err)
  } finally {
    writeBusy.value[d.id] = false
  }
}
</script>

<template>
  <div class="header-row">
    <h1>Devices</h1>
    <button class="primary" @click="openCreate">Add device</button>
  </div>

  <div v-if="devices.loading && !devices.loaded" class="skeleton">Loading…</div>

  <div v-else-if="devices.devices.length === 0" class="empty-state card">
    <h3>No devices yet</h3>
    <p>Add a device to bind a name and role to a board pin.</p>
    <button class="primary" @click="openCreate">Add device</button>
  </div>

  <table v-else>
    <thead>
      <tr>
        <th>Name</th>
        <th>Pin</th>
        <th>Role</th>
        <th>Live value</th>
        <th></th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="d in devices.devices" :key="d.id">
        <td>{{ d.name }}</td>
        <td>{{ d.pin }}</td>
        <td>{{ d.role }}</td>
        <td>
          <span :class="{ stale: isStaleValue(devices.liveValues[d.id]?.timestamp) }">
            {{ devices.liveValues[d.id] !== undefined ? String(devices.liveValues[d.id]!.value) : '—' }}
          </span>
        </td>
        <td class="actions">
          <template v-if="canWrite(d)">
            <button v-if="d.role === 'digital-out'" :disabled="writeBusy[d.id]" @click="toggleDigital(d)">
              Toggle
            </button>
            <input
              v-else
              type="range"
              min="0"
              max="255"
              :disabled="writeBusy[d.id]"
              :value="typeof devices.liveValues[d.id]?.value === 'number' ? devices.liveValues[d.id]!.value : 0"
              @change="writePwm(d, Number(($event.target as HTMLInputElement).value))"
            />
          </template>
          <button @click="openEdit(d)">Edit</button>
          <button class="danger" @click="askDelete(d)">Delete</button>
        </td>
      </tr>
    </tbody>
  </table>

  <DeviceFormDialog
    v-if="showForm"
    :device="editing"
    :busy="formBusy"
    @cancel="showForm = false"
    @submit="onFormSubmit"
  />

  <ConfirmDialog
    v-if="deleting"
    title="Delete device"
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
  align-items: center;
  justify-content: flex-end;
}
</style>
