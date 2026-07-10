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

  it('updateNodeConfig() prunes an edge whose target port was renamed away (expression inputs)', () => {
    const store = useEditorStore()
    store.applyLoaded({
      ...samplePipeline,
      nodes: [
        { id: 'src', type: 'constant', config: { kind: 'float', value: 1 }, position: { x: 0, y: 0 } },
        {
          id: 'n1',
          type: 'expression',
          config: { expression: 'a', inputs: [{ name: 'a', type: 'float64' }], outputType: 'bool' },
          position: { x: 100, y: 0 },
        },
      ],
      edges: [],
    })
    store.addEdge({ id: 'e1', source: 'src', sourceHandle: 'out', target: 'n1', targetHandle: 'a' })
    store.dirty = false

    // Renaming the input identifier a -> x means port "a" no longer
    // exists; the edge wired to it must be dropped rather than left
    // pointing at a vanished handle.
    store.updateNodeConfig('n1', { expression: 'x', inputs: [{ name: 'x', type: 'float64' }], outputType: 'bool' })

    expect(store.edges).toHaveLength(0)
    expect(store.dirty).toBe(true)
  })

  it('updateNodeConfig() prunes an edge whose target port was removed (expression inputs)', () => {
    const store = useEditorStore()
    store.applyLoaded({
      ...samplePipeline,
      nodes: [
        { id: 'src', type: 'constant', config: { kind: 'float', value: 1 }, position: { x: 0, y: 0 } },
        {
          id: 'n1',
          type: 'expression',
          config: {
            expression: 'a && b',
            inputs: [
              { name: 'a', type: 'bool' },
              { name: 'b', type: 'bool' },
            ],
            outputType: 'bool',
          },
          position: { x: 100, y: 0 },
        },
      ],
      edges: [],
    })
    store.addEdge({ id: 'e1', source: 'src', sourceHandle: 'out', target: 'n1', targetHandle: 'a' })
    store.addEdge({ id: 'e2', source: 'src', sourceHandle: 'out', target: 'n1', targetHandle: 'b' })
    store.dirty = false

    store.updateNodeConfig('n1', { expression: 'a', inputs: [{ name: 'a', type: 'bool' }], outputType: 'bool' })

    expect(store.edges.map((e) => e.id)).toEqual(['e1'])
    expect(store.dirty).toBe(true)
  })

  it('applyLoaded() seeds status/detail from the GET response, falling back to enabled when absent', () => {
    const store = useEditorStore()
    store.applyLoaded({ ...samplePipeline, enabled: true, status: 'paused', detail: 'device dev_1 is unreachable (board brd_1)' })
    expect(store.status).toBe('paused')
    expect(store.detail).toBe('device dev_1 is unreachable (board brd_1)')

    // PUT's response shape (Pipeline & warnings) never carries status/detail.
    store.applyLoaded({ ...samplePipeline, enabled: false })
    expect(store.status).toBe('stopped')
    expect(store.detail).toBe('')
  })

  it('applyStatus() updates status/detail for the current pipeline and ignores a stale one', () => {
    const store = useEditorStore()
    store.applyLoaded(samplePipeline)

    store.applyStatus({ pipelineId: 'pl_1', status: 'paused', detail: 'device dev_1 is unreachable (board brd_1)' })
    expect(store.status).toBe('paused')
    expect(store.detail).toBe('device dev_1 is unreachable (board brd_1)')

    // A status event for a different (previously open) pipeline must not
    // clobber the one currently loaded.
    store.applyStatus({ pipelineId: 'pl_2', status: 'error' })
    expect(store.status).toBe('paused')
  })

  it('setEnabled() applies the action response status and clears detail (enable/disable never carries one)', () => {
    const store = useEditorStore()
    store.applyLoaded(samplePipeline)
    store.detail = 'stale detail'

    store.setEnabled(true, 'running')

    expect(store.enabled).toBe(true)
    expect(store.status).toBe('running')
    expect(store.detail).toBe('')
  })

  it('enterSimulate() does not mutate the pipeline draft or trigger a save', () => {
    const store = useEditorStore()
    store.applyLoaded({
      ...samplePipeline,
      nodes: [{ id: 'n1', type: 'constant', config: { kind: 'float', value: 1 }, position: { x: 0, y: 0 } }],
      edges: [],
    })
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)

    const nodesBefore = store.nodes
    const edgesBefore = store.edges
    const nameBefore = store.name
    const tickMsBefore = store.tickMs

    store.enterSimulate()

    expect(store.simulating).toBe(true)
    expect(store.nodes).toBe(nodesBefore)
    expect(store.edges).toBe(edgesBefore)
    expect(store.name).toBe(nameBefore)
    expect(store.tickMs).toBe(tickMsBefore)
    expect(store.dirty).toBe(false)
    expect(fetchSpy).not.toHaveBeenCalled()

    store.exitSimulate()
    expect(store.simulating).toBe(false)
  })

  it('applyTick() timestamps only the nodes present in that tick snapshot', () => {
    const store = useEditorStore()
    store.applyLoaded(samplePipeline)

    store.applyTick({ pipelineId: 'pl_1', tick: 1, nodes: { a: 1, b: true } })

    expect(Object.keys(store.liveNodeUpdatedAt).sort()).toEqual(['a', 'b'])

    // A degraded tick that abandons evaluation before node "b" only
    // refreshes "a" — "b"'s stamp must stay at its earlier value, not jump
    // forward with the rest of the tick.
    const staleStamp = store.liveNodeUpdatedAt.b
    store.applyTick({ pipelineId: 'pl_1', tick: 2, nodes: { a: 2 } })
    expect(store.liveNodeUpdatedAt.b).toBe(staleStamp)
  })
})
