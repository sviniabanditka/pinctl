// The pipeline currently open for editing (docs/11-web-ui.md "Pinia
// stores": editor store). Owns the working Vue Flow graph, dirty tracking,
// client + server validation results, and the live TickSnapshot while
// debugging. Torn down (pipeline:{id} subscription released) when leaving
// the editor route — see PipelineEditorView.

import { defineStore } from 'pinia'
import { api } from '../api/client'
import type { Pipeline, PipelineTickTopicData, PipelineWriteRequest, ValidationIssue } from '../api/types'
import {
  type FlowEdge,
  type FlowNode,
  flowEdgeToModelEdge,
  flowNodeToModelNode,
  mapValidationIssuesToNodes,
  modelEdgeToFlowEdge,
  modelNodeToFlowNode,
  nextNodeId,
} from '../lib/graphMapping'
import { NODE_SCHEMAS, type NodeSchema } from '../lib/nodeSchemas'
import { findCycleNodes, findUnconnectedRequiredInputs } from '../lib/clientValidation'
import { useWsStore } from './ws'

const VALIDATE_DEBOUNCE_MS = 400

export const useEditorStore = defineStore('editor', {
  state: () => ({
    pipelineId: '' as string,
    name: '',
    tickMs: 20,
    enabled: false,
    nodes: [] as FlowNode[],
    edges: [] as FlowEdge[],
    createdAt: '',
    updatedAt: '',

    loading: false,
    loaded: false,
    saving: false,
    dirty: false,

    serverErrors: {} as Record<string, ValidationIssue[]>,
    serverWarnings: [] as ValidationIssue[],

    liveTick: null as number | null,
    liveNodeValues: {} as Record<string, boolean | number>,

    validateTimer: null as ReturnType<typeof setTimeout> | null,
    validating: false,
    lastValidateErrors: [] as ValidationIssue[],

    liveUnsub: null as (() => void) | null,
  }),

  getters: {
    selectedNodeIds: () => [] as string[], // selection lives in the view (Vue Flow owns it); kept out of this store

    /** Client-side mirror: required-input-port-unconnected, recomputed on every edge/node change. */
    clientMissingInputs: (state): Record<string, string[]> =>
      findUnconnectedRequiredInputs(state.nodes, state.edges),

    /** Client-side mirror: cycle detection over the current graph. */
    clientCycleNodes: (state): Set<string> => findCycleNodes(state.nodes, state.edges),

    /** Save is disabled while any client-mirrored rule is violated (docs/11 "Save": "no point round-tripping..."). */
    hasClientErrors(): boolean {
      return this.clientCycleNodes.size > 0 || Object.keys(this.clientMissingInputs).length > 0
    },
  },

  actions: {
    async load(pipelineId: string) {
      this.loading = true
      try {
        const p = await api.get<Pipeline>(`/pipelines/${pipelineId}`)
        this.applyLoaded(p)
      } finally {
        this.loading = false
      }
    },

    applyLoaded(p: Pipeline) {
      this.pipelineId = p.id
      this.name = p.name
      this.tickMs = p.tickMs
      this.enabled = p.enabled
      this.nodes = p.nodes.map(modelNodeToFlowNode)
      this.edges = p.edges.map(modelEdgeToFlowEdge)
      this.createdAt = p.createdAt
      this.updatedAt = p.updatedAt
      this.loaded = true
      this.dirty = false
      this.serverErrors = {}
      this.serverWarnings = []
      this.liveTick = null
      this.liveNodeValues = {}
    },

    markDirty() {
      this.dirty = true
      this.scheduleValidate()
    },

    setName(name: string) {
      this.name = name
      this.markDirty()
    },

    setTickMs(tickMs: number) {
      this.tickMs = tickMs
      this.markDirty()
    },

    addNode(type: string, position: { x: number; y: number }): FlowNode {
      const schema: NodeSchema | undefined = NODE_SCHEMAS[type]
      const id = nextNodeId(this.nodes.map((n) => n.id))
      const node: FlowNode = {
        id,
        type,
        position,
        data: { nodeType: type, config: schema ? schema.defaultConfig() : {} },
      }
      this.nodes.push(node)
      this.markDirty()
      return node
    },

    removeNode(id: string) {
      this.nodes = this.nodes.filter((n) => n.id !== id)
      this.edges = this.edges.filter((e) => e.source !== id && e.target !== id)
      delete this.serverErrors[id]
      this.markDirty()
    },

    moveNode(id: string, position: { x: number; y: number }) {
      const node = this.nodes.find((n) => n.id === id)
      if (!node) return
      node.position = position
      this.markDirty()
    },

    updateNodeConfig(id: string, config: Record<string, unknown>) {
      const node = this.nodes.find((n) => n.id === id)
      if (!node) return
      node.data.config = config
      this.markDirty()
    },

    addEdge(edge: FlowEdge) {
      this.edges.push(edge)
      this.markDirty()
    },

    removeEdge(id: string) {
      this.edges = this.edges.filter((e) => e.id !== id)
      this.markDirty()
    },

    toWriteRequest(): PipelineWriteRequest {
      return {
        name: this.name,
        tickMs: this.tickMs,
        nodes: this.nodes.map(flowNodeToModelNode),
        edges: this.edges.map(flowEdgeToModelEdge),
      }
    },

    /** Debounced trigger for POST /pipelines/validate (docs/11: "~400ms after the last graph edit"). */
    scheduleValidate() {
      if (this.validateTimer) clearTimeout(this.validateTimer)
      this.validateTimer = setTimeout(() => this.validateNow(), VALIDATE_DEBOUNCE_MS)
    },

    async validateNow() {
      if (!this.pipelineId) return
      const requestedFor = this.pipelineId
      this.validating = true
      try {
        const res = await api.post<{ valid: boolean; errors?: ValidationIssue[]; warnings?: ValidationIssue[] }>(
          '/pipelines/validate',
          this.toWriteRequest(),
        )
        // The user may have navigated to a different pipeline (or teardown
        // reset the store) while this request was in flight — discard a
        // response that no longer matches what's open.
        if (this.pipelineId !== requestedFor) return
        this.lastValidateErrors = res.errors ?? []
        this.serverErrors = mapValidationIssuesToNodes(res.errors, this.nodes.map((n) => n.id))
        this.serverWarnings = res.warnings ?? []
      } catch {
        // validate is best-effort inline feedback; a transient failure here
        // shouldn't block editing — Save still runs the authoritative check.
      } finally {
        if (this.pipelineId === requestedFor) this.validating = false
      }
    },

    /** Returns saved pipeline + warnings on success; throws ApiError (with .details.errors) on 400. */
    async save(): Promise<{ warnings: ValidationIssue[] }> {
      this.saving = true
      try {
        const body = this.toWriteRequest()
        const saved = await api.put<Pipeline & { warnings?: ValidationIssue[] }>(
          `/pipelines/${this.pipelineId}`,
          body,
        )
        this.updatedAt = saved.updatedAt
        this.enabled = saved.enabled
        this.dirty = false
        this.serverErrors = {}
        this.serverWarnings = saved.warnings ?? []
        return { warnings: saved.warnings ?? [] }
      } finally {
        this.saving = false
      }
    },

    applyServerErrors(errors: ValidationIssue[]) {
      this.serverErrors = mapValidationIssuesToNodes(errors, this.nodes.map((n) => n.id))
    },

    setEnabled(enabled: boolean) {
      this.enabled = enabled
    },

    applyTick(data: PipelineTickTopicData) {
      if (data.pipelineId !== this.pipelineId) return
      this.liveTick = data.tick
      this.liveNodeValues = data.nodes
    },

    /**
     * Subscribes to pipeline:{id} while the pipeline is enabled; no-op (and
     * returns a no-op unsub) when disabled. Idempotent: unsubscribes any
     * previous live subscription first, so calling this repeatedly (e.g. on
     * every enable/reload) never leaks a handler. The unsub is also stored
     * on the store itself so teardown() can release it even if the caller
     * drops the returned closure.
     */
    subscribeLive(): () => void {
      this.liveUnsub?.()
      this.liveUnsub = null
      if (!this.enabled) return () => {}
      const ws = useWsStore()
      const topic = `pipeline:${this.pipelineId}`
      const handler = (data: unknown) => this.applyTick(data as PipelineTickTopicData)
      ws.subscribe(topic, handler)
      const unsub = () => {
        ws.unsubscribe(topic, handler)
        if (this.liveUnsub === unsub) this.liveUnsub = null
      }
      this.liveUnsub = unsub
      return unsub
    },

    teardown() {
      if (this.validateTimer) clearTimeout(this.validateTimer)
      this.validateTimer = null
      this.liveUnsub?.()
      this.liveUnsub = null
      this.$reset()
    },
  },
})
