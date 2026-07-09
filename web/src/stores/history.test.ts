import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHistoryStore } from './history'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status })
}

const sampleResponse = {
  deviceId: 'dev_1',
  role: 'analog-in' as const,
  from: '2026-07-09T12:00:00Z',
  to: '2026-07-09T13:00:00Z',
  bucket: '15s',
  samples: [{ t: '2026-07-09T12:00:00Z', v: 512.4, min: 505, max: 519 }],
}

describe('history store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('fetch() populates samples/role/from/to/bucket from the response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(sampleResponse)))
    const store = useHistoryStore()

    await store.fetch('dev_1', '1h')

    expect(store.samples).toEqual(sampleResponse.samples)
    expect(store.role).toBe('analog-in')
    expect(store.bucket).toBe('15s')
    expect(store.loading).toBe(false)
    expect(store.disabled).toBe(false)
  })

  it('picks the bucket per the range preset table (1h -> 15s, 6h -> 1m, 24h -> 5m, 7d -> 30m)', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse(sampleResponse)))
    vi.stubGlobal('fetch', fetchMock)
    const store = useHistoryStore()

    const cases: [string, string][] = [
      ['1h', '15s'],
      ['6h', '1m'],
      ['24h', '5m'],
      ['7d', '30m'],
    ]
    for (const [range, bucket] of cases) {
      await store.fetch('dev_1', range)
      const [url] = fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string]
      expect(url).toContain(`bucket=${bucket}`)
    }
  })

  it('a 503 history_disabled response sets disabled, not error', async () => {
    const envelope = { error: { code: 'history_disabled', message: 'history is disabled on this instance' } }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(envelope, 503)))
    const store = useHistoryStore()

    await store.fetch('dev_1', '1h')

    expect(store.disabled).toBe(true)
    expect(store.error).toBeNull()
    expect(store.samples).toEqual([])
  })

  it('a generic error sets store.error and rethrows for the caller to toast', async () => {
    const envelope = { error: { code: 'internal_error', message: 'boom' } }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(envelope, 500)))
    const store = useHistoryStore()

    await expect(store.fetch('dev_1', '1h')).rejects.toMatchObject({ code: 'internal_error' })
    expect(store.error).not.toBeNull()
    expect(store.disabled).toBe(false)
  })

  it('aborts the in-flight request when fetch is called again before it resolves', async () => {
    let capturedSignal: AbortSignal | undefined
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      if (!capturedSignal) capturedSignal = init?.signal ?? undefined
      return new Promise(() => {
        // never resolves — simulates an in-flight request
      })
    })
    vi.stubGlobal('fetch', fetchMock)
    const store = useHistoryStore()

    const first = store.fetch('dev_1', '1h')
    expect(capturedSignal?.aborted).toBe(false)

    // Change range before the first request resolves.
    void store.fetch('dev_1', '6h')

    expect(capturedSignal?.aborted).toBe(true)
    // The never-resolving first fetch() call itself never settles here, so
    // don't await `first` — just confirm it hasn't rejected synchronously.
    void first
  })

  it('abort() cancels the in-flight request', async () => {
    let capturedSignal: AbortSignal | undefined
    const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) => {
      capturedSignal = init?.signal ?? undefined
      return new Promise(() => {})
    })
    vi.stubGlobal('fetch', fetchMock)
    const store = useHistoryStore()

    void store.fetch('dev_1', '1h')
    store.abort()

    expect(capturedSignal?.aborted).toBe(true)
  })
})
