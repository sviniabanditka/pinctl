<script setup lang="ts">
// Simulate mode's side dock (docs/18-simulation.md, POST /api/v1/simulate):
// seed inputs for every device the graph reads, run controls, the timeline
// scrubber, and a modest scripted-change list. Rendered as a permanent flex
// sibling of .canvas-wrapper (like LogPanel docks below it) rather than a
// node-anchored popover — it has no single node to anchor to.
//
// Node/edge values themselves are NOT rendered here: PipelineNode.vue and
// PipelineEdge.vue read the frame this panel selects through the editor
// store's displayActive/displayNodeValues/displayTick getters, so a
// simulated frame renders through exactly the same code a live tick does.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useDevicesStore } from '../../stores/devices'
import { useEditorStore } from '../../stores/editor'
import { SIM_MAX_STEPS } from '../../lib/simulate'

const editor = useEditorStore()
const devices = useDevicesStore()

const readDevices = computed(() =>
  editor.simReadDeviceIds.map((id) => devices.devices.find((d) => d.id === id)).filter((d): d is NonNullable<typeof d> => !!d),
)

function isBoolDevice(role: string | undefined): boolean {
  return role === 'digital-in'
}

function inputValue(deviceId: string): number {
  return editor.simInputs[deviceId] ?? 0
}

function isSeeded(deviceId: string): boolean {
  return Object.prototype.hasOwnProperty.call(editor.simInputs, deviceId)
}

function onSlider(deviceId: string, raw: string) {
  editor.setSimInput(deviceId, Number(raw))
}

function onToggle(deviceId: string, checked: boolean) {
  editor.setSimInput(deviceId, checked ? 1 : 0)
}

function onStepsInput(raw: string) {
  editor.setSimSteps(Number(raw))
}

async function run() {
  await editor.runSimulation()
}

// -- scrubber ---------------------------------------------------------------

function formatAt(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toISOString().replace('T', ' ').replace('Z', '')
}

function onScrub(raw: string) {
  editor.setSimFrameIndex(Number(raw))
}

/** `/`/text-field guard mirrors PipelineEditorView's own window keydown handler — the scrubber's keys must not fire while the user is typing into steps/script fields inside this same panel. */
function typingInField(ev: KeyboardEvent): boolean {
  const target = ev.target as HTMLElement | null
  return !!target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT' || target.isContentEditable)
}

function onWindowKeydown(ev: KeyboardEvent) {
  if (typingInField(ev)) return
  if (editor.simFrameCount === 0) return
  switch (ev.key) {
    case 'ArrowLeft':
      ev.preventDefault()
      editor.stepSimFrame(-1)
      break
    case 'ArrowRight':
      ev.preventDefault()
      editor.stepSimFrame(1)
      break
    case ' ':
      ev.preventDefault()
      editor.toggleSimPlay()
      break
    case 'Home':
      ev.preventDefault()
      editor.setSimFrameIndex(0)
      break
    case 'End':
      ev.preventDefault()
      editor.setSimFrameIndex(editor.simFrameCount - 1)
      break
  }
}

onMounted(() => window.addEventListener('keydown', onWindowKeydown))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onWindowKeydown)
  editor.stopSimPlayback()
})

// -- scripted changes ---------------------------------------------------

const scriptDeviceId = ref('')
const scriptTick = ref(1)
const scriptValue = ref(0)

watch(
  readDevices,
  (list) => {
    if (!scriptDeviceId.value && list.length > 0) scriptDeviceId.value = list[0].id
  },
  { immediate: true },
)

watch(
  () => editor.simCurrentFrame?.tick,
  (tick) => {
    if (tick !== undefined) scriptTick.value = tick
  },
)

const scriptDeviceRole = computed(() => devices.devices.find((d) => d.id === scriptDeviceId.value)?.role)

function addScriptEntry() {
  if (!scriptDeviceId.value) return
  editor.addSimScriptEntry({ tick: Math.max(1, Math.floor(scriptTick.value)), deviceId: scriptDeviceId.value, value: scriptValue.value })
}

function deviceName(id: string): string {
  return devices.devices.find((d) => d.id === id)?.name ?? id
}

function removeEntry(i: number) {
  editor.removeSimScriptEntry(i)
}

function onEntryTickChange(i: number, raw: string) {
  editor.updateSimScriptEntry(i, { tick: Math.max(1, Math.floor(Number(raw))) })
}

function onEntryValueChange(i: number, raw: string) {
  editor.updateSimScriptEntry(i, { value: Number(raw) })
}
</script>

