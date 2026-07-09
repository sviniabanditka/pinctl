<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <div class="empty-state card">
    <span class="dot error" />
    <h3>No signal here</h3>
    <p>Nothing's wired to this address. <RouterLink to="/">Back to the dashboard</RouterLink>.</p>
  </div>
</template>

<style scoped>
.empty-state {
  max-width: 420px;
  margin: var(--space-12) auto 0;
}

.dot {
  display: block;
  margin: 0 auto var(--space-2);
}
</style>
