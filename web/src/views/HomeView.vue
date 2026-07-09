<script setup lang="ts">
// The single console: everything that used to be spread across Dashboard /
// Boards / Devices / Pipelines lives here as three tables (Boards, Devices,
// Pipelines) plus a global "+" command palette for the actions that used to
// be page-level buttons (Rescan, Add device, New pipeline).
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useBoardsStore } from '../stores/boards'
import { useDevicesStore } from '../stores/devices'
import { usePipelinesStore } from '../stores/pipelines'
import { useWsStore } from '../stores/ws'
import { pushErrorToast, pushToast } from '../composables/toast'
import { isStaleValue } from '../lib/staleness'
import Sparkline from '../components/Sparkline.vue'
import StatusBadge from '../components/StatusBadge.vue'
import CommandPalette, { type PaletteGroup } from '../components/CommandPalette.vue'
import DeviceFormDialog from '../components/DeviceFormDialog.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import type { BoardEntry, Device, DeviceOptions, DeviceRole, DevicesTopicData, PipelineSummary } from '../api/types'

const boards = useBoardsStore()
const devices = useDevicesStore()
const pipelines = usePipelinesStore()
const ws = useWsStore()
const router = useRouter()

// -- live trace buffers (devices section) -----------------------------------
const RING_SIZE = 60
/** Per-device rolling sample buffer, fed by the `devices` WS topic. The
 * Sparkline component itself holds no state — it just renders whatever
 * array it's handed — so the ring buffer lives here. */
const traces = reactive<Record<string, number[]>>({})

function pushSample(deviceId: string, value: boolean | number) {
  const n = typeof value === 'boolean' ? (value ? 1 : 0) : value
  const buf = traces[deviceId] ?? (traces[deviceId] = [])
  buf.push(n)
  if (buf.length > RING_SIZE) buf.splice(0, buf.length - RING_SIZE)
}

function onDeviceValue(data: unknown) {
  const d = data as DevicesTopicData
  pushSample(d.deviceId, d.value)
}

let unsubBoards: (() => void) | null = null
let unsubDevices: (() => void) | null = null
let unsubPipelines: (() => void) | null = null

onMounted(async () => {
  unsubBoards = boards.subscribeLive()
  const releaseValues = devices.subscribeLive()
  ws.subscribe('devices', onDeviceValue)
  unsubDevices = () => {
    releaseValues()
    ws.unsubscribe('devices', onDeviceValue)
  }
  unsubPipelines = pipelines.subscribeLive()

  const tasks: Promise<unknown>[] = []
  if (!boards.loaded) tasks.push(boards.fetch())
  if (!devices.loaded) tasks.push(devices.fetch())
  if (!pipelines.loaded) tasks.push(pipelines.fetch())
  try {
    await Promise.all(tasks)
  } catch (err) {
    pushErrorToast(err)
  }

  window.addEventListener('keydown', onWindowKeydown)
})

onUnmounted(() => {
  unsubBoards?.()
  unsubDevices?.()
  unsubPipelines?.()
  window.removeEventListener('keydown', onWindowKeydown)
})

const initialLoading = computed(() => boards.loading && !boards.loaded && devices.loading && !devices.loaded)

// ============================================================================
// Boards
// ============================================================================
const editingBoardId = ref<string | null>(null)
const boardRenameBusy = ref<Record<string, boolean>>({})
const boardDraft = reactive({ name: '' })

function startRename(b: BoardEntry) {
  editingBoardId.value = b.id
  boardDraft.name = b.name
}

function cancelRename() {
  editingBoardId.value = null
}

async function confirmRename(b: BoardEntry) {
  const name = boardDraft.name.trim()
  if (!name || name === b.name) {
    editingBoardId.value = null
    return
  }
  boardRenameBusy.value[b.id] = true
  try {
    await boards.rename(b.id, name)
    pushToast('Board renamed', 'info')
    editingBoardId.value = null
  } catch (err) {
    pushErrorToast(err)
  } finally {
    boardRenameBusy.value[b.id] = false
  }
}

