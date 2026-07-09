<script setup lang="ts">
import { computed, markRaw, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { VueFlow, useVueFlow } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import '@vue-flow/core/dist/style.css'
import '@vue-flow/core/dist/theme-default.css'
import '@vue-flow/controls/dist/style.css'

import { useEditorStore } from '../stores/editor'
import { useDevicesStore } from '../stores/devices'
import { usePipelinesStore } from '../stores/pipelines'
import { useWsStore } from '../stores/ws'
import { api } from '../api/client'
import { pushErrorToast, pushToast } from '../composables/toast'
import { ApiError } from '../api/client'
import { checkConnection, nodePortType, portTypeContext } from '../lib/clientValidation'
import { NODE_SCHEMAS, PALETTE_GROUPS, PORT_COLORS, nodeInputs, nodeOutputs, resolvePortType, schemaFor, type NodeSchema } from '../lib/nodeSchemas'
import type { PipelineActionResponse, PipelinesTopicData, ValidationIssue } from '../api/types'
import PipelineNode from '../components/pipeline-editor/PipelineNode.vue'
import PipelineEdge from '../components/pipeline-editor/PipelineEdge.vue'
import CommandPalette, { type PaletteGroup } from '../components/CommandPalette.vue'
import ConfigPanel from '../components/pipeline-editor/ConfigPanel.vue'
import EditorToolbar from '../components/pipeline-editor/EditorToolbar.vue'
import LogPanel from '../components/pipeline-editor/LogPanel.vue'

const props = defineProps<{ id: string }>()

const editor = useEditorStore()
const devices = useDevicesStore()
const ws = useWsStore()

const nodeTypes = Object.fromEntries(Object.keys(NODE_SCHEMAS).map((t) => [t, markRaw(PipelineNode)]))
const edgeTypes = { pinctl: markRaw(PipelineEdge) }

const { project, screenToFlowCoordinate, vueFlowRef, viewport, onConnect } = useVueFlow()

const selectedNodeId = ref<string | null>(null)
const toggleBusy = ref(false)
const pipelineStatus = ref<PipelinesTopicData | null>(null)
const paletteOpen = ref(false)

// -- log drawer: hidden by default, state persisted across sessions --------
const LOG_OPEN_KEY = 'pinctl:editor-log-open'
const logOpen = ref(typeof window !== 'undefined' && window.localStorage.getItem(LOG_OPEN_KEY) === '1')
watch(logOpen, (open) => {
  if (typeof window !== 'undefined') window.localStorage.setItem(LOG_OPEN_KEY, open ? '1' : '0')
})

/** Count of log entries that arrived while the drawer was hidden; clears the moment it opens. */
const logHiddenCount = ref(0)
watch(
  () => editor.liveLog.length,
  (next, prev) => {
    if (logOpen.value) return
    logHiddenCount.value = next > prev ? logHiddenCount.value + (next - prev) : 0
  },
)
watch(logOpen, (open) => {
  if (open) logHiddenCount.value = 0
})

function toggleLog() {
  logOpen.value = !logOpen.value
}

function closeLog() {
  logOpen.value = false
}

// -- node config popover: anchored beside the selected node in screen space -
// Vue Flow keeps `viewport` (pan/zoom transform) reactive but doesn't expose
// per-node screen rects directly, so the node's actual rendered
// getBoundingClientRect() (via its `data-id` attribute) is used as the
// source of truth — it's exact and needs no manual zoom/pan arithmetic.
const NARROW_BREAKPOINT = 900
const POPOVER_WIDTH = 320
const POPOVER_GAP = 12

const popoverStyle = ref<Record<string, string>>({})
const popoverSide = ref<'left' | 'right' | 'sheet'>('right')
const isNarrow = ref(typeof window !== 'undefined' && window.innerWidth <= NARROW_BREAKPOINT)

let popoverRaf: number | null = null
function scheduleRepositionPopover() {
  if (popoverRaf !== null) return
  popoverRaf = requestAnimationFrame(() => {
    popoverRaf = null
    repositionPopover()
  })
}

function repositionPopover() {
  if (!selectedNodeId.value) {
    popoverStyle.value = {}
    return
  }
  if (isNarrow.value) {
    popoverSide.value = 'sheet'
    // Sit the config bottom-sheet above the log drawer (if open) rather than
    // covering it — both anchor to the viewport's bottom edge at this width.
    const bottom = logOpen.value ? 200 : 0
    popoverStyle.value = { left: '0px', right: '0px', bottom: `${bottom}px`, top: 'auto' }
    return
  }
  const container = vueFlowRef.value
  const nodeEl = container?.querySelector<HTMLElement>(`[data-id="${CSS.escape(selectedNodeId.value)}"]`)
  if (!container || !nodeEl) return
  const rect = nodeEl.getBoundingClientRect()
  const vw = window.innerWidth
  const vh = window.innerHeight
  let side: 'left' | 'right' = 'right'
  let left = rect.right + POPOVER_GAP
  if (left + POPOVER_WIDTH + 8 > vw) {
    side = 'left'
    left = rect.left - POPOVER_GAP - POPOVER_WIDTH
  }
  left = Math.max(8, Math.min(left, vw - POPOVER_WIDTH - 8))
  const top = Math.max(8, Math.min(rect.top, vh - 8 - 160))
  popoverSide.value = side
  popoverStyle.value = { left: `${left}px`, top: `${top}px` }
}

function closePopover() {
  const id = selectedNodeId.value
  selectedNodeId.value = null
  if (id) {
    nextTick(() => {
      vueFlowRef.value?.querySelector<HTMLElement>(`[data-id="${CSS.escape(id)}"]`)?.focus()
    })
  }
}

watch(selectedNodeId, () => nextTick(scheduleRepositionPopover))
watch(viewport, scheduleRepositionPopover, { deep: true })
watch(logOpen, () => nextTick(scheduleRepositionPopover))
watch(
  () => editor.nodes.find((n) => n.id === selectedNodeId.value)?.position,
  scheduleRepositionPopover,
  { deep: true },
)

function onWindowResize() {
  isNarrow.value = window.innerWidth <= NARROW_BREAKPOINT
  scheduleRepositionPopover()
}

/** `/` or `n` opens the add-node palette when the canvas has focus (not while typing); Esc dismisses palette/popover. */
function onWindowKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape') {
    if (paletteOpen.value) {
      paletteOpen.value = false
      return
    }
    if (selectedNodeId.value) closePopover()
    return
  }
  if (paletteOpen.value) return
  const target = ev.target as HTMLElement | null
  const typing =
    !!target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT' || target.isContentEditable)
  if (typing) return
  if (ev.key === '/' || ev.key.toLowerCase() === 'n') {
    ev.preventDefault()
    paletteOpen.value = true
  }
}

