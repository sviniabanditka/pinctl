import { defineStore } from 'pinia'
import { api } from '../api/client'
import type { BoardEntry, BoardsTopicData } from '../api/types'
import { useWsStore } from './ws'

/**
 * v0.4 multi-board collection (docs/10-api.md "Boards": GET /boards, POST
 * /boards/rescan, PATCH /boards/{id}). Falls back to a single synthesized
 * entry server-side (internal/api/boards.go's boardsSnapshot) when the
 * server has no multi-board Manager wired in, so this store's surface
 * works unchanged in both single- and multi-board deployments.
 */
export const useBoardsStore = defineStore('boards', {
  state: () => ({
    boards: [] as BoardEntry[],
    loading: false,
    loaded: false,
    error: false,
    rescanning: false,
  }),

  actions: {
    async fetch() {
      this.loading = true
      this.error = false
      try {
        const res = await api.get<{ boards: BoardEntry[] }>('/boards')
        this.boards = res.boards
        this.loaded = true
      } catch {
        this.error = true
      } finally {
        this.loading = false
      }
    },

    /** POST /boards/rescan — fire-and-forget; results arrive via the `boards` WS topic or a follow-up fetch(). */
    async rescan() {
      this.rescanning = true
      try {
        await api.post<void>('/boards/rescan')
      } finally {
        this.rescanning = false
      }
    },

    async rename(id: string, name: string): Promise<BoardEntry> {
      const updated = await api.patch<BoardEntry>(`/boards/${id}`, { name })
      const idx = this.boards.findIndex((b) => b.id === id)
      if (idx !== -1) {
        // PATCH's response omits capabilities (docs/10-api.md's example),
        // so merge onto the existing entry rather than replacing it wholesale.
        this.boards[idx] = { ...this.boards[idx]!, ...updated }
      }
      return updated
    },

    applyStatus(data: BoardsTopicData) {
      const b = this.boards.find((b) => b.id === data.boardId)
      if (b) {
        b.status = data.status
        b.port = data.port
        return
      }
      // Unknown boardId — likely a newly discovered board (e.g. after
      // Rescan) that this store hasn't loaded yet. Pull the fresh list
      // rather than silently dropping the event. Single-flight: fetch()
      // sets this.loading synchronously before its first await, so a burst
      // of events for not-yet-known boards collapses into one request.
      if (!this.loading) {
        void this.fetch()
      }
    },

    /** Subscribe this caller's interest in live board status; refcounted via the ws store like devices/pipelines. */
    subscribeLive(): () => void {
      const ws = useWsStore()
      const handler = (data: unknown) => this.applyStatus(data as BoardsTopicData)
      ws.subscribe('boards', handler)
      return () => ws.unsubscribe('boards', handler)
    },
  },
})