async function onRescan() {
  pushToast('Scanning for boards…', 'info')
  try {
    await boards.rescan()
  } catch (err) {
    pushErrorToast(err)
  }
}

function boardCapabilities(b: BoardEntry): string {
  if (!b.capabilities) return '—'
  return `${b.capabilities.digitalPins.length} digital · ${b.capabilities.analogPins.length} analog · ${b.capabilities.pwmPins.length} pwm`
}

const hasConnectedBoard = computed(() => boards.boards.some((b) => b.status === 'connected'))

// ============================================================================
// Devices
// ============================================================================
const showDeviceForm = ref(false)
const editingDevice = ref<Device | null>(null)
const deletingDevice = ref<Device | null>(null)
const deviceFormBusy = ref(false)
const deviceDeleteBusy = ref(false)
const writeBusy = ref<Record<string, boolean>>({})

function openCreateDevice() {
  editingDevice.value = null
  showDeviceForm.value = true
}

function openEditDevice(d: Device) {
  editingDevice.value = d
  showDeviceForm.value = true
}

async function onDeviceFormSubmit(payload: {
  name: string
  boardId: string
  pin: number
  role: DeviceRole
  options: DeviceOptions
}) {
  deviceFormBusy.value = true
  try {
    if (editingDevice.value) {
      await devices.update(editingDevice.value.id, { name: payload.name, options: payload.options })
      pushToast('Device updated', 'info')
    } else {
      await devices.create({
        name: payload.name,
        boardId: payload.boardId,
        pin: payload.pin,
        role: payload.role,
        options: payload.options,
      })
      pushToast('Device created', 'info')
    }
    showDeviceForm.value = false
  } catch (err) {
    pushErrorToast(err)
  } finally {
    deviceFormBusy.value = false
  }
}

function askDeleteDevice(d: Device) {
  deletingDevice.value = d
}

async function confirmDeleteDevice() {
  if (!deletingDevice.value) return
  deviceDeleteBusy.value = true
  try {
    await devices.remove(deletingDevice.value.id)
    pushToast('Device deleted', 'info')
    deletingDevice.value = null
  } catch (err) {
    // device_in_use (409) — pushErrorToast already surfaces the blocking
    // pipeline id(s) via the error envelope's details (composables/toast.ts).
    pushErrorToast(err)
  } finally {
    deviceDeleteBusy.value = false
  }
}

function boardName(id: string): string {
  return boards.boards.find((b) => b.id === id)?.name ?? '—'
}

function isInput(d: Device): boolean {
  return d.role === 'digital-in' || d.role === 'analog-in'
}

function isBoolRole(d: Device): boolean {
  return d.role === 'digital-in' || d.role === 'digital-out'
}

function currentValue(d: Device): boolean | number | undefined {
  return devices.liveValues[d.id]?.value
}

function displayValue(d: Device): string {
  const v = currentValue(d)
  if (v === undefined) return '—'
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  return String(v)
}