let unsubPipelines: (() => void) | null = null

async function loadAll() {
  editor.teardown()
  const tasks: Promise<unknown>[] = [editor.load(props.id)]
  if (!devices.loaded) tasks.push(devices.fetch())
  try {
    await Promise.all(tasks)
  } catch (err) {
    pushErrorToast(err)
    return
  }
  fillEdgeTypes()
  subscribeLiveIfEnabled()
}

/** Loaded edges carry no `type`/`data.valueType` yet — fill them in now that devices are loaded. */
function fillEdgeTypes() {
  const ctx = portTypeContext(devices.devices)
  for (const e of editor.edges) {
    e.type = 'pinctl'
    const sourceNode = editor.nodes.find((n) => n.id === e.source)
    e.data = { valueType: nodePortType(sourceNode, e.sourceHandle, 'out', ctx) }
  }
}

// editor.subscribeLive() tracks (and unsubscribes) its own previous
// subscription internally, so repeated calls here are safe without the view
// keeping its own unsub handle.
function subscribeLiveIfEnabled() {
  editor.subscribeLive()
}

onMounted(async () => {
  await loadAll()
  unsubPipelines = (() => {
    const handler = (data: unknown) => {
      const d = data as PipelinesTopicData
      if (d.pipelineId === props.id) pipelineStatus.value = d
    }
    ws.subscribe('pipelines', handler)
    return () => ws.unsubscribe('pipelines', handler)
  })()
  window.addEventListener('resize', onWindowResize)
  window.addEventListener('keydown', onWindowKeydown)
})

watch(
  () => props.id,
  () => loadAll(),
)

onBeforeUnmount(() => {
  unsubPipelines?.()
  editor.teardown()
  window.removeEventListener('resize', onWindowResize)
  window.removeEventListener('keydown', onWindowKeydown)
  if (popoverRaf !== null) cancelAnimationFrame(popoverRaf)
})

