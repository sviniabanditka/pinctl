import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { usePipelinesStore } from './pipelines'
import type { PipelineSummary } from '../api/types'

function summary(overrides: Partial<PipelineSummary> = {}): PipelineSummary {
  return {
    id: 'pl_1',
    name: 'Pipeline 1',
    enabled: true,
    tickMs: 20,
    nodeCount: 3,
    status: 'running',
    ...overrides,
  }
}

describe('pipelines store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('applyStatus() carries a paused status and its detail into the matching pipeline', () => {
    const store = usePipelinesStore()
    store.pipelines = [summary()]

    store.applyStatus({
      pipelineId: 'pl_1',
      status: 'paused',
      detail: 'device dev_9zq3v7ab4c is unreachable (board brd_4kx8n2wq1r)',
    })

    const p = store.pipelines[0]!
    expect(p.status).toBe('paused')
    expect(p.detail).toBe('device dev_9zq3v7ab4c is unreachable (board brd_4kx8n2wq1r)')
    // Still enabled — paused is a live/degraded state, not disabled.
    expect(p.enabled).toBe(true)
  })

  it('applyStatus() clears detail when a later event reports a clean status', () => {
    const store = usePipelinesStore()
    store.pipelines = [summary({ status: 'paused', detail: 'device dev_1 is unreachable (board brd_1)' })]

    store.applyStatus({ pipelineId: 'pl_1', status: 'running' })

    const p = store.pipelines[0]!
    expect(p.status).toBe('running')
    expect(p.detail).toBe('')
  })

  it('applyStatus() ignores an event for a pipeline not in the list', () => {
    const store = usePipelinesStore()
    store.pipelines = [summary()]

    store.applyStatus({ pipelineId: 'pl_other', status: 'error' })

    expect(store.pipelines[0]!.status).toBe('running')
  })

  it('applyAction() sets the toggled enabled state without touching a still-relevant paused status', () => {
    // enable/disable's response has no detail; applyAction clears it and
    // relies on the "pipelines" WS broadcast that follows to repopulate it
    // for a pipeline that's paused right after being (re-)enabled.
    const store = usePipelinesStore()
    store.pipelines = [summary({ enabled: false, status: 'stopped' })]

    store.applyAction({ id: 'pl_1', enabled: true, status: 'paused' })

    const p = store.pipelines[0]!
    expect(p.enabled).toBe(true)
    expect(p.status).toBe('paused')
    expect(p.detail).toBe('')
  })
})