async function toggleDigital(d: Device) {
  const current = devices.liveValues[d.id]?.value
  const next = !(typeof current === 'boolean' ? current : false)
  writeBusy.value[d.id] = true
  try {
    await devices.manualWrite(d.id, next)
  } catch (err) {
    // device_driven_by_pipeline (409 "conflict") — pushErrorToast already
    // resolves details.pipelineName into "Driven by pipeline: X".
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

function pwmValue(d: Device): number {
  const v = devices.liveValues[d.id]?.value
  return typeof v === 'number' ? v : 0
}

// ============================================================================
// Pipelines
// ============================================================================
const showCreatePipeline = ref(false)
const newPipelineName = ref('')
const newPipelineTickMs = ref(20)
const createPipelineBusy = ref(false)
const deletingPipeline = ref<PipelineSummary | null>(null)
const pipelineDeleteBusy = ref(false)
const togglingPipelineId = ref<string | null>(null)

function openPipeline(id: string) {
  router.push(`/pipelines/${id}`)
}

async function createPipeline() {
  createPipelineBusy.value = true
  try {
    const created = await pipelines.create(newPipelineName.value, newPipelineTickMs.value)
    pushToast('Pipeline created', 'info')
    showCreatePipeline.value = false
    newPipelineName.value = ''
    newPipelineTickMs.value = 20
    router.push(`/pipelines/${created.id}`)
  } catch (err) {
    pushErrorToast(err)
  } finally {
    createPipelineBusy.value = false
  }
}

async function togglePipelineEnabled(p: PipelineSummary) {
  togglingPipelineId.value = p.id
  try {
    if (p.enabled) {
      await pipelines.disable(p.id)
    } else {
      await pipelines.enable(p.id)
    }
  } catch (err) {
    pushErrorToast(err)
  } finally {
    togglingPipelineId.value = null
  }
}

function askDeletePipeline(p: PipelineSummary) {
  deletingPipeline.value = p
}

async function confirmDeletePipeline() {
  if (!deletingPipeline.value) return
  pipelineDeleteBusy.value = true
  try {
    await pipelines.remove(deletingPipeline.value.id)
    pushToast('Pipeline deleted', 'info')
    deletingPipeline.value = null
  } catch (err) {
    pushErrorToast(err)
  } finally {
    pipelineDeleteBusy.value = false
  }
}

// ============================================================================
// "+" command palette
// ============================================================================
const paletteOpen = ref(false)

const paletteGroups = computed<PaletteGroup[]>(() => [
  {
    label: 'Boards',
    items: [
      {
        id: 'rescan-boards',
        title: 'Rescan boards',
        subtitle: 'Scan connected ports for new boards.',
      },
    ],
  },
  {
    label: 'Devices',
    items: [
      {
        id: 'add-device',
        title: 'Add device',
        subtitle: hasConnectedBoard.value
          ? 'Bind a name and role to a board pin.'
          : 'No board is connected — connect one first.',
        disabled: !hasConnectedBoard.value,
      },
    ],
  },
  {
    label: 'Pipelines',
    items: [
      {
        id: 'create-pipeline',
        title: 'Create pipeline',
        subtitle: 'Wire devices together in the editor.',
      },
    ],
  },
])

function onPaletteSelect(id: string) {
  paletteOpen.value = false
  if (id === 'rescan-boards') {
    onRescan()
  } else if (id === 'add-device') {
    openCreateDevice()
  } else if (id === 'create-pipeline') {
    showCreatePipeline.value = true
  }
}

/** "/" or "n" opens the palette when nothing is being typed into. */
function onWindowKeydown(ev: KeyboardEvent) {
  if (paletteOpen.value) return
  const target = ev.target as HTMLElement | null
  const typing =
    !!target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT' || target.isContentEditable)
  if (typing) return
  if (ev.key === '/' || ev.key.toLowerCase() === 'n') {
    ev.preventDefault()
    paletteOpen.value = true
  }
}
</script>

<template>
  <div class="home-head">
    <span class="label">Console</span>
    <button
      type="button"
      class="primary add-btn"
      aria-haspopup="dialog"
      aria-keyshortcuts="/"
      :aria-expanded="paletteOpen"
      title="Add… (/ or n)"
      @click="paletteOpen = true"
    >
      <span aria-hidden="true">+</span> Add
    </button>
  </div>

  <div v-if="initialLoading" class="skeleton">Reading instrument state…</div>

  <template v-else>
    <!-- Boards -->
    <section class="panel">
      <div class="panel-head">
        <h2 class="label">Boards</h2>
        <button type="button" class="quiet" :disabled="boards.rescanning" @click="onRescan">
          {{ boards.rescanning ? 'Scanning…' : 'Rescan' }}
        </button>
      </div>

      <table v-if="boards.boards.length > 0" class="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Identity</th>
            <th>Port</th>
            <th>Firmware</th>
            <th>Capabilities</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="b in boards.boards" :key="b.id">
            <td data-label="Name">
              <div v-if="editingBoardId === b.id" class="rename-row">
                <input
                  v-model="boardDraft.name"
                  autofocus
                  maxlength="80"
                  :aria-label="`Rename ${b.name}`"
                  :disabled="boardRenameBusy[b.id]"
                  @keyup.enter="confirmRename(b)"
                  @keyup.escape="cancelRename"
                />
                <button class="quiet" :disabled="boardRenameBusy[b.id]" @click="confirmRename(b)">Save</button>
                <button class="quiet" :disabled="boardRenameBusy[b.id]" @click="cancelRename">Cancel</button>
              </div>
              <button v-else type="button" class="link-button name-cell" @click="startRename(b)">
                <span class="dot" :class="b.status" />
                <span class="sr-only">{{ b.status }} —</span>
                <span class="board-name">{{ b.name }}</span>
              </button>
            </td>
            <td class="cell-mono ink-faint" data-label="Identity">{{ b.identity.kind }}:{{ b.identity.key }}</td>
            <td class="cell-mono ink-dim" data-label="Port">{{ b.port ?? b.portHint ?? '—' }}</td>
            <td class="cell-mono ink-dim" data-label="Firmware">
              {{ b.status === 'connected' && b.firmware ? `${b.firmware.name} ${b.firmware.major}.${b.firmware.minor}` : '—' }}
            </td>
            <td class="cell-mono ink-dim" data-label="Capabilities">{{ boardCapabilities(b) }}</td>
          </tr>
        </tbody>
      </table>

      <table v-else class="data-table">
        <tbody>
          <tr class="table-empty">
            <td :colspan="5">
              No boards yet. Plug one in over USB — it needs to be listed under
              <code class="mono">serial.allowlist</code> in <code class="mono">config.yaml</code> before pinctl will see it.
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <!-- Devices -->
    <section class="panel">
      <div class="panel-head">
        <h2 class="label">Devices</h2>
        <button type="button" class="quiet" @click="openCreateDevice">Add device</button>
      </div>

      <table v-if="devices.devices.length > 0" class="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Role</th>
            <th>Board</th>
            <th>Pin</th>
            <th>Trace</th>
            <th>Value</th>
            <th>Control</th>
            <th><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in devices.devices" :key="d.id">
            <td data-label="Name">{{ d.name }}</td>
            <td data-label="Role"><span class="label">{{ d.role }}</span></td>
            <td class="cell-mono ink-dim" data-label="Board">{{ boardName(d.boardId) }}</td>
            <td class="cell-mono" data-label="Pin">{{ d.pin }}</td>
            <td data-label="Trace">
              <Sparkline
                v-if="isInput(d)"
                :values="traces[d.id] ?? []"
                :bool="isBoolRole(d)"
                :min="0"
                :max="isBoolRole(d) ? 1 : 1023"
                :color="isBoolRole(d) ? 'var(--signal-bool)' : 'var(--signal-float)'"
                :label="d.name"
              />
              <span v-else class="output-pill" :class="{ on: currentValue(d) === true || (typeof currentValue(d) === 'number' && (currentValue(d) as number) > 0) }">
                <span class="dot" :class="currentValue(d) ? 'connected' : 'idle'" />
                last written
              </span>
            </td>
            <td
              class="cell-num"
              data-label="Value"
              :class="[isBoolRole(d) ? 'value-bool' : 'value-float', { stale: isStaleValue(devices.liveValues[d.id]?.timestamp) }]"
            >
              {{ displayValue(d) }}
            </td>
            <td data-label="Control">
              <template v-if="d.role === 'digital-out'">
                <button
                  type="button"
                  class="toggle-switch"
                  role="switch"
                  :aria-checked="devices.liveValues[d.id]?.value === true"
                  :aria-label="`Toggle ${d.name}`"
                  :disabled="writeBusy[d.id]"
                  @click="toggleDigital(d)"
                >
                  <span class="toggle-track"><span class="toggle-thumb" /></span>
                </button>
              </template>
              <template v-else-if="d.role === 'pwm-out'">
                <div class="pwm-control">
                  <input
                    type="range"
                    min="0"
                    max="255"
                    :disabled="writeBusy[d.id]"
                    :value="pwmValue(d)"
                    :aria-label="`${d.name} PWM value`"
                    @change="writePwm(d, Number(($event.target as HTMLInputElement).value))"
                  />
                  <span class="cell-mono pwm-value">{{ pwmValue(d) }}</span>
                </div>
              </template>
              <span v-else class="ink-faint">—</span>
            </td>
            <td class="cell-actions" data-label="">
              <span class="row-actions">
                <button class="quiet" @click="openEditDevice(d)">Edit</button>
                <button class="danger-icon" @click="askDeleteDevice(d)">Delete</button>
              </span>
            </td>
          </tr>
        </tbody>
      </table>

      <table v-else class="data-table">
        <tbody>
          <tr class="table-empty">
            <td :colspan="8">Add a device to see its signal — bind a name and role to a board pin.</td>
          </tr>
        </tbody>
      </table>
    </section>

    <!-- Pipelines -->
    <section class="panel">
      <div class="panel-head">
        <h2 class="label">Pipelines</h2>
        <button type="button" class="quiet" @click="showCreatePipeline = true">New pipeline</button>
      </div>

      <table v-if="pipelines.pipelines.length > 0" class="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Status</th>
            <th>Enabled</th>
            <th>Tick</th>
            <th>Nodes</th>
            <th><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="p in pipelines.pipelines"
            :key="p.id"
            class="row-clickable"
            tabindex="0"
            @click="openPipeline(p.id)"
            @keydown.enter="openPipeline(p.id)"
          >
            <td data-label="Name">
              <span class="pipeline-name">{{ p.name }}</span>
            </td>
            <td data-label="Status"><StatusBadge :status="p.status" /></td>
            <td data-label="Enabled" @click.stop>
              <button
                type="button"
                class="toggle-switch"
                role="switch"
                :aria-checked="p.enabled"
                :aria-label="`${p.enabled ? 'Disable' : 'Enable'} ${p.name}`"
                :disabled="togglingPipelineId === p.id"
                @click="togglePipelineEnabled(p)"
              >
                <span class="toggle-track"><span class="toggle-thumb" /></span>
              </button>
            </td>
            <td class="cell-mono" data-label="Tick">{{ p.tickMs }} ms</td>
            <td class="cell-mono ink-dim" data-label="Nodes">{{ p.nodeCount }}</td>
            <td class="cell-actions" data-label="" @click.stop>
              <span class="row-actions">
                <button class="quiet" @click="openPipeline(p.id)">Open</button>
                <button class="danger-icon" @click="askDeletePipeline(p)">Delete</button>
              </span>
            </td>
          </tr>
        </tbody>
      </table>

      <table v-else class="data-table">
        <tbody>
          <tr class="table-empty">
            <td :colspan="6">No pipelines yet. Use the + button above to create one.</td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>

  <CommandPalette
    :open="paletteOpen"
    :groups="paletteGroups"
    placeholder="Rescan boards, add device, create pipeline…"
    empty-text="No matching actions."
    @select="onPaletteSelect"
    @close="paletteOpen = false"
  />

  <DeviceFormDialog
    v-if="showDeviceForm"
    :device="editingDevice"
    :busy="deviceFormBusy"
    :boards="boards.boards"
    @cancel="showDeviceForm = false"
    @submit="onDeviceFormSubmit"
  />

  <ConfirmDialog
    v-if="deletingDevice"
    title="Delete device"
    :message="`Delete '${deletingDevice.name}'? This can't be undone.`"
    :busy="deviceDeleteBusy"
    @cancel="deletingDevice = null"
    @confirm="confirmDeleteDevice"
  />

  <div v-if="showCreatePipeline" class="dialog-backdrop" @click.self="showCreatePipeline = false">
    <div class="dialog">
      <h2>New pipeline</h2>
      <form @submit.prevent="createPipeline">
        <div class="field">
          <label for="pl-name">Name</label>
          <input id="pl-name" v-model="newPipelineName" required maxlength="80" />
        </div>
        <div class="field">
          <label for="pl-tick">Tick rate (ms)</label>
          <input id="pl-tick" v-model.number="newPipelineTickMs" class="mono num" type="number" min="1" required />
        </div>
        <div class="dialog-actions">
          <button type="button" class="quiet" @click="showCreatePipeline = false">Cancel</button>
          <button type="submit" class="primary" :disabled="createPipelineBusy">
            {{ createPipelineBusy ? 'Creating…' : 'Create' }}
          </button>
        </div>
      </form>
    </div>
  </div>

  <ConfirmDialog
    v-if="deletingPipeline"
    title="Delete pipeline"
    :message="`Delete '${deletingPipeline.name}'? This can't be undone.`"
    :busy="pipelineDeleteBusy"
    @cancel="deletingPipeline = null"
    @confirm="confirmDeletePipeline"
  />
</template>

<style scoped>
.home-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-6);
}

