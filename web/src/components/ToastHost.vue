<script setup lang="ts">
import { useToasts } from '../composables/toast'

const { toasts, dismissToast } = useToasts()
</script>

<template>
  <div class="toast-host">
    <div v-for="t in toasts" :key="t.id" class="toast" :class="t.severity">
      <div class="toast-body">
        <div class="toast-message">{{ t.message }}</div>
        <div v-if="t.detail" class="toast-detail">{{ t.detail }}</div>
      </div>
      <button class="toast-dismiss" @click="dismissToast(t.id)">×</button>
    </div>
  </div>
</template>

<style scoped>
.toast-host {
  position: fixed;
  bottom: 16px;
  right: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  z-index: 200;
  max-width: 360px;
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 12px;
  border-radius: var(--radius);
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  box-shadow: 0 4px 16px rgb(0 0 0 / 20%);
}

.toast.info {
  border-color: var(--color-info);
}

.toast.warning {
  border-color: var(--color-warning);
}

.toast.error {
  border-color: var(--color-danger);
}

.toast-body {
  flex: 1;
  min-width: 0;
}

.toast-message {
  font-size: 13px;
}

.toast-detail {
  font-size: 12px;
  color: var(--color-text-dim);
  margin-top: 2px;
}

.toast-dismiss {
  border: none;
  background: none;
  padding: 0 2px;
  line-height: 1;
  font-size: 16px;
  color: var(--color-text-dim);
}
</style>
