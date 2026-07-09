<script setup lang="ts">
// The shell: a page header (mark, tagline, WS status) + whatever the
// current route renders + the toast host. There is no nav rail any more —
// pinctl is down to two routes (Home, the pipeline editor), so a header
// link back to "/" is all the wayfinding the app needs (see router/index.ts).
import { computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { useWsStore } from './stores/ws'
import ToastHost from './components/ToastHost.vue'
import AppIcon from './components/icons/AppIcon.vue'

const ws = useWsStore()
const route = useRoute()

// The pipeline editor (meta.fullscreen, router/index.ts) needs the full
// viewport with no page scrollbar: .content-flush hands it the exact
// remainder below the header via flexbox instead of padding, and
// PipelineEditorView.vue's own `height: 100dvh` / negative-margin hack
// (out of scope to edit here) is neutralized from base.css instead — see
// the `.editor-shell` override there for why.
const fullscreen = computed(() => !!route.meta.fullscreen)

const WS_LABEL: Record<string, string> = {
  open: 'Connected',
  connecting: 'Connecting…',
  reconnecting: 'Reconnecting…',
  closed: 'Offline',
}
</script>

<template>
  <div class="shell">
    <header class="app-header" :class="{ thin: fullscreen }">
      <RouterLink to="/" class="brand">
        <AppIcon name="mark" />
        <span class="wordmark">pinctl</span>
      </RouterLink>
      <p v-if="!fullscreen" class="tagline">Pipeline I/O control for Arduino boards.</p>

      <div
        class="ws-indicator"
        :class="ws.connectionState"
        role="status"
        aria-live="polite"
      >
        <span
          class="dot"
          :class="{
            running: ws.connectionState === 'open',
            warn: ws.connectionState === 'connecting' || ws.connectionState === 'reconnecting',
            error: ws.connectionState === 'closed',
            'dot-pulse': ws.connectionState === 'reconnecting',
          }"
        />
        <span class="label ws-label">{{ WS_LABEL[ws.connectionState] ?? ws.connectionState }}</span>
      </div>
    </header>

    <main class="content" :class="{ 'content-flush': fullscreen }">
      <RouterView />
    </main>
  </div>
  <ToastHost />
</template>

<style scoped>
.shell {
  display: flex;
  flex-direction: column;
  height: 100vh;
}

.app-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-6);
  border-bottom: 1px solid var(--hairline);
  background: var(--surface);
}

.app-header.thin {
  padding: var(--space-2) var(--space-4);
}

.brand {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--ink);
  text-decoration: none;
  flex-shrink: 0;
}

.wordmark {
  font-family: var(--font-label);
  font-weight: 600;
  font-size: 0.9375rem;
  letter-spacing: 0.02em;
}

.tagline {
  color: var(--ink-dim);
  font-size: 0.8125rem;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ws-indicator {
  margin-left: auto;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.ws-label {
  white-space: nowrap;
}

.content {
  flex: 1;
  min-height: 0;
  min-width: 0;
  overflow-y: auto;
  padding: var(--space-6) var(--space-8);
}

/* The editor owns the entire remaining viewport itself — no padding for
   it to negative-margin its way out of, no scroll container above it. */
.content-flush {
  padding: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

@media (max-width: 560px) {
  .app-header {
    flex-wrap: wrap;
    padding: var(--space-2) var(--space-4);
  }

  .tagline {
    order: 3;
    flex-basis: 100%;
    white-space: normal;
  }

  .content:not(.content-flush) {
    padding: var(--space-4);
  }
}
</style>
