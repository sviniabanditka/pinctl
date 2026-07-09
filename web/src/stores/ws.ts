import { defineStore } from 'pinia'
import type { WsMessage } from '../api/types'

export type WsConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'

type TopicHandler = (data: unknown) => void

const BACKOFF_STEPS_MS = [500, 1000, 2000, 5000]

function wsUrl(): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}/api/v1/ws`
}

/**
 * Single multiplexed WebSocket connection, owned once by the app (docs/11
 * "ws" store). Topic subscriptions are refcounted so multiple components/
 * stores wanting the same topic (e.g. Dashboard + Devices both wanting
 * `devices`) share one wire subscription, and it's only released when the
 * last interested party unsubscribes.
 */
export const useWsStore = defineStore('ws', {
  state: () => ({
    connectionState: 'closed' as WsConnectionState,
    refcounts: new Map<string, number>() as Map<string, number>,
    handlers: new Map<string, Set<TopicHandler>>() as Map<string, Set<TopicHandler>>,
    socket: null as WebSocket | null,
    reconnectAttempt: 0,
    reconnectTimer: null as ReturnType<typeof setTimeout> | null,
    pingTimer: null as ReturnType<typeof setInterval> | null,
    closedByApp: false,
  }),

  getters: {
    isStale: (state) => state.connectionState !== 'open',
  },

  actions: {
    connect() {
      if (this.socket || this.closedByApp) return
      this.connectionState = this.reconnectAttempt > 0 ? 'reconnecting' : 'connecting'
      const socket = new WebSocket(wsUrl())
      this.socket = socket

      socket.addEventListener('open', () => this.onOpen())
      socket.addEventListener('message', (ev: MessageEvent) => this.onMessage(ev))
      socket.addEventListener('close', () => this.onClose())
      socket.addEventListener('error', () => socket.close())
    },

    onOpen() {
      this.connectionState = 'open'
      this.reconnectAttempt = 0
      // Re-send subscribe for every topic with an active refcount — the
      // refcount table survives a reconnect, components don't need to know.
      for (const topic of this.refcounts.keys()) {
        this.send('subscribe', topic)
      }
      this.pingTimer = setInterval(() => this.send('ping', ''), 30000)
    },

    onMessage(ev: MessageEvent) {
      let msg: WsMessage
      try {
        msg = JSON.parse(ev.data as string)
      } catch {
        return
      }
      if (msg.type !== 'event') return
      const set = this.handlers.get(msg.topic)
      if (!set) return
      for (const handler of set) handler(msg.data)
    },

    onClose() {
      this.socket = null
      if (this.pingTimer) {
        clearInterval(this.pingTimer)
        this.pingTimer = null
      }
      if (this.closedByApp) {
        this.connectionState = 'closed'
        return
      }
      this.connectionState = 'reconnecting'
      const delay = BACKOFF_STEPS_MS[Math.min(this.reconnectAttempt, BACKOFF_STEPS_MS.length - 1)]
      this.reconnectAttempt += 1
      this.reconnectTimer = setTimeout(() => this.connect(), delay)
    },

    send(type: 'subscribe' | 'unsubscribe' | 'ping', topic: string) {
      if (this.socket?.readyState === WebSocket.OPEN) {
        this.socket.send(JSON.stringify({ type, topic, data: {} }))
      }
    },

    /** Register interest in `topic`, sending a wire `subscribe` only on 0→1. */
    subscribe(topic: string, handler: TopicHandler) {
      const count = this.refcounts.get(topic) ?? 0
      this.refcounts.set(topic, count + 1)
      if (!this.handlers.has(topic)) this.handlers.set(topic, new Set())
      this.handlers.get(topic)!.add(handler)
      if (count === 0) {
        this.send('subscribe', topic)
      }
    },

    /** Release interest in `topic`, sending a wire `unsubscribe` only on 1→0. */
    unsubscribe(topic: string, handler: TopicHandler) {
      const count = this.refcounts.get(topic) ?? 0
      if (count <= 0) return
      this.handlers.get(topic)?.delete(handler)
      const next = count - 1
      if (next <= 0) {
        this.refcounts.delete(topic)
        this.handlers.delete(topic)
        this.send('unsubscribe', topic)
      } else {
        this.refcounts.set(topic, next)
      }
    },

    disconnect() {
      this.closedByApp = true
      if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
      if (this.pingTimer) clearInterval(this.pingTimer)
      this.socket?.close()
      this.socket = null
      this.connectionState = 'closed'
    },
  },
})
