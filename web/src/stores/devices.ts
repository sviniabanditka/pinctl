import { defineStore } from 'pinia'
import { api } from '../api/client'
import type { Device, DeviceOptions, DeviceRole, DevicesTopicData } from '../api/types'
import { useWsStore } from './ws'

export interface LiveValue {
  value: boolean | number
  timestamp: string
}

export interface DeviceCreateInput {
  name: string
  boardId: string
  pin: number
  role: DeviceRole
  options: DeviceOptions
}

export interface DeviceUpdateInput {
  name?: string
  options?: DeviceOptions
}

export const useDevicesStore = defineStore('devices', {
  state: () => ({
    devices: [] as Device[],
    liveValues: {} as Record<string, LiveValue>,
    loading: false,
    loaded: false,
  }),

  actions: {
    async fetch() {
      this.loading = true
      try {
        const res = await api.get<{ devices: Device[] }>('/devices')
        this.devices = res.devices
        this.loaded = true
      } finally {
        this.loading = false
      }
    },

    async create(input: DeviceCreateInput): Promise<Device> {
      const created = await api.post<Device>('/devices', input)
      this.devices.push(created)
      return created
    },

    async update(id: string, input: DeviceUpdateInput): Promise<Device> {
      const updated = await api.patch<Device>(`/devices/${id}`, input)
      const idx = this.devices.findIndex((d) => d.id === id)
      if (idx !== -1) this.devices[idx] = updated
      return updated
    },

    async remove(id: string): Promise<void> {
      await api.delete<void>(`/devices/${id}`)
      this.devices = this.devices.filter((d) => d.id !== id)
    },

    async manualWrite(id: string, value: boolean | number): Promise<void> {
      await api.post(`/devices/${id}/write`, { value })
      // Output devices never appear on the `devices` WS topic (the board
      // only reports inputs), so remember what we wrote — the toggle's
      // current-state source.
      this.liveValues[id] = { value, timestamp: new Date().toISOString() }
    },

    applyLiveValue(data: DevicesTopicData) {
      this.liveValues[data.deviceId] = { value: data.value, timestamp: data.timestamp }
    },

    /**
     * Subscribe this caller's interest in live device values. Each call
     * registers its own handler with the ws store, which refcounts the wire
     * `devices` subscription across every caller (e.g. Dashboard + Devices
     * both mounted) — call the returned function on unmount to release it.
     */
    subscribeLive(): () => void {
      const ws = useWsStore()
      const handler = (data: unknown) => this.applyLiveValue(data as DevicesTopicData)
      ws.subscribe('devices', handler)
      return () => ws.unsubscribe('devices', handler)
    },
  },
})
