import { setActivePinia, createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useDevicesStore } from './devices'
import type { Device } from '../api/types'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), { status })
}

const sampleDevice: Device = {
  id: 'dev_1',
  name: 'LDR',
  boardId: 'brd_local',
  pin: 0,
  role: 'analog-in',
  options: {},
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
}

describe('devices store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('fetch() populates devices from GET /devices', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ devices: [sampleDevice] })))
    const store = useDevicesStore()

    await store.fetch()

    expect(store.devices).toEqual([sampleDevice])
    expect(store.loaded).toBe(true)
  })

  it('create() posts the payload and appends the created device', async () => {
    const created = { ...sampleDevice, id: 'dev_2', name: 'Porch LED', role: 'digital-out' as const }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(created, 201))
    vi.stubGlobal('fetch', fetchMock)
    const store = useDevicesStore()

    const result = await store.create({
      name: 'Porch LED',
      boardId: '',
      pin: 13,
      role: 'digital-out',
      options: { initial: false, onDisconnect: 'safe-value', safeValue: false },
    })

    expect(result).toEqual(created)
    expect(store.devices).toContainEqual(created)
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/devices')
    expect(init.method).toBe('POST')
  })

  it('update() replaces the device in place', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ devices: [sampleDevice] })))
    const store = useDevicesStore()
    await store.fetch()

    const updated = { ...sampleDevice, name: 'Renamed LDR' }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(updated)))
    await store.update('dev_1', { name: 'Renamed LDR' })

    expect(store.devices[0]!.name).toBe('Renamed LDR')
  })

  it('remove() drops the device from local state on success', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ devices: [sampleDevice] })))
    const store = useDevicesStore()
    await store.fetch()

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(null, 204)))
    await store.remove('dev_1')

    expect(store.devices).toHaveLength(0)
  })

  it('remove() surfaces a 409 conflict without mutating local state', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ devices: [sampleDevice] })))
    const store = useDevicesStore()
    await store.fetch()

    const conflictBody = {
      error: { code: 'device_in_use', message: 'device is referenced by 1 pipeline', details: { pipelineIds: ['pl_1'] } },
    }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(conflictBody, 409)))

    await expect(store.remove('dev_1')).rejects.toMatchObject({ code: 'device_in_use', status: 409 })
    expect(store.devices).toHaveLength(1)
  })

  it('applyLiveValue() records the latest value keyed by deviceId', () => {
    const store = useDevicesStore()
    store.applyLiveValue({ deviceId: 'dev_1', value: 287, timestamp: '2026-01-01T00:00:00.100Z' })
    expect(store.liveValues['dev_1']).toEqual({ value: 287, timestamp: '2026-01-01T00:00:00.100Z' })
  })
})
