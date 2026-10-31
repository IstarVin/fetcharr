// Typed client for the Fetcharr API. The browser authenticates with the
// HttpOnly session cookie from POST /api/session; the token itself is never
// kept in JavaScript.
import type {
  BatchCreated,
  CreateBatch,
  CreateJob,
  Info,
  Job,
  JobList,
  JobStatus,
  Playback,
  RetryBody,
  ServerSettings,
  YtdlpCheck,
  YtdlpUpdate,
} from './types'

/** A non-2xx response, or `network` / `bad_response` when there was none to read. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

/** The API root, relative to the page so a reverse-proxy prefix is kept. */
export function apiBase(baseURI: string = document.baseURI): URL {
  return new URL('api/', baseURI)
}

export function wsURL(baseURI: string = document.baseURI): string {
  const u = new URL('ws', apiBase(baseURI))
  u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:'
  return u.toString()
}

/** Called on every 401 so the app can show the login screen. */
let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void): void {
  onUnauthorized = fn
}

async function request<T>(method: string, path: string, body?: unknown, query?: Record<string, string | number | boolean | undefined>): Promise<T> {
  const url = new URL(path, apiBase())
  for (const [k, v] of Object.entries(query ?? {})) {
    if (v !== undefined) url.searchParams.set(k, String(v))
  }
  const init: RequestInit = { method, credentials: 'same-origin', headers: { Accept: 'application/json' } }
  if (body !== undefined) {
    init.body = JSON.stringify(body)
    init.headers = { ...init.headers, 'Content-Type': 'application/json' }
  }
  let res: Response
  try {
    res = await fetch(url, init)
  } catch {
    throw new ApiError(0, 'network', "Can't reach server")
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: unknown
  try {
    data = text ? JSON.parse(text) : undefined
  } catch {
    throw new ApiError(res.status, 'bad_response', 'Not a Fetcharr server')
  }
  if (!res.ok) {
    const err = (data as { error?: { code?: string; message?: string } } | undefined)?.error
    const e = new ApiError(res.status, err?.code ?? 'internal', err?.message ?? `HTTP ${res.status}`)
    if (res.status === 401 && path !== 'session') onUnauthorized()
    throw e
  }
  return data as T
}

export const api = {
  login: (token: string) => request<void>('POST', 'session', { token }),
  logout: () => request<void>('DELETE', 'session'),

  info: () => request<Info>('GET', 'info'),

  listJobs: (q: { status?: JobStatus[]; category?: string; batch_id?: string; finished_after?: string; q?: string; limit?: number; offset?: number } = {}) =>
    request<JobList>('GET', 'jobs', undefined, { ...q, status: q.status?.join(',') }),
  getJob: (id: string) => request<Job>('GET', `jobs/${encodeURIComponent(id)}`),
  createJob: (body: CreateJob) => request<Job>('POST', 'jobs', body),
  createBatch: (body: CreateBatch) => request<BatchCreated>('POST', 'jobs/batch', body),

  jobAction: (id: string, action: 'pause' | 'resume' | 'cancel') => request<Job>('POST', `jobs/${encodeURIComponent(id)}/${action}`),
  retry: (id: string, body?: RetryBody) => request<Job>('POST', `jobs/${encodeURIComponent(id)}/retry`, body),
  /** Retries every failed job on the server. Older servers answer 404/405. */
  retryFailed: () => request<{ jobs: Job[] }>('POST', 'jobs/retry', undefined, { status: 'failed' }),
  batchAction: (batchId: string, action: 'pause' | 'resume' | 'cancel' | 'retry' | 'prioritize') =>
    request<{ jobs: Job[] }>('POST', `batches/${encodeURIComponent(batchId)}/${action}`),
  prioritize: (id: string) => request<Job>('POST', `jobs/${encodeURIComponent(id)}/prioritize`),

  deleteJob: (id: string, deleteFile: boolean) =>
    request<void>('DELETE', `jobs/${encodeURIComponent(id)}`, undefined, { delete_file: deleteFile || undefined }),
  clearHistory: () => request<{ deleted: number }>('DELETE', 'jobs'),

  playback: (id: string) => request<Playback>('POST', `jobs/${encodeURIComponent(id)}/playback`),

  ytdlpCheck: () => request<YtdlpCheck>('GET', 'system/ytdlp/check'),
  ytdlpUpdate: () => request<YtdlpUpdate>('POST', 'system/ytdlp/update'),

  getSettings: () => request<ServerSettings>('GET', 'system/settings'),
  saveSettings: (doc: ServerSettings) => request<ServerSettings>('PUT', 'system/settings', doc),
  testNotification: (id: string) => request<void>('POST', 'system/notifications/test', { id }),
}

/** Resolves a playback link (relative to the API root) to an absolute URL. */
export function playbackURL(p: Playback): string {
  return new URL(p.url, apiBase()).toString()
}

/** The message to show for an error, preferring the server's own text. */
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}
