<script setup lang="ts">
// Hand-rolled 20x20 line icon — no icon dependency. stroke=currentColor so
// it inherits ink. The nav-rail icon set (dashboard/board/device/pipeline)
// and the rail's expand chevron were dropped along with the rail itself
// (see App.vue) — `mark` is the only icon left with a home, the app
// wordmark's glyph in the header.
defineProps<{
  name: 'mark'
}>()
</script>

<template>
  <svg
    v-if="name === 'mark'"
    viewBox="0 0 20 20"
    width="20"
    height="20"
    fill="none"
    stroke="currentColor"
    stroke-width="1.5"
    aria-hidden="true"
  >
    <rect x="3" y="3" width="14" height="14" rx="2" />
    <circle cx="7.5" cy="7.5" r="1.1" fill="currentColor" stroke="none" />
    <circle cx="12.5" cy="7.5" r="1.1" fill="currentColor" stroke="none" />
    <path d="M7.5 12.5h5" stroke-linecap="round" />
  </svg>
</template>
