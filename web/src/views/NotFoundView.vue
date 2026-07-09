<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <div class="empty-state card">
    <h3>Page not found</h3>
    <RouterLink to="/">Back to dashboard</RouterLink>
  </div>
</template>
