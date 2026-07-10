<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import type { BoardEntry, Device, DeviceOptions, DeviceRole } from '../api/types'

const props = withDefaults(
  defineProps<{ device?: Device | null; busy?: boolean; boards?: BoardEntry[] }>(),
  { busy: false, boards: () => [] },
)
const emit = defineEmits<{
  cancel: []
  submit: [payload: { name: string; boardId: string; pin: number; role: DeviceRole; options: DeviceOptions }]
}>()

const isEdit = computed(() => !!props.device)

// Multi-board mode: a real /boards collection exists, so the field is
// shown and required. Legacy single-board mode (boards list empty — no
// board.Manager wired in, docs/12-roadmap.md): the field is hidden
// entirely and boardId is omitted from the submit payload, letting the
// server's defaultBoardID fallback apply (internal/api/devices.go).
const showBoardField = computed(() => props.boards.length > 0)

const form = reactive({
  name: props.device?.name ?? '',
  boardId: props.device?.boardId ?? props.boards[0]?.id ?? '',
  pin: props.device?.pin ?? 0,
  role: (props.device?.role ?? 'digital-in') as DeviceRole,
  pullup: props.device?.options.pullup ?? false,
  initial: props.device?.options.initial ?? false,
  onDisconnect: props.device?.options.onDisconnect ?? 'hold',
  safeValue: props.device?.options.safeValue ?? false,
  minPulseUs: props.device?.options.minPulseUs ?? 544,
  maxPulseUs: props.device?.options.maxPulseUs ?? 2400,
})

const isOutput = computed(() => form.role === 'digital-out' || form.role === 'pwm-out' || form.role === 'servo')
const isServo = computed(() => form.role === 'servo')
// Numeric-valued output roles: pwm-out (0-255 duty cycle) and servo
// (0-180 degrees). Both need a number input rather than a checkbox for
// initial/safeValue, unlike digital-out's boolean.
const isNumeric = computed(() => form.role === 'pwm-out' || form.role === 'servo')

// `initial`/`safeValue` are a boolean for digital-out and a number for the
// numeric output roles (pwm-out, servo). Switching roles would otherwise
// hand a `false` to a number input (or a duty cycle/angle to a checkbox),
// so re-seed them to the new role's zero whenever the numeric-ness changes.
watch(isNumeric, (numeric) => {
  if (typeof form.initial !== (numeric ? 'number' : 'boolean')) form.initial = numeric ? 0 : false
  if (typeof form.safeValue !== (numeric ? 'number' : 'boolean')) form.safeValue = numeric ? 0 : false
})

function buildOptions(): DeviceOptions {
  if (form.role === 'digital-in') {
    return { pullup: form.pullup }
  }
  if (isOutput.value) {
    const opts: DeviceOptions = {
      initial: isNumeric.value ? Number(form.initial) : !!form.initial,
      onDisconnect: form.onDisconnect as 'hold' | 'safe-value',
    }
    if (form.onDisconnect === 'safe-value') {
      opts.safeValue = isNumeric.value ? Number(form.safeValue) : !!form.safeValue
    }
    if (isServo.value) {
      opts.minPulseUs = Number(form.minPulseUs)
      opts.maxPulseUs = Number(form.maxPulseUs)
    }
    return opts
  }
  return {}
}

function onSubmit() {
  emit('submit', {
    name: form.name,
    boardId: showBoardField.value ? form.boardId : '',
    pin: Number(form.pin),
    role: form.role,
    options: buildOptions(),
  })
}
</script>

<template>
  <div class="dialog-backdrop" @click.self="emit('cancel')">
    <div class="dialog">
      <h2>{{ isEdit ? 'Edit device' : 'Add device' }}</h2>
      <form @submit.prevent="onSubmit">
        <div class="field">
          <label for="dev-name">Name</label>
          <input id="dev-name" v-model="form.name" required maxlength="80" />
        </div>

        <div v-if="showBoardField" class="field">
          <label for="dev-board">Board</label>
          <select id="dev-board" v-model="form.boardId" required :disabled="isEdit">
            <option v-for="b in props.boards" :key="b.id" :value="b.id">{{ b.name }}</option>
          </select>
          <span v-if="isEdit" class="field-hint">Board can't be changed — delete and recreate to re-point it.</span>
        </div>

        <div class="field">
          <label for="dev-pin">Pin</label>
          <input
            id="dev-pin"
            v-model.number="form.pin"
            class="mono num"
            type="number"
            min="0"
            required
            :disabled="isEdit"
          />
          <span v-if="isEdit" class="field-hint">Pin can't be changed — delete and recreate to re-point it.</span>
        </div>

        <div class="field">
          <label for="dev-role">Role</label>
          <select id="dev-role" v-model="form.role" :disabled="isEdit">
            <option value="digital-in">digital-in</option>
            <option value="digital-out">digital-out</option>
            <option value="analog-in">analog-in</option>
            <option value="pwm-out">pwm-out</option>
            <option value="servo">servo</option>
          </select>
          <span v-if="!isEdit" class="field-hint">Role can't be changed after creation.</span>
        </div>

        <template v-if="form.role === 'digital-in'">
          <div class="field field-inline">
            <label class="field-inline"><input v-model="form.pullup" type="checkbox" /> Pull-up resistor</label>
          </div>
        </template>

        <template v-if="isServo">
          <div class="field">
            <label for="dev-min-pulse">Min pulse (µs)</label>
            <input
              id="dev-min-pulse"
              v-model.number="form.minPulseUs"
              class="mono num"
              type="number"
              min="400"
              max="3000"
            />
          </div>
          <div class="field">
            <label for="dev-max-pulse">Max pulse (µs)</label>
            <input
              id="dev-max-pulse"
              v-model.number="form.maxPulseUs"
              class="mono num"
              type="number"
              min="400"
              max="3000"
            />
            <span class="field-hint">Standard is 544–2400 µs; cheap servos often need trimming to reach 0°/180° cleanly.</span>
          </div>
        </template>

        <template v-if="isOutput">
          <div class="field">
            <label for="dev-initial">{{ isServo ? 'Initial angle (°)' : 'Initial value' }}</label>
            <input
              v-if="isNumeric"
              id="dev-initial"
              v-model.number="form.initial"
              class="mono num"
              type="number"
              min="0"
              :max="isServo ? 180 : 255"
            />
            <label v-else class="field-inline"><input v-model="form.initial" type="checkbox" /> On</label>
          </div>
          <div class="field">
            <label for="dev-ondisc">On disconnect</label>
            <select id="dev-ondisc" v-model="form.onDisconnect">
              <option value="hold">hold last value</option>
              <option value="safe-value">go to safe value</option>
            </select>
          </div>
          <div v-if="form.onDisconnect === 'safe-value'" class="field">
            <label for="dev-safe">{{ isServo ? 'Safe angle (°)' : 'Safe value' }}</label>
            <input
              v-if="isNumeric"
              id="dev-safe"
              v-model.number="form.safeValue"
              class="mono num"
              type="number"
              min="0"
              :max="isServo ? 180 : 255"
            />
            <label v-else class="field-inline"><input v-model="form.safeValue" type="checkbox" /> On</label>
          </div>
        </template>

        <div class="dialog-actions">
          <button type="button" class="quiet" @click="emit('cancel')">Cancel</button>
          <button type="submit" class="primary" :disabled="busy">
            {{ busy ? 'Saving…' : 'Save' }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<style scoped>
.field-inline {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  text-transform: none;
  letter-spacing: normal;
  font-family: var(--font-sans);
  font-weight: 400;
  font-size: 0.8125rem;
  color: var(--ink);
}
</style>