onBeforeRouteLeave(() => {
  if (editor.dirty) {
    const ok = window.confirm('Discard unsaved changes?')
    if (!ok) return false
  }
  return true
})

function beforeUnloadGuard(ev: BeforeUnloadEvent) {
  if (editor.dirty) {
    ev.preventDefault()
    ev.returnValue = ''
  }
}
window.addEventListener('beforeunload', beforeUnloadGuard)
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnloadGuard))

onConnect((connection) => {
  const result = checkConnection(
    {
      source: connection.source,
      sourceHandle: connection.sourceHandle ?? '',
      target: connection.target,
      targetHandle: connection.targetHandle ?? '',
    },
    editor.nodes,
    editor.edges,
    devices.devices,
  )
  if (!result.ok) {
    pushToast(result.reason ?? 'Invalid connection', 'warning')
    return
  }
  const ctx = portTypeContext(devices.devices)
  const sourceNode = editor.nodes.find((n) => n.id === connection.source)
  const valueType = nodePortType(sourceNode, connection.sourceHandle ?? '', 'out', ctx)
  editor.addEdge({
    id: `${connection.source}.${connection.sourceHandle}->${connection.target}.${connection.targetHandle}`,
    source: connection.source,
    sourceHandle: connection.sourceHandle ?? '',
    target: connection.target,
    targetHandle: connection.targetHandle ?? '',
    type: 'pinctl',
    data: { valueType },
  })
})

function onNodeClick({ node }: { node: { id: string } }) {
  selectedNodeId.value = node.id
}

function onPaneClick() {
  selectedNodeId.value = null
}

function onNodeDragStop({ node }: { node: { id: string; position: { x: number; y: number } } }) {
  editor.moveNode(node.id, node.position)
}

function onNodesChange(changes: { type: string; id?: string }[]) {
  for (const change of changes) {
    if (change.type === 'remove' && change.id) {
      if (selectedNodeId.value === change.id) selectedNodeId.value = null
      editor.removeNode(change.id)
    }
  }
}

function onEdgesChange(changes: { type: string; id?: string }[]) {
  for (const change of changes) {
    if (change.type === 'remove' && change.id) {
      editor.removeEdge(change.id)
    }
  }
}

function addNodeAt(type: string, position: { x: number; y: number }) {
  const node = editor.addNode(type, position)
  selectedNodeId.value = node.id
}

// -- add-node command palette: NODE_SCHEMAS/PALETTE_GROUPS wired into the
// generic CommandPalette's group/item shape. Small dots surface each port's
// value type at a glance (docs/11-web-ui.md palette section).
const noPortCtx = { deviceRole: () => undefined }
function portDots(schema: NodeSchema): { color: string; title: string }[] {
  const cfg = schema.defaultConfig()
  const ins = nodeInputs(schema, cfg).map((p) => resolvePortType(p, cfg, noPortCtx))
  const outs = nodeOutputs(schema, cfg).map((p) => resolvePortType(p, cfg, noPortCtx))
  return [...ins, ...outs].map((t) => ({ color: PORT_COLORS[t], title: t }))
}

const paletteGroups = computed<PaletteGroup[]>(() =>
  PALETTE_GROUPS.map((g) => ({
    label: g.label,
    items: Object.values(NODE_SCHEMAS)
      .filter((s) => s.group === g.key)
      .map((s) => ({ id: s.type, title: s.displayName, subtitle: s.description, dots: portDots(s) })),
  })).filter((g) => g.items.length > 0),
)

function onPaletteSelect(type: string) {
  paletteOpen.value = false
  addNodeCentered(type)
}

/** Inserts at the viewport centre, nudged so a repeat insert (or the palette itself) doesn't sit exactly on top of the last node. */
function addNodeCentered(type: string) {
  const bounds = vueFlowRef.value?.getBoundingClientRect()
  const center = bounds
    ? screenToFlowCoordinate({ x: bounds.left + bounds.width / 2, y: bounds.top + bounds.height / 2 })
    : { x: 280, y: 160 }
  const jitter = (editor.nodes.length % 8) * 24
  addNodeAt(type, { x: center.x - 80 + jitter, y: center.y - 40 + jitter })
}

function onSelectErrorNode(id: string) {
  selectedNodeId.value = id
}

function onDragOver(ev: DragEvent) {
  ev.preventDefault()
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move'
}