<template>
  <aside class="sim-panel" aria-label="Simulate">
    <div class="sim-panel-header">
      <span class="label">Simulate</span>
    </div>

    <p class="sim-honesty">
      This proves the logic, not the wiring. It cannot know that pin 0 is the RX line, that WiFi jitters, or that a
      relay's inrush resets the board.
    </p>

    <div class="sim-section">
      <span class="label sim-section-title">Inputs</span>
      <p v-if="readDevices.length === 0" class="sim-empty">This graph doesn't read any devices.</p>
      <div v-for="d in readDevices" :key="d.id" class="sim-input-row">
        <div class="sim-input-row-head">
          <span class="sim-input-name" :title="d.id">{{ d.name }}</span>
          <span v-if="!isSeeded(d.id)" class="sim-unseeded-tag" title="Unseeded — will read as this type's zero and surface a warning">unseeded</span>
        </div>

        <label v-if="isBoolDevice(d.role)" class="field-inline sim-toggle">
          <input type="checkbox" :checked="!!inputValue(d.id)" @change="onToggle(d.id, ($event.target as HTMLInputElement).checked)" />
          <span class="mono num value-bool">{{ inputValue(d.id) ? 'true' : 'false' }}</span>
        </label>
        <div v-else class="sim-slider-row">
          <input
            type="range"
            min="0"
            max="1023"
            step="1"
            :value="inputValue(d.id)"
            @input="onSlider(d.id, ($event.target as HTMLInputElement).value)"
          />
          <span class="mono num value-float sim-slider-readout">{{ inputValue(d.id) }}</span>
        </div>
      </div>
    </div>

    <div v-if="editor.simUnseededWarnings.length > 0" class="issue-list warning">
      Unseeded: {{ editor.simUnseededWarnings.map((w) => w.deviceName).join(', ') }} — read as zero.
    </div>

    <div class="sim-section">
      <span class="label sim-section-title">Run</span>
      <div class="sim-run-row">
        <label class="field-inline sim-field-tight">
          <span class="label">Steps</span>
          <input type="number" class="mono" min="0" :max="SIM_MAX_STEPS" :value="editor.simSteps" @change="onStepsInput(($event.target as HTMLInputElement).value)" />
        </label>
        <label class="field-inline sim-field-tight">
          <span class="label">Tick</span>
          <span class="mono num">{{ editor.tickMs }}ms</span>
        </label>
      </div>
      <p class="field-hint">Capped at {{ SIM_MAX_STEPS }} steps.</p>
      <button class="primary sim-run-btn" :disabled="editor.simRunning || editor.hasClientErrors" @click="run">
        {{ editor.simRunning ? 'Running…' : 'Run' }}
      </button>
      <div v-if="editor.simRequestError" class="issue-list error">{{ editor.simRequestError }}</div>
    </div>

    <div v-if="editor.simResult?.error" class="issue-list error">
      Stopped at tick <span class="mono num">{{ editor.simResult.error.tick }}</span> ({{ editor.simResult.error.nodeId }}):
      {{ editor.simResult.error.message }}
    </div>

    <div v-if="editor.simFrameCount > 0" class="sim-section sim-scrubber-section">
      <span class="label sim-section-title">Timeline</span>
      <div class="sim-scrubber-readout">
        <span class="mono num">tick {{ editor.simCurrentFrame?.tick }}</span>
        <span class="mono sim-at">{{ editor.simCurrentFrame ? formatAt(editor.simCurrentFrame.at) : '' }}</span>
      </div>
      <input
        type="range"
        min="0"
        :max="Math.max(0, editor.simFrameCount - 1)"
        :value="editor.simFrameIndex"
        @input="onScrub(($event.target as HTMLInputElement).value)"
      />
      <div class="sim-scrubber-controls">
        <button class="quiet" title="Step back (←)" :disabled="editor.simFrameIndex === 0" @click="editor.stepSimFrame(-1)">◀</button>
        <button class="quiet" :title="editor.simPlaying ? 'Pause (Space)' : 'Play (Space)'" @click="editor.toggleSimPlay()">
          {{ editor.simPlaying ? '❚❚' : '▶' }}
        </button>
        <button
          class="quiet"
          title="Step forward (→)"
          :disabled="editor.simFrameIndex >= editor.simFrameCount - 1"
          @click="editor.stepSimFrame(1)"
        >
          ▶
        </button>
        <span class="mono num sim-frame-count">{{ editor.simFrameIndex + 1 }} / {{ editor.simFrameCount }}</span>
      </div>
      <p class="field-hint">←/→ step · Space play/pause · Home/End jump</p>
    </div>

    <div class="sim-section">
      <span class="label sim-section-title">Scripted changes</span>
      <ul v-if="editor.simScript.length > 0" class="sim-script-list">
        <li v-for="(e, i) in editor.simScript" :key="i" class="sim-script-row mono">
          <span class="sim-script-label">tick</span>
          <input type="number" class="mono sim-script-tick" min="1" :value="e.tick" @change="onEntryTickChange(i, ($event.target as HTMLInputElement).value)" />
          <span class="sim-script-arrow">→</span>
          <span class="sim-script-device" :title="e.deviceId">{{ deviceName(e.deviceId) }}</span>
          <span class="sim-script-eq">=</span>
          <input type="number" class="mono sim-script-value" :value="e.value" @change="onEntryValueChange(i, ($event.target as HTMLInputElement).value)" />
          <button type="button" class="danger-icon" title="Remove" @click="removeEntry(i)">×</button>
        </li>
      </ul>

      <div v-if="readDevices.length > 0" class="sim-script-add">
        <select v-model="scriptDeviceId" class="mono sim-script-add-device">
          <option v-for="d in readDevices" :key="d.id" :value="d.id">{{ d.name }}</option>
        </select>
        <span class="label">at</span>
        <input type="number" class="mono sim-script-add-tick" min="1" v-model.number="scriptTick" />
        <span class="label">=</span>
        <input
          v-if="isBoolDevice(scriptDeviceRole)"
          type="number"
          class="mono sim-script-add-value"
          min="0"
          max="1"
          v-model.number="scriptValue"
        />
        <input v-else type="number" class="mono sim-script-add-value" v-model.number="scriptValue" />
        <button type="button" class="quiet" @click="addScriptEntry">+ Add</button>
      </div>
    </div>
  </aside>
