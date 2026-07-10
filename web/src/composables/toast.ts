import { reactive } from 'vue'
import { ApiError } from '../api/client'

export type ToastSeverity = 'info' | 'warning' | 'error'

export interface Toast {
  id: number
  message: string
  detail?: string
  severity: ToastSeverity
  sticky: boolean
}

let nextId = 1
const toasts = reactive<Toast[]>([])

const AUTO_DISMISS_MS = 5000

function severityFor(code: string): ToastSeverity {
  if (code === 'validation_error' || code === 'conflict' || code === 'device_in_use' || code === 'unsupported_pin_mode') {
    return 'warning'
  }
  if (code === 'not_found') return 'info'
  return 'error'
}

function detailFor(err: ApiError): string | undefined {
  const d = err.details
  if (!d) return undefined
  if (Array.isArray(d.pipelineIds) && d.pipelineIds.length > 0) {
    return `Blocking pipeline(s): ${(d.pipelineIds as string[]).join(', ')}`
  }
  if (typeof d.pipelineName === 'string') {
    return `Driven by pipeline: ${d.pipelineName}`
  }
  return undefined
}

export function pushToast(message: string, severity: ToastSeverity = 'info', detail?: string): void {
  const sticky = severity === 'error'
  const toast: Toast = { id: nextId++, message, detail, severity, sticky }
  toasts.push(toast)
  if (!sticky) {
    setTimeout(() => dismissToast(toast.id), AUTO_DISMISS_MS)
  }
}

export function pushErrorToast(err: unknown): void {
  if (err instanceof ApiError) {
    const severity = err.code === 'internal_error' ? 'error' : severityFor(err.code)
    pushToast(err.message, severity, detailFor(err))
    return
  }
  pushToast(err instanceof Error ? err.message : String(err), 'error')
}

export function dismissToast(id: number): void {
  const idx = toasts.findIndex((t) => t.id === id)
  if (idx !== -1) toasts.splice(idx, 1)
}

export function useToasts() {
  return { toasts, pushToast, pushErrorToast, dismissToast }
}
