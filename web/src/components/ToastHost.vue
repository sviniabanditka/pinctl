<script setup lang="ts">
import { useToasts } from '../composables/toast'

const { toasts, dismissToast } = useToasts()
</script>

<template>
  <div class="toast-host">
    <div v-for="t in toasts" :key="t.id" class="toast" :class="t.severity">
      <div class="toast-body">
        <div class="toast-message">{{ t.message }}</div>
        <div v-if="t.detail" class="toast-detail mono">{{ t.detail }}</div>
      </div>
      <button type="button" class="toast-dismiss quiet" aria-label="Dismiss" @click="dismissToast(t.id)">
        ×
      </button>
    </div>
  </div>
</template>

<style scoped>
.toast-host {
  position: fixed;
  bottom: var(--space-4);
  right: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  z-index: 200;
  max-width: 360px;
}

.toast {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--raised);
  border: 1px solid var(--hairline-strong);
  border-left: 2px solid var(--ink-faint);
  box-shadow: var(--shadow-popover);
}

.toast.info {
  border-left-color: var(--ink-dim);
}

.toast.warning {
  border-left-color: var(--signal-warn);
}

.toast.error {
  border-left-color: var(--signal-alert);
}

.toast-body {
  flex: 1;
  min-width: 0;
}

.toast-message {
  font-size: 0.8125rem;
  color: var(--ink);
}

.toast-detail {
  font-size: 0.8125rem;
  color: var(--ink-dim);
  margin-top: 2px;
}

.toast-dismiss {
  border: none;
  padding: 0 2px;
  line-height: 1;
  font-size: 1rem;
  color: var(--ink-faint);
}

.toast-dismiss:hover:not(:disabled) {
  color: var(--ink);
}
</style>