</template>

<style scoped>
.sim-panel {
  flex-shrink: 0;
  width: 320px;
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  overflow-y: auto;
  padding: var(--space-4);
  border-left: 1px solid var(--hairline);
  background: var(--surface);
}

.sim-panel-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.sim-honesty {
  color: var(--ink-dim);
  font-size: 0.75rem;
  line-height: 1.5;
}

.sim-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding-top: var(--space-3);
  border-top: 1px solid var(--hairline);
}

.sim-section-title {
  color: var(--ink-faint);
}

.sim-empty {
  color: var(--ink-faint);
  font-size: 0.8125rem;
}

.sim-input-row {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.sim-input-row-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.sim-input-name {
  font-size: 0.8125rem;
}

.sim-unseeded-tag {
  font-family: var(--font-label);
  font-size: 0.625rem;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--signal-warn);
}

.sim-toggle {
  gap: var(--space-2);
}

.sim-slider-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.sim-slider-row input[type='range'] {
  flex: 1;
}

.sim-slider-readout {
  min-width: 4ch;
  text-align: right;
}

.sim-run-row {
  display: flex;
  gap: var(--space-4);
}

.sim-field-tight {
  gap: var(--space-1);
}

.sim-field-tight input {
  width: 72px;
}

.sim-run-btn {
  align-self: flex-start;
}

.sim-scrubber-readout {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: var(--space-2);
}

.sim-at {
  color: var(--ink-dim);
  font-size: 0.75rem;
}

.sim-scrubber-controls {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.sim-frame-count {
  margin-left: auto;
  color: var(--ink-dim);
  font-size: 0.75rem;
}

.sim-script-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.sim-script-row {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: 0.75rem;
}

.sim-script-label {
  color: var(--ink-faint);
}

.sim-script-tick {
  width: 48px;
}

.sim-script-arrow {
  color: var(--ink-faint);
}

.sim-script-device {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sim-script-eq {
  color: var(--ink-faint);
}

.sim-script-value {
  width: 64px;
}

.sim-script-add {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-wrap: wrap;
}

.sim-script-add-device {
  flex: 1;
  min-width: 0;
}

.sim-script-add-tick,
.sim-script-add-value {
  width: 56px;
}

.issue-list {
  font-size: 0.75rem;
  border-radius: var(--radius-sm);
  padding: var(--space-2);
  border: 1px solid transparent;
}

.issue-list.error {
  border-color: color-mix(in srgb, var(--signal-alert) 40%, transparent);
  color: var(--signal-alert);
}

.issue-list.warning {
  border-color: color-mix(in srgb, var(--signal-warn) 40%, transparent);
  color: var(--signal-warn);
}
</style>
