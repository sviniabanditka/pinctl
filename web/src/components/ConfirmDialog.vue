<script setup lang="ts">
withDefaults(defineProps<{ title: string; message: string; confirmLabel?: string; busy?: boolean }>(), {
  confirmLabel: 'Delete',
  busy: false,
})
const emit = defineEmits<{ cancel: []; confirm: [] }>()
</script>

<template>
  <div class="dialog-backdrop" @click.self="emit('cancel')">
    <div class="dialog" role="alertdialog" aria-modal="true" :aria-label="title">
      <h2>{{ title }}</h2>
      <p>{{ message }}</p>
      <div class="dialog-actions">
        <button type="button" class="quiet" @click="emit('cancel')">Cancel</button>
        <button type="button" class="danger" :disabled="busy" @click="emit('confirm')">
          {{ busy ? 'Working…' : confirmLabel }}
        </button>
      </div>
    </div>
  </div>
</template>