function onDrop(ev: DragEvent) {
  const type = ev.dataTransfer?.getData('application/pinctl-node-type')
  if (!type || !schemaFor(type)) return
  const bounds = vueFlowRef.value?.getBoundingClientRect()
  const point = project({
    x: ev.clientX - (bounds?.left ?? 0),
    y: ev.clientY - (bounds?.top ?? 0),
  })
  addNodeAt(type, point)
}

function deleteSelectedNode(id: string) {
  editor.removeNode(id)
  if (selectedNodeId.value === id) selectedNodeId.value = null
}

async function onSave() {
  try {
    const { warnings } = await editor.save()
    pushToast('Pipeline saved', 'info')
    for (const w of warnings) pushToast(w.message, 'warning')
  } catch (err) {
    if (err instanceof ApiError && err.code === 'validation_error') {
      const details = err.details as { errors?: ValidationIssue[]; warnings?: ValidationIssue[] } | undefined
      editor.applyServerErrors(details?.errors ?? [])
      // Pipeline-save 400s render inline, not as a toast (docs/11 "Error toasts").
    } else {
      pushErrorToast(err)
    }
  }
}

const pipelines = usePipelinesStore()

async function onToggleEnabled() {
  if (editor.dirty) return
  toggleBusy.value = true
  try {
    const action = editor.enabled ? 'disable' : 'enable'
    const res = await api.post<PipelineActionResponse>(`/pipelines/${props.id}/${action}`)
    editor.setEnabled(res.enabled)
    pipelines.applyAction(res)
    subscribeLiveIfEnabled()
  } catch (err) {
    pushErrorToast(err)
  } finally {
    toggleBusy.value = false
  }
}

const debugHintVisible = computed(() => !editor.enabled)
</script>

<template>
  <div class="editor-shell">
    <EditorToolbar
      :toggle-busy="toggleBusy"
      @save="onSave"
      @toggle-enabled="onToggleEnabled"
      @select-error-node="onSelectErrorNode"
    />

    <div class="editor-body">
      <div class="canvas-wrapper" @dragover="onDragOver" @drop="onDrop">
        <div v-if="debugHintVisible" class="debug-hint">Enable the pipeline to see live values.</div>
        <div v-else-if="pipelineStatus && pipelineStatus.status !== 'running'" class="debug-hint warn">
          {{ pipelineStatus.status }}<template v-if="pipelineStatus.detail"> — {{ pipelineStatus.detail }}</template>
        </div>
        <VueFlow
          v-model:nodes="editor.nodes"
          v-model:edges="editor.edges"
          :node-types="nodeTypes"
          :edge-types="edgeTypes"
          :delete-key-code="['Backspace', 'Delete']"
          fit-view-on-init
          @node-click="onNodeClick"
          @pane-click="onPaneClick"
          @node-drag-stop="onNodeDragStop"
          @nodes-change="onNodesChange"
          @edges-change="onEdgesChange"
        >
          <Background :gap="16" />
          <Controls />
        </VueFlow>

        <button
          type="button"
          class="log-fab"
          aria-controls="editor-log-panel"
          :aria-expanded="logOpen"
          title="Toggle log"
          @click="toggleLog"
        >
          <span class="mono label">LOG</span>
          <span v-if="!logOpen && logHiddenCount > 0" class="mono log-fab-badge">{{ logHiddenCount }}</span>
        </button>

        <button
          type="button"
          class="add-node-fab"
          aria-label="Add node"
          aria-haspopup="dialog"
          aria-keyshortcuts="/"
          :aria-expanded="paletteOpen"
          title="Add node (/)"
          @click="paletteOpen = true"
        >
          <span aria-hidden="true">+</span>
        </button>

        <CommandPalette
          :open="paletteOpen"
          :groups="paletteGroups"
          placeholder="Search node types…"
          empty-text="No node types match your search."
          @select="onPaletteSelect"
          @close="paletteOpen = false"
        />
      </div>
    </div>

    <ConfigPanel
      :selected-node-id="selectedNodeId"
      :anchor-style="popoverStyle"
      :side="popoverSide"
      @delete="deleteSelectedNode"
      @close="closePopover"
    />

    <LogPanel :open="logOpen" @close="closeLog" />
  </div>
</template>

