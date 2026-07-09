import { defineStore } from 'pinia'
import { api } from '../api/client'
import type { BoardInfo } from '../api/types'

/**
 * v0.3 is still single-board (docs/12-roadmap.md), and the only board
 * endpoint implemented so far is the v0.1 `GET /api/v1/board` (singular) —
 * there is no `/boards` collection, rescan, or rename endpoint yet. This
 * store wraps that single-resource surface; docs/11-web-ui.md's `/boards`
 * screen (plural, multi-board) is a later-roadmap item.
 */
export const useBoardStore = defineStore('board', {
  state: () => ({
    board: null as BoardInfo | null,
    loading: false,
    loaded: false,
    error: false,
  }),

  actions: {
    async fetch() {
      this.loading = true
      this.error = false
      try {
        this.board = await api.get<BoardInfo>('/board')
        this.loaded = true
      } catch {
        this.error = true
      } finally {
        this.loading = false
      }
    },
  },
})
