<script setup lang="ts">
import { computed, reactive } from 'vue'
import type { Device, DeviceOptions, DeviceRole } from '../api/types'

const props = withDefaults(defineProps<{ device?: Device | null; busy?: boolean }>(), { busy: false })
const emit = defineEmits<{
  cancel: []
  submit: [payload: { name: string; pin: number; role: DeviceRole; options: DeviceOptions }]
}>()

const isEdit = computed(() => !!props.device)

const form = reactive({
  name: props.device?.name ?? '',
  pin: props.device?.pin ?? 0,
  role: (props.device?.role ?? 'digital-in') as DeviceRole,
  pullup: props.device?.options.pullup ?? false,
  initial: props.device?.options.initial ?? false,
  onDisconnect: props.device?.options.onDisconnect ?? 'hold',
  safeValue: props.device?.options.safeValue ?? false,
})

const isOutput = computed(() => form.role === 'digital-out' || form.role === 'pwm-out')
const isPwm = computed(() => form.role === 'pwm-out')

function buildOptions(): DeviceOptions {
  if (form.role === 'digital-in') {
    return { pullup: form.pullup }
  }
  if (isOutput.value) {
    const opts: DeviceOptions = {
      initial: isPwm.value ? Number(form.initial) : !!form.initial,
      onDisconnect: form.onDisconnect as 'hold' | 'safe-value',
    }
    if (form.onDisconnect === 'safe-value') {
      opts.safeValue = isPwm.value ? Number(form.safeValue) : !!form.safeValue
    }
    return opts
  }
  return {}
}

function onSubmit() {
  emit('submit', { name: form.name, pin: Number(form.pin), role: form.role, options: buildOptions() })
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

        <div class="field">
          <label for="dev-pin">Pin</label>
          <input id="dev-pin" v-model.number="form.pin" type="number" min="0" required :disabled="isEdit" />
          <span v-if="isEdit" class="field-hint">Pin can't be changed — delete and recreate to re-point it.</span>
        </div>

        <div class="field">
          <label for="dev-role">Role</label>
          <select id="dev-role" v-model="form.role" :disabled="isEdit">
            <option value="digital-in">digital-in</option>
            <option value="digital-out">digital-out</option>
            <option value="analog-in">analog-in</option>
            <option value="pwm-out">pwm-out</option>
          </select>
          <span v-if="!isEdit" class="field-hint">Role can't be changed after creation.</span>
        </div>

        <template v-if="form.role === 'digital-in'">
          <div class="field field-inline">
            <label><input v-model="form.pullup" type="checkbox" /> Pull-up resistor</label>
          </div>
        </template>

        <template v-if="isOutput">
          <div class="field">
            <label for="dev-initial">Initial value</label>
            <input v-if="isPwm" id="dev-initial" v-model.number="form.initial" type="number" min="0" max="255" />
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
            <label for="dev-safe">Safe value</label>
            <input v-if="isPwm" id="dev-safe" v-model.number="form.safeValue" type="number" min="0" max="255" />
            <label v-else class="field-inline"><input v-model="form.safeValue" type="checkbox" /> On</label>
          </div>
        </template>

        <div class="dialog-actions">
          <button type="button" @click="emit('cancel')">Cancel</button>
          <button type="submit" class="primary" :disabled="busy">
            {{ busy ? 'Saving…' : 'Save' }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<style scoped>
.field-inline label {
  display: flex;
  align-items: center;
  gap: 6px;
}
</style>
