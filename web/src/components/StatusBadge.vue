<script setup lang="ts">
defineProps<{ status: string }>()

// docs/10-api.md status vocabularies map onto the shared signal palette:
// connected/running -> float signal, warning/paused -> warn signal,
// error/disconnected -> alert signal, stopped/idle -> faint ink.
const RUNNING = new Set(['running', 'connected', 'open'])
const WARN = new Set(['paused', 'reconnecting', 'connecting', 'warning'])
const ERROR = new Set(['error', 'disconnected', 'closed'])
</script>

<template>
  <span class="status-badge" :class="{ pulse: WARN.has(status) }">
    <span
      class="dot"
      :class="{
        running: RUNNING.has(status),
        warn: WARN.has(status),
        error: ERROR.has(status),
      }"
    />
    <span class="label status-label">{{ status }}</span>
  </span>
</template>

<style scoped>
.status-badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.status-badge.pulse .dot {
  animation: dot-pulse 1.6s ease-in-out infinite;
}

@media (prefers-reduced-motion: reduce) {
  .status-badge.pulse .dot {
    animation: none;
  }
}

.status-label {
  color: inherit;
}

.status-badge:has(.dot.running) .status-label {
  color: var(--status-running);
}

.status-badge:has(.dot.warn) .status-label {
  color: var(--status-warn);
}

.status-badge:has(.dot.error) .status-label {
  color: var(--status-error);
}
</style>
