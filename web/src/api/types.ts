// Mirrors internal/model JSON shapes (docs/09-data-model.md) and the
// internal/api response shapes (docs/10-api.md). Hand-kept in sync — no
// server-driven codegen in MVP.

export type DeviceRole = 'digital-in' | 'digital-out' | 'analog-in' | 'pwm-out'

export interface DeviceOptions {
  pullup?: boolean
  initial?: boolean | number
  onDisconnect?: 'hold' | 'safe-value'
  safeValue?: boolean | number
}

export interface Device {
  id: string
  name: string
  boardId: string
  pin: number
  role: DeviceRole
  options: DeviceOptions
  createdAt: string
  updatedAt: string
}

export interface Position {
  x: number
  y: number
}

export interface Node {
  id: string
  type: string
  config: Record<string, unknown>
  position: Position
}

export interface Edge {
  from: string
  to: string
}

export type PipelineStatus = 'running' | 'paused' | 'error' | 'stopped'

/** GET /pipelines list-view shape — no nodes/edges. */
export interface PipelineSummary {
  id: string
  name: string
  enabled: boolean
  tickMs: number
  nodeCount: number
  status: PipelineStatus
}

/** GET/PUT /pipelines/{id} full-graph shape. */
export interface Pipeline {
  id: string
  name: string
  enabled: boolean
  tickMs: number
  nodes: Node[]
  edges: Edge[]
  createdAt: string
  updatedAt: string
}

export interface PipelineWriteRequest {
  name: string
  tickMs: number
  nodes: Node[]
  edges: Edge[]
}

export interface ValidationIssue {
  field?: string
  code: string
  message: string
}

export interface ValidateResponse {
  valid: boolean
  errors?: ValidationIssue[]
  warnings?: ValidationIssue[]
}

export interface PipelineActionResponse {
  id: string
  enabled: boolean
  status: PipelineStatus
}

/**
 * GET /api/v1/board — v0.1 single-board endpoint. Superseded by the plural
 * /boards collection (BoardEntry below) in v0.4, but kept as the Dashboard's
 * fallback for a server built without a multi-board Manager (single-board
 * dev mode, docs/12-roadmap.md) — see internal/api/boards.go's
 * boardsSnapshot doc comment.
 */
export interface BoardInfo {
  connected: boolean
  port: string
  firmware: { name: string; major: number; minor: number }
  protocolVersion: string
  capabilities: { pin: number; modes: string[] }[]
}

export type BoardStatus = 'connected' | 'disconnected' | 'reconnecting' | 'error'

export interface BoardIdentity {
  kind: string
  key: string
}

export interface BoardCapabilities {
  digitalPins: number[]
  analogPins: number[]
  pwmPins: number[]
}

/** GET /api/v1/boards item shape (internal/api/boards.go's boardOut). */
export interface BoardEntry {
  id: string
  name: string
  identity: BoardIdentity
  portHint?: string
  status: BoardStatus
  port: string | null
  /** Live session data: present only while the board is connected. */
  firmware: { name: string; major: number; minor: number } | null
  capabilities: BoardCapabilities | null
}

export interface BoardsTopicData {
  boardId: string
  status: BoardStatus
  port: string | null
}

// --- WebSocket protocol (docs/10-api.md#websocket-apiv1ws) ---

export type WsMessageType = 'subscribe' | 'unsubscribe' | 'ping' | 'pong' | 'event' | 'error'

export interface WsMessage<T = unknown> {
  type: WsMessageType
  topic: string
  data: T
}

export interface DevicesTopicData {
  deviceId: string
  value: boolean | number
  timestamp: string
}

export interface PipelinesTopicData {
  pipelineId: string
  status: PipelineStatus
  detail?: string
}

export interface PipelineTickTopicData {
  pipelineId: string
  tick: number
  nodes: Record<string, boolean | number>
  log?: { nodeId: string; label: string; value: boolean | number; timestamp: string }[]
}

export interface WsErrorData {
  code: string
  message: string
}
