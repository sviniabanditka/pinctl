<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router'
import { useWsStore } from './stores/ws'
import ToastHost from './components/ToastHost.vue'

const ws = useWsStore()
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">pinctl</div>
      <nav>
        <RouterLink to="/" exact-active-class="active">Dashboard</RouterLink>
        <RouterLink to="/devices" active-class="active">Devices</RouterLink>
        <RouterLink to="/pipelines" active-class="active">Pipelines</RouterLink>
      </nav>
      <div class="ws-indicator" :class="ws.connectionState">
        <span class="dot" />
        <span>{{ ws.connectionState }}</span>
      </div>
    </aside>
    <main class="content">
      <RouterView />
    </main>
  </div>
  <ToastHost />
</template>

<style scoped>
.shell {
  display: flex;
  min-height: 100vh;
}

.sidebar {
  width: 200px;
  flex-shrink: 0;
  background: var(--color-bg-alt);
  border-right: 1px solid var(--color-border);
  display: flex;
  flex-direction: column;
  padding: 16px 12px;
}

.brand {
  font-weight: 700;
  font-size: 16px;
  padding: 0 8px 16px;
}

nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

nav a {
  color: var(--color-text);
  text-decoration: none;
  padding: 8px 10px;
  border-radius: var(--radius);
}

nav a:hover {
  background: var(--color-surface);
}

nav a.active {
  background: var(--color-surface);
  color: var(--color-accent);
  font-weight: 600;
}

.ws-indicator {
  margin-top: auto;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 10px;
  font-size: 12px;
  color: var(--color-text-dim);
  text-transform: capitalize;
}

.ws-indicator .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-badge-stopped);
}

.ws-indicator.open .dot {
  background: var(--color-success);
}

.ws-indicator.connecting .dot,
.ws-indicator.reconnecting .dot {
  background: var(--color-warning);
}

.ws-indicator.closed .dot {
  background: var(--color-danger);
}

.content {
  flex: 1;
  min-width: 0;
  padding: 24px 32px;
}
</style>
