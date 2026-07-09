// Thin fetch wrapper: every REST call in the app goes through here so the
// error-envelope-to-toast mapping is centralized (docs/11-web-ui.md
// "Pinia stores": "All stores construct their REST calls through the
// shared api/ module").

export interface ApiErrorBody {
  code: string
  message: string
  details?: Record<string, unknown>
}

/** Thrown for any non-2xx response, built from the docs/10-api.md error envelope. */
export class ApiError extends Error {
  code: string
  details?: Record<string, unknown>
  status: number

  constructor(status: number, body: ApiErrorBody) {
    super(body.message)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code
    this.details = body.details
  }
}

const BASE = '/api/v1'

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  if (res.status === 204) {
    return undefined as T
  }

  const text = await res.text()
  const json = text.length > 0 ? JSON.parse(text) : undefined

  if (!res.ok) {
    const envelope = json as { error?: ApiErrorBody } | undefined
    if (envelope?.error) {
      throw new ApiError(res.status, envelope.error)
    }
    throw new ApiError(res.status, { code: 'internal_error', message: `request failed: ${res.status}` })
  }

  return json as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {}),
  patch: <T>(path: string, body: unknown) => request<T>('PATCH', path, body),
  put: <T>(path: string, body: unknown) => request<T>('PUT', path, body),
  delete: <T>(path: string) => request<T>('DELETE', path),
}
