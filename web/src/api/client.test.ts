import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from './client'

describe('api client', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('returns parsed JSON on a 200 response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 })),
    )
    const result = await api.get<{ ok: boolean }>('/devices')
    expect(result).toEqual({ ok: true })
  })

  it('returns undefined for a 204 response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
    const result = await api.delete<undefined>('/devices/dev_1')
    expect(result).toBeUndefined()
  })

  it('throws ApiError built from the error envelope on non-2xx', async () => {
    const envelope = { error: { code: 'conflict', message: 'device is referenced by 1 pipeline', details: { pipelineIds: ['pl_1'] } } }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(envelope), { status: 409 })))

    await expect(api.delete('/devices/dev_1')).rejects.toMatchObject({
      code: 'conflict',
      message: 'device is referenced by 1 pipeline',
      status: 409,
      details: { pipelineIds: ['pl_1'] },
    })
  })

  it('throws a generic ApiError when the body has no error envelope', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 500 })))
    await expect(api.get('/system')).rejects.toBeInstanceOf(ApiError)
  })

  it('sends a JSON body with Content-Type on POST', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 'x' }), { status: 201 }))
    vi.stubGlobal('fetch', fetchMock)

    await api.post('/devices', { name: 'LED' })

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('POST')
    expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
    expect(init.body).toBe(JSON.stringify({ name: 'LED' }))
  })
})
