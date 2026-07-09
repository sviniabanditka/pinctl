import { defineStore } from 'pinia'
import { api } from '../api/client'
import type {
  Pipeline,
  PipelineActionResponse,
  PipelinesTopicData,
  PipelineSummary,
  PipelineWriteRequest,
} from '../api/types'
import { useWsStore } from './ws'

export const usePipelinesStore = defineStore('pipelines', {
  state: () => ({
    pipelines: [] as PipelineSummary[],
    loading: false,
    loaded: false,
  }),

  actions: {
    async fetch() {
      this.loading = true
      try {
        const res = await api.get<{ pipelines: PipelineSummary[] }>('/pipelines')
        this.pipelines = res.pipelines
        this.loaded = true
      } finally {
        this.loading = false
      }
    },

    /** Create with an empty graph — the editor (out of this scaffold's
     * scope) fills in nodes/edges and PUTs the full graph afterward. */
    async create(name: string, tickMs = 20): Promise<PipelineSummary> {
      const body: PipelineWriteRequest = { name, tickMs, nodes: [], edges: [] }
      // Response is the full saved pipeline (+warnings), not the list
      // summary shape — POST /pipelines always creates disabled, so status
      // is derived rather than read off the response (docs/10-api.md).
      const created = await api.post<Pipeline>('/pipelines', body)
      const summary: PipelineSummary = {
        id: created.id,
        name: created.name,
        enabled: created.enabled,
        tickMs: created.tickMs,
        nodeCount: created.nodes.length,
        status: created.enabled ? 'running' : 'stopped',
      }
      this.pipelines.push(summary)
      return summary
    },

    async remove(id: string): Promise<void> {
      await api.delete<void>(`/pipelines/${id}`)
      this.pipelines = this.pipelines.filter((p) => p.id !== id)
    },

    async enable(id: string): Promise<void> {
      const res = await api.post<PipelineActionResponse>(`/pipelines/${id}/enable`)
      this.applyAction(res)
    },

    async disable(id: string): Promise<void> {
      const res = await api.post<PipelineActionResponse>(`/pipelines/${id}/disable`)
      this.applyAction(res)
    },

    applyAction(res: PipelineActionResponse) {
      const p = this.pipelines.find((p) => p.id === res.id)
      if (p) {
        p.enabled = res.enabled
        p.status = res.status
      }
    },

    applyStatus(data: PipelinesTopicData) {
      const p = this.pipelines.find((p) => p.id === data.pipelineId)
      if (p) p.status = data.status
    },

    subscribeLive(): () => void {
      const ws = useWsStore()
      const handler = (data: unknown) => this.applyStatus(data as PipelinesTopicData)
      ws.subscribe('pipelines', handler)
      return () => ws.unsubscribe('pipelines', handler)
    },
  },
})
