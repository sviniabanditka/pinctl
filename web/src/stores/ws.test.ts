import { setActivePinia, createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWsStore } from './ws'

/** Minimal fake WebSocket capturing sent frames and letting tests drive
 * open/message/close events, so the ws store's subscribe logic and
 * reconnect handling can be exercised without a real socket/server. */
class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static OPEN = 1
  static CONNECTING = 0
  static CLOSED = 3

  readyState = FakeWebSocket.CONNECTING
  sent: string[] = []
  listeners: Record<string, ((ev?: unknown) => void)[]> = {}

  url: string

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  addEventListener(type: string, cb: (ev?: unknown) => void) {
    ;(this.listeners[type] ??= []).push(cb)
  }

  send(data: string) {
    this.sent.push(data)
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED
    this.emit('close')
  }

  emit(type: string, ev?: unknown) {
    for (const cb of this.listeners[type] ?? []) cb(ev)
  }

  open() {
    this.readyState = FakeWebSocket.OPEN
    this.emit('open')
  }

  receive(msg: unknown) {
    this.emit('message', { data: JSON.stringify(msg) })
  }
}

describe('ws store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket)
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  function latestSocket(): FakeWebSocket {
    return FakeWebSocket.instances[FakeWebSocket.instances.length - 1]!
  }

  it('only sends a wire subscribe on the first subscriber for a topic', () => {
    const ws = useWsStore()
    ws.connect()
    latestSocket().open()

    const h1 = vi.fn()
    const h2 = vi.fn()
    ws.subscribe('devices', h1)
    ws.subscribe('devices', h2)

    const subscribeFrames = latestSocket().sent.filter((s) => JSON.parse(s).type === 'subscribe')
    expect(subscribeFrames).toHaveLength(1)
  })

  it('only sends a wire unsubscribe once the last subscriber releases the topic', () => {
    const ws = useWsStore()
    ws.connect()
    latestSocket().open()

    const h1 = vi.fn()
    const h2 = vi.fn()
    ws.subscribe('devices', h1)
    ws.subscribe('devices', h2)
    latestSocket().sent = []

    ws.unsubscribe('devices', h1)
    expect(latestSocket().sent.filter((s) => JSON.parse(s).type === 'unsubscribe')).toHaveLength(0)

    ws.unsubscribe('devices', h2)
    expect(latestSocket().sent.filter((s) => JSON.parse(s).type === 'unsubscribe')).toHaveLength(1)
  })

  it('dispatches event frames to handlers registered for the matching topic', () => {
    const ws = useWsStore()
    ws.connect()
    latestSocket().open()

    const deviceHandler = vi.fn()
    const pipelineHandler = vi.fn()
    ws.subscribe('devices', deviceHandler)
    ws.subscribe('pipelines', pipelineHandler)

    latestSocket().receive({ type: 'event', topic: 'devices', data: { deviceId: 'dev_1', value: true } })

    expect(deviceHandler).toHaveBeenCalledWith({ deviceId: 'dev_1', value: true })
    expect(pipelineHandler).not.toHaveBeenCalled()
  })

  it('re-subscribes every still-active topic after a reconnect', () => {
    const ws = useWsStore()
    ws.connect()
    latestSocket().open()

    ws.subscribe('devices', vi.fn())
    const firstSocket = latestSocket()

    firstSocket.close()
    expect(ws.connectionState).toBe('reconnecting')

    vi.advanceTimersByTime(500)
    expect(FakeWebSocket.instances.length).toBe(2)

    latestSocket().open()
    const subscribeFrames = latestSocket().sent.filter((s) => JSON.parse(s).type === 'subscribe')
    expect(subscribeFrames).toHaveLength(1)
    expect(JSON.parse(subscribeFrames[0]!).topic).toBe('devices')
  })

  it('marks the connection open once the socket opens', () => {
    const ws = useWsStore()
    expect(ws.connectionState).toBe('closed')
    ws.connect()
    expect(ws.connectionState).toBe('connecting')
    latestSocket().open()
    expect(ws.connectionState).toBe('open')
    expect(ws.isStale).toBe(false)
  })
})
