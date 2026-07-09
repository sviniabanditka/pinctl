<script setup lang="ts">
import { computed, markRaw, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
import { NODE_SCHEMAS, schemaFor } from '../lib/nodeSchemas'
import type { PipelineActionResponse, PipelinesTopicData, ValidationIssue } from '../api/types'
import PipelineNode from '../components/pipeline-editor/PipelineNode.vue'
import PipelineEdge from '../components/pipeline-editor/PipelineEdge.vue'
import NodePalette from '../components/pipeline-editor/NodePalette.vue'
import ConfigPanel from '../components/pipeline-editor/ConfigPanel.vue'
import EditorToolbar from '../components/pipeline-editor/EditorToolbar.vue'

const props = defineProps<{ id: string }>()

const editor = useEditorStore()
const devices = useDevicesStore()
const ws = useWsStore()

const nodeTypes = Object.fromEntries(Object.keys(NODE_SCHEMAS).map((t) => [t, markRaw(PipelineNode)]))
const edgeTypes = { pinctl: markRaw(PipelineEdge) }

const { project, vueFlowRef, onConnect } = useVueFlow()

const selectedNodeId = ref<string | null>(null)
const toggleBusy = ref(false)
const pipelineStatus = ref<PipelinesTopicData | null>(null)

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
})

watch(
  () => props.id,
  () => loadAll(),
)

onBeforeUnmount(() => {
  unsubPipelines?.()
  editor.teardown()
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

function addNodeCentered(type: string) {
  addNodeAt(type, { x: 200 + editor.nodes.length * 30, y: 120 + editor.nodes.length * 20 })
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
    <div class="editor-body">
      <NodePalette @add="addNodeCentered" />

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
      </div>

      <ConfigPanel :selected-node-id="selectedNodeId" @delete="deleteSelectedNode" />
    </div>

    <EditorToolbar :toggle-busy="toggleBusy" @save="onSave" @toggle-enabled="onToggleEnabled" />
  </div>
</template>

<style scoped>
.editor-shell {
  display: flex;
  flex-direction: column;
  height: calc(100vh - 48px);
  margin: -24px -32px;
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
}

.debug-hint {
  position: absolute;
  top: 8px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 5;
  background: var(--color-info-bg);
  color: var(--color-info);
  border-radius: var(--radius);
  padding: 4px 10px;
  font-size: 12px;
}

.debug-hint.warn {
  background: var(--color-warning-bg);
  color: var(--color-warning);
}
</style>