.add-btn {
  font-size: 0.9375rem;
  padding: 8px 16px;
}

.panel {
  margin-bottom: var(--space-8);
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding-bottom: var(--space-2);
  border-bottom: 1px solid var(--hairline-strong);
  margin-bottom: var(--space-1);
}

.rename-row {
  display: flex;
  gap: var(--space-2);
  align-items: center;
}

.link-button {
  background: none;
  border: none;
  padding: 2px 0;
  font: inherit;
  color: var(--ink);
  font-weight: 500;
  cursor: pointer;
  text-align: left;
}

.link-button:hover {
  color: var(--signal-float);
}

.name-cell {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.board-name,
.pipeline-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pipeline-name {
  color: var(--ink);
  font-weight: 500;
}

.output-pill {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  font-family: var(--font-label);
  font-weight: 600;
  font-size: 0.6875rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--ink-faint);
}

.output-pill.on {
  color: var(--ink-dim);
}

/* Toggle switch — shared by the devices digital-out control and the
   pipelines enabled control; monochrome track, ink thumb, signal colour
   only when on. */
.toggle-switch {
  background: transparent;
  border: none;
  padding: 0;
}

.toggle-track {
  display: inline-flex;
  align-items: center;
  width: 36px;
  height: 20px;
  border-radius: 999px;
  background: var(--raised);
  border: 1px solid var(--hairline-strong);
  padding: 2px;
  transition: background-color 0.12s ease;
}

.toggle-switch[aria-checked='true'] .toggle-track {
  background: color-mix(in srgb, var(--signal-float) 25%, var(--raised));
  border-color: var(--signal-float);
}

.toggle-thumb {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--ink-faint);
  transition: transform 0.12s ease, background-color 0.12s ease;
}

.toggle-switch[aria-checked='true'] .toggle-thumb {
  background: var(--signal-float);
  transform: translateX(16px);
}

@media (prefers-reduced-motion: reduce) {
  .toggle-track,
  .toggle-thumb {
    transition: none;
  }
}

.pwm-control {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.pwm-control input[type='range'] {
  width: 120px;
  accent-color: var(--signal-float);
}

.pwm-value {
  color: var(--signal-float);
  min-width: 2.5em;
  text-align: right;
}

.row-clickable:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: -2px;
}
</style>