<style scoped>
/* App.vue's <main class="content"> (out of scope here) wraps every view in
   var(--space-6)/var(--space-8) padding. The editor is the one view that
   needs to own the full viewport instead — so it escapes that padding with
   a matching negative margin and then sizes itself off the viewport
   directly (100dvh, not 100vh: dvh accounts for mobile browsers' dynamic
   toolbar instead of leaving dead space or overflow under it). Toolbar is
   a fixed-height row, canvas is flex: 1 and fills everything else, log
   drawer (LogPanel, fixed 200px) only takes space when open — no
   hard-coded subtraction that has to be kept in sync with any of their
   real heights. */
.editor-shell {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

.editor-body {
  flex: 1;
  display: flex;
  min-height: 0;
}

.canvas-wrapper {
  flex: 1;
  position: relative;
  min-width: 0;
  min-height: 0;
}

.add-node-fab {
  position: absolute;
  right: var(--space-6);
  bottom: var(--space-6);
  z-index: 6;
  width: 56px;
  height: 56px;
  padding: 0;
  border-radius: 50%;
  background: var(--raised);
  border: 1px solid var(--hairline);
  color: var(--ink);
  box-shadow: var(--shadow-popover);
  font-size: 1.5rem;
  line-height: 1;
  transition: transform 0.12s ease, border-color 0.12s ease, background-color 0.12s ease;
}

.add-node-fab:hover,
.add-node-fab:focus-visible {
  border-color: var(--hairline-strong);
  background: var(--surface);
  transform: translateY(-2px);
}

@media (prefers-reduced-motion: reduce) {
  .add-node-fab {
    transition: none;
  }
}

/* Floating log toggle: a sibling of the FAB, stacked directly above it on
   the same right edge with a spacing-scale gap — visually secondary
   (smaller, hairline border) rather than a second primary action. Both
   floating buttons live inside .canvas-wrapper (not the editor shell), so
   when the log drawer opens and the canvas shrinks to make room, they stay
   anchored to the canvas's own bottom-right corner, above the drawer. */
.log-fab {
  position: absolute;
  right: var(--space-6);
  bottom: calc(var(--space-6) + 56px + var(--space-3));
  z-index: 6;
  width: 44px;
  height: 44px;
  padding: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1px;
  border-radius: 50%;
  background: var(--raised);
  border: 1px solid var(--hairline);
  color: var(--ink-dim);
  box-shadow: var(--shadow-popover);
  transition: border-color 0.12s ease, color 0.12s ease;
}

.log-fab .label {
  font-size: 0.5625rem;
}

.log-fab[aria-expanded='true'],
.log-fab:hover,
.log-fab:focus-visible {
  border-color: var(--hairline-strong);
  color: var(--ink);
}

@media (prefers-reduced-motion: reduce) {
  .log-fab {
    transition: none;
  }
}

.log-fab-badge {
  position: absolute;
  top: -2px;
  right: -2px;
  min-width: 14px;
  padding: 0 3px;
  border-radius: 999px;
  background: var(--panel);
  border: 1px solid var(--hairline);
  color: var(--ink-dim);
  font-size: 0.5625rem;
  line-height: 14px;
  text-align: center;
}

.debug-hint {
  position: absolute;
  top: var(--space-2);
  left: 50%;
  transform: translateX(-50%);
  z-index: 5;
  background: var(--raised);
  border: 1px solid var(--hairline);
  color: var(--ink-dim);
  border-radius: var(--radius-sm);
  padding: 4px 10px;
  font-size: 0.75rem;
}

.debug-hint.warn {
  color: var(--signal-warn);
  border-color: color-mix(in srgb, var(--signal-warn) 40%, transparent);
}

/* @vue-flow/controls ships a light-theme panel (white buttons, light
   hairlines) via its own style.css — override to match the graphite
   chrome instead of leaving a bright white box floating over the canvas. */
:deep(.vue-flow__controls) {
  box-shadow: var(--shadow-popover);
  border-radius: var(--radius-sm);
  overflow: hidden;
}

:deep(.vue-flow__controls-button) {
  background: var(--raised);
  border-bottom: 1px solid var(--hairline);
  color: var(--ink-dim);
}

:deep(.vue-flow__controls-button svg) {
  fill: currentColor;
}

:deep(.vue-flow__controls-button:hover) {
  background: var(--surface);
  color: var(--ink);
}

:deep(.vue-flow__controls-button:disabled svg) {
  fill-opacity: 0.4;
}
</style>
