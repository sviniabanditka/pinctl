import { setActivePinia, createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useEditorStore } from './editor'
import { useWsStore } from './ws'
import type { Pipeline } from '../api/types'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status })
}

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => {
    resolve = r
  })
  return { promise, resolve }
}

const samplePipeline: Pipeline = {
  id: 'pl_1',
  name: 'Pipeline 1',
  tickMs: 20,
  enabled: true,
  nodes: [],
  edges: [],
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
}

describe('editor store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('subscribeLive() records the unsub closure so teardown() releases it', () => {
    const store = useEditorStore()
    store.applyLoaded(samplePipeline)
    const ws = useWsStore()
    const subscribeSpy = vi.spyOn(ws, 'subscribe')
    const unsubscribeSpy = vi.spyOn(ws, 'unsubscribe')

    store.subscribeLive()

    expect(subscribeSpy).toHaveBeenCalledTimes(1)
    expect(store.liveUnsub).not.toBeNull()

    store.teardown()

    expect(unsubscribeSpy).toHaveBeenCalledTimes(1)
    expect(store.liveUnsub).toBeNull()
  })

  it('subscribeLive() called twice unsubscribes the previous subscription first', () => {
    const store = useEditorStore()
    store.applyLoaded(samplePipeline)
    const ws = useWsStore()
    const unsubscribeSpy = vi.spyOn(ws, 'unsubscribe')

    store.subscribeLive()
    expect(unsubscribeSpy).not.toHaveBeenCalled()

    store.subscribeLive()
    expect(unsubscribeSpy).toHaveBeenCalledTimes(1)

    store.teardown()
    expect(unsubscribeSpy).toHaveBeenCalledTimes(2)
  })

  it('teardown() is a no-op on liveUnsub when subscribeLive was never called', () => {
    const store = useEditorStore()
    store.applyLoaded(samplePipeline)
    expect(() => store.teardown()).not.toThrow()
  })

  it('validateNow() discards a response that arrives after pipelineId changed', async () => {
    const store = useEditorStore()
    store.applyLoaded(samplePipeline)

    const { promise, resolve } = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(promise))

    const validatePromise = store.validateNow()

    // Simulate navigating to a different pipeline while the request is in flight.
    store.pipelineId = 'pl_2'

    resolve(
      jsonResponse({
        valid: false,
        errors: [{ code: 'stale', message: 'should be discarded' }],
      }),
    )
    await validatePromise

    expect(store.lastValidateErrors).toEqual([])
    expect(store.serverErrors).toEqual({})
  })

  it('validateNow() applies a response that arrives for the still-current pipelineId', async () => {
    const store = useEditorStore()
    store.applyLoaded(samplePipeline)

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({
          valid: false,
          errors: [{ code: 'ok', message: 'applies' }],
        }),
      ),
    )

    await store.validateNow()

    expect(store.lastValidateErrors).toHaveLength(1)
  })

  it('updateNodeConfig() prunes edges wired to a port removed by the new config', () => {
    const store = useEditorStore()
    store.applyLoaded({
      ...samplePipeline,
      nodes: [
        { id: 'src', type: 'and', config: { inputCount: 2 }, position: { x: 0, y: 0 } },
        { id: 'n1', type: 'and', config: { inputCount: 4 }, position: { x: 100, y: 0 } },
      ],
      edges: [],
    })
    store.addEdge({ id: 'e1', source: 'src', sourceHandle: 'out', target: 'n1', targetHandle: 'in1' })
    store.addEdge({ id: 'e2', source: 'src', sourceHandle: 'out', target: 'n1', targetHandle: 'in3' })
    store.dirty = false

    // Shrinking n1's inputCount 4 -> 2 drops in3/in4; e2 (wired to in3)
    // should be pruned while e1 (wired to the still-present in1) survives.
    store.updateNodeConfig('n1', { inputCount: 2 })

    expect(store.edges.map((e) => e.id)).toEqual(['e1'])
    expect(store.dirty).toBe(true)
  })
})
