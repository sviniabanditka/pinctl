<script setup lang="ts">
// Generic, reusable command palette: a backdrop + searchable, keyboard
// operable list of grouped items. No knowledge of nodes or any other
// domain — pipeline-editor wires its node catalog into `groups`, other
// callers can wire anything else (commands, boards, devices, …) the same
// way.
import { computed, nextTick, ref, watch } from 'vue'

export interface PaletteItem {
  id: string
  /** Rendered in mono. */
  title: string
  /** One-line description, rendered in --ink-dim sans text. */
  subtitle?: string
  /** Extra terms matched by the search filter but not rendered. */
  keywords?: string[]
  /** Small coloured dots, right-aligned (e.g. port/type indicators). */
  dots?: { color: string; title: string }[]
  disabled?: boolean
}

export interface PaletteGroup {
  label: string
  items: PaletteItem[]
}

const props = withDefaults(
  defineProps<{
    open: boolean
    groups: PaletteGroup[]
    placeholder?: string
    emptyText?: string
  }>(),
  {
    placeholder: 'Search…',
    emptyText: 'No results.',
  },
)

const emit = defineEmits<{ select: [id: string]; close: [] }>()

const query = ref('')
const highlightedId = ref<string | null>(null)
const searchInput = ref<HTMLInputElement | null>(null)
const rowEls = new Map<string, HTMLElement>()
let previouslyFocused: HTMLElement | null = null

function matches(item: PaletteItem, q: string): boolean {
  if (!q) return true
  const needle = q.toLowerCase()
  if (item.title.toLowerCase().includes(needle)) return true
  if (item.subtitle?.toLowerCase().includes(needle)) return true
  if (item.keywords?.some((k) => k.toLowerCase().includes(needle))) return true
  return false
}

const filteredGroups = computed(() =>
  props.groups
    .map((g) => ({ label: g.label, items: g.items.filter((item) => matches(item, query.value)) }))
    .filter((g) => g.items.length > 0),
)

const flatItems = computed(() => filteredGroups.value.flatMap((g) => g.items))
const enabledItems = computed(() => flatItems.value.filter((item) => !item.disabled))

function resetHighlight() {
  highlightedId.value = enabledItems.value[0]?.id ?? null
}

watch(query, resetHighlight)

watch(filteredGroups, () => {
  if (highlightedId.value && enabledItems.value.some((item) => item.id === highlightedId.value)) return
  resetHighlight()
})

watch(
  () => props.open,
  async (isOpen) => {
    if (isOpen) {
      previouslyFocused = document.activeElement as HTMLElement | null
      query.value = ''
      resetHighlight()
      await nextTick()
      searchInput.value?.focus()
    } else {
      previouslyFocused?.focus()
      previouslyFocused = null
    }
  },
)

function close() {
  emit('close')
}

function select(item: PaletteItem) {
  if (item.disabled) return
  emit('select', item.id)
}

function scrollHighlightIntoView() {
  nextTick(() => {
    if (!highlightedId.value) return
    rowEls.get(highlightedId.value)?.scrollIntoView({ block: 'nearest' })
  })
}

function moveHighlight(dir: 1 | -1) {
  const list = enabledItems.value
  if (list.length === 0) return
  const currentIdx = list.findIndex((item) => item.id === highlightedId.value)
  const nextIdx = currentIdx === -1 ? 0 : (currentIdx + dir + list.length) % list.length
  highlightedId.value = list[nextIdx].id
  scrollHighlightIntoView()
}

function setRowEl(id: string, el: Element | null) {
  if (el) rowEls.set(id, el as HTMLElement)
  else rowEls.delete(id)
}

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape') {
    ev.preventDefault()
    close()
    return
  }
  if (ev.key === 'ArrowDown') {
    ev.preventDefault()
    moveHighlight(1)
    return
  }
  if (ev.key === 'ArrowUp') {
    ev.preventDefault()
    moveHighlight(-1)
    return
  }
  if (ev.key === 'Enter') {
    ev.preventDefault()
    const item = flatItems.value.find((i) => i.id === highlightedId.value)
    if (item) select(item)
    return
  }
  if (ev.key === 'Tab') {
    // The search input is the only genuinely focusable control — rows are
    // navigated with the arrow keys and simulated via aria-activedescendant
    // — so trap focus on it rather than letting Tab escape the dialog.
    ev.preventDefault()
    searchInput.value?.focus()
  }
}

const activeDescendantId = computed(() => (highlightedId.value ? `palette-item-${highlightedId.value}` : undefined))
</script>

<template>
  <div v-if="open" class="palette-backdrop" @click="close">
    <div class="palette-panel popover" role="dialog" aria-modal="true" aria-label="Command palette" @click.stop @keydown="onKeydown">
      <input
        ref="searchInput"
        v-model="query"
        type="text"
        class="palette-search"
        :placeholder="placeholder"
        aria-label="Search"
        autocomplete="off"
      />

      <div class="palette-list" role="listbox" aria-label="Results" tabindex="-1" :aria-activedescendant="activeDescendantId">
        <div v-if="flatItems.length === 0" class="palette-empty">{{ emptyText }}</div>
        <template v-for="g in filteredGroups" :key="g.label">
          <div class="label palette-group-label">{{ g.label }}</div>
          <div
            v-for="item in g.items"
            :id="`palette-item-${item.id}`"
            :key="item.id"
            :ref="(el) => setRowEl(item.id, el as Element | null)"
            class="palette-row"
            :class="{ active: highlightedId === item.id, disabled: item.disabled }"
            role="option"
            :aria-selected="highlightedId === item.id"
            :aria-disabled="item.disabled || undefined"
            @click="select(item)"
            @mouseenter="!item.disabled && (highlightedId = item.id)"
          >
            <div class="palette-row-main">
              <div class="mono palette-row-title">{{ item.title }}</div>
              <div v-if="item.subtitle" class="palette-row-subtitle">{{ item.subtitle }}</div>
            </div>
            <slot name="item-extra" :item="item" />
            <div v-if="item.dots?.length" class="palette-row-dots">
              <span v-for="(d, i) in item.dots" :key="i" class="palette-dot" :style="{ background: d.color }" :title="d.title" />
            </div>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.palette-backdrop {
  position: fixed;
  inset: 0;
  background: rgb(0 0 0 / 40%);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding-top: 12vh;
  z-index: 50;
}

.palette-panel {
  width: min(420px, 92vw);
  max-height: 60vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.palette-search {
  flex-shrink: 0;
  border: none;
  border-bottom: 1px solid var(--hairline);
  border-radius: 0;
  padding: var(--space-3) var(--space-4);
  font-size: 0.9375rem;
  background: transparent;
}

.palette-search:hover {
  border-color: var(--hairline);
}

.palette-list {
  overflow-y: auto;
  padding: var(--space-2);
}

.palette-empty {
  padding: var(--space-4);
  color: var(--ink-dim);
  font-size: 0.8125rem;
  text-align: center;
}

.palette-group-label {
  padding: var(--space-2) var(--space-2) var(--space-1);
}

.palette-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-2);
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.palette-row.active,
.palette-row:hover {
  background: var(--surface);
}

.palette-row.disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.palette-row-main {
  min-width: 0;
}

.palette-row-title {
  font-size: 0.8125rem;
  color: var(--ink);
}

.palette-row-subtitle {
  font-size: 0.75rem;
  color: var(--ink-dim);
  margin-top: 1px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.palette-row-dots {
  flex-shrink: 0;
  display: flex;
  gap: 3px;
}

.palette-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--ink-faint);
}
</style>
