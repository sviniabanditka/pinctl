import { defineStore } from 'pinia'
import { api, ApiError, historyPath } from '../api/client'
import type { DeviceRole, HistorySample } from '../api/types'
import { rangePreset } from '../lib/chart'

export const useHistoryStore = defineStore('history', {
  state: () => ({
    samples: [] as HistorySample[],
    role: null as DeviceRole | null,
    from: '',
    to: '',
    bucket: undefined as string | undefined,
    loading: false,
    /** Set when the server answers 503 history_disabled — not an error
     * state, an honest "this instance keeps no history" (docs/16-history.md). */
    disabled: false,
    error: null as ApiError | null,
    controller: null as AbortController | null,
  }),

  actions: {
    /**
     * Fetches history for `deviceId` over the named range preset. Aborts
     * any request this store still has in flight first — called again on a
     * range change or when the sheet closes, per docs/16-history.md's query
     * API and the sheet's "abort an in-flight request" requirement.
     */
    async fetch(deviceId: string, rangeId: string): Promise<void> {
      this.abort()
      const preset = rangePreset(rangeId)
      const toDate = new Date()
      const fromDate = new Date(toDate.getTime() - preset.spanMs)
      const controller = new AbortController()
      this.controller = controller

      this.loading = true
      this.disabled = false
      this.error = null
      try {
        const res = await api.get<{
          deviceId: string
          role: DeviceRole
          from: string
          to: string
          bucket?: string
          samples: HistorySample[]
        }>(
          historyPath(deviceId, { from: fromDate.toISOString(), to: toDate.toISOString(), bucket: preset.bucket }),
          controller.signal,
        )
        this.samples = res.samples
        this.role = res.role
        this.from = res.from
        this.to = res.to
        this.bucket = res.bucket
      } catch (err) {
        if (err instanceof DOMException && err.name === 'AbortError') return
        if (err instanceof ApiError && err.code === 'history_disabled') {
          this.disabled = true
          this.samples = []
          return
        }
        this.error = err instanceof ApiError ? err : new ApiError(500, { code: 'internal_error', message: String(err) })
        throw err
      } finally {
        if (this.controller === controller) this.loading = false
      }
    },

    /** Aborts the in-flight request, if any. Safe to call unconditionally. */
    abort() {
      this.controller?.abort()
      this.controller = null
    },

    reset() {
      this.abort()
      this.samples = []
      this.role = null
      this.from = ''
      this.to = ''
      this.bucket = undefined
      this.loading = false
      this.disabled = false
      this.error = null
    },
  },
})
