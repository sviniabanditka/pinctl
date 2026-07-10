// Pure logic for Simulate mode (docs/18-simulation.md, POST /api/v1/simulate):
// request building, write carry-forward, frame/scrubber index math, and
// unseeded-input warning mapping. Kept dependency-free (no store, no Vue) so
// it's unit-testable the same way lib/graphMapping.ts and
// lib/clientValidation.ts are — see simulate.test.ts.

import type { Device, Edge as ModelEdge, Node as ModelNode, SimulateRequest, SimulateScriptEntry, SimulateWarning } from '../api/types'

/** Hard cap on a simulate request's `steps`, mirroring internal/simulate.MaxSteps. */
export const SIM_MAX_STEPS = 2000

/** The steps field's default when Simulate mode is first entered. */
export const SIM_DEFAULT_STEPS = 100

/** Fixed scrubber playback rate (ms per frame) — deliberately not tied to
 * the pipeline's own tickMs, which can be as low as a few ms and would make
 * "real-time" playback unwatchable (docs/18-simulation.md: a delay resolves
 * in a "few milliseconds of wall time"). */
export const SIM_PLAYBACK_INTERVAL_MS = 150

/** Clamps a requested step count to `[0, SIM_MAX_STEPS]`, per the endpoint's
 * documented cap (a request over the cap is a 400, so the UI never sends one). */
export function clampSimSteps(n: number): number {
  if (!Number.isFinite(n) || n < 0) return 0
  return Math.min(Math.floor(n), SIM_MAX_STEPS)
}

/** Builds the exact POST /api/v1/simulate body from the editor's current
 * draft graph plus the panel's inputs/script/steps state. `inputs` and
 * `script` are copied defensively so later store mutations can't leak into
 * an in-flight request object. */
export function buildSimulateRequest(
  pipeline: { name: string; tickMs: number; nodes: ModelNode[]; edges: ModelEdge[] },
  inputs: Record<string, number>,
  script: SimulateScriptEntry[],
  steps: number,
): SimulateRequest {
  return {
    pipeline: { name: pipeline.name, tickMs: pipeline.tickMs, nodes: pipeline.nodes, edges: pipeline.edges },
    steps: clampSimSteps(steps),
    inputs: { ...inputs },
    script: script.map((e) => ({ ...e })),
  }
}

/** Device ids read by the graph's `device-read` nodes, in first-seen order, deduped. */
export function readDeviceIds(nodes: ModelNode[]): string[] {
  return uniqueDeviceIds(nodes, 'device-read')
}

/** Device ids driven by the graph's `device-write` nodes, in first-seen order, deduped. */
export function writeDeviceIds(nodes: ModelNode[]): string[] {
  return uniqueDeviceIds(nodes, 'device-write')
}

function uniqueDeviceIds(nodes: ModelNode[], type: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const n of nodes) {
    if (n.type !== type) continue
    const id = n.config?.deviceId
    if (typeof id === 'string' && id.length > 0 && !seen.has(id)) {
      seen.add(id)
      out.push(id)
    }
  }
  return out
}

export interface WriteState {
  value: boolean | number
  changed: boolean
}

/**
 * Carries each device's last written value forward across frames: a device
 * absent from a given frame's `writes` keeps whatever it was last written
 * as (docs/18-simulation.md: "writes only contains a device when its value
 * changed that tick... don't present an absent write as 'no value'; it
 * means 'unchanged'"). `changed` is true only for the frame(s) where the
 * device actually appears in that frame's own `writes` map — the signal a
 * device-write node uses to mark the tick its pin value actually moved.
 */
export function carryForwardWrites(frames: { writes: Record<string, boolean | number> }[]): Record<string, WriteState>[] {
  const out: Record<string, WriteState>[] = []
  let last: Record<string, boolean | number> = {}
  for (const frame of frames) {
    last = { ...last, ...frame.writes }
    const state: Record<string, WriteState> = {}
    for (const [deviceId, value] of Object.entries(last)) {
      state[deviceId] = { value, changed: Object.prototype.hasOwnProperty.call(frame.writes, deviceId) }
    }
    out.push(state)
  }
  return out
}

/** Clamps a frame index into `[0, frameCount - 1]` (0 when there are no frames at all). */
export function clampFrameIndex(index: number, frameCount: number): number {
  if (frameCount <= 0) return 0
  return Math.max(0, Math.min(index, frameCount - 1))
}

/** Steps a frame index by `delta`, clamped — used by the ◀/▶ buttons and the ←/→ keys. */
export function stepFrameIndex(index: number, frameCount: number, delta: number): number {
  return clampFrameIndex(index + delta, frameCount)
}

/**
 * Maps `unseeded_input` warnings (the endpoint's only current code, per
 * docs/18-simulation.md) onto device display names, deduped by device — the
 * response can repeat a warning per tick the device was read, but the panel
 * shows it once per device.
 */
export function mapUnseededWarnings(warnings: SimulateWarning[], devices: Device[]): { deviceId: string; deviceName: string }[] {
  const seen = new Set<string>()
  const out: { deviceId: string; deviceName: string }[] = []
  for (const w of warnings) {
    if (w.code !== 'unseeded_input' || seen.has(w.deviceId)) continue
    seen.add(w.deviceId)
    out.push({ deviceId: w.deviceId, deviceName: devices.find((d) => d.id === w.deviceId)?.name ?? w.deviceId })
  }
  return out
}
