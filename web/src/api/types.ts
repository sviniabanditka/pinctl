// Mirrors internal/model JSON shapes (docs/09-data-model.md) and the
// internal/api response shapes (docs/10-api.md). Hand-kept in sync — no
// server-driven codegen in MVP.

export type DeviceRole = 'digital-in' | 'digital-out' | 'analog-in' | 'pwm-out' | 'servo'

export interface DeviceOptions {
  pullup?: boolean
  initial?: boolean | number
  onDisconnect?: 'hold' | 'safe-value'
  safeValue?: boolean | number
  /** servo: SERVO_CONFIG min pulse width in microseconds, default 544 */
  minPulseUs?: number
  /** servo: SERVO_CONFIG max pulse width in microseconds, default 2400 */
  maxPulseUs?: number
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

/** GET /pipelines list-view shape — no nodes/edges. `detail` is present
 * (non-empty) only while status is `paused` or `error` — e.g. "device
 * dev_9zq3v7ab4c is unreachable (board brd_4kx8n2wq1r)" (internal/api/
 * pipelines.go's pipelineSummary). */
export interface PipelineSummary {
  id: string
  name: string
  enabled: boolean
  tickMs: number
  nodeCount: number
  status: PipelineStatus
  detail?: string
}

/** GET/PUT /pipelines/{id} full-graph shape. `status`/`detail` are only
 * ever present on the GET response (internal/api/pipelines.go's
 * pipelineDetailResponse) — PUT's pipelineSaveResponse doesn't carry them,
 * hence optional here rather than a second near-identical type. */
export interface Pipeline {
  id: string
  name: string
  enabled: boolean
  tickMs: number
  nodes: Node[]
  edges: Edge[]
  createdAt: string
  updatedAt: string
  status?: PipelineStatus
  detail?: string
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
  servoPins: number[]
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

// --- Value history (docs/16-history.md) ---

/** One point in a GET /devices/{id}/history response. `min`/`max` are
 * present only when the query was bucketed. */
export interface HistorySample {
  t: string
  v: number
  min?: number
  max?: number
}

export interface HistoryResponse {
  deviceId: string
  role: DeviceRole
  from: string
  to: string
  /** Echoes the request's `bucket`; absent for a raw-samples query. */
  bucket?: string
  samples: HistorySample[]
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
