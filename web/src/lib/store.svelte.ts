// App-wide state: the session, live jobs from the WebSocket, and the
// paged History list. The server is the source of truth; nothing is cached
// beyond this page's lifetime.
import { SvelteMap } from 'svelte/reactivity'
import { api, ApiError, errorMessage, setUnauthorizedHandler, wsURL } from './api'
import { ACTIVE_STATUSES, FINISHED_STATUSES, type Category, type Info, type Job, type WsMessage } from './types'

// ---- Session ---------------------------------------------------------------

type SessionStatus = 'loading' | 'login' | 'ready' | 'unreachable'

class Session {
  status = $state<SessionStatus>('loading')
  info = $state<Info | null>(null)
  error = $state<string | null>(null)

  get categories(): Category[] {
    return this.info?.categories ?? []
  }

  async boot(): Promise<void> {
    this.error = null
    try {
      this.info = await api.info()
      this.status = 'ready'
      live.start()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        this.status = 'login'
      } else {
        this.status = 'unreachable'
        this.error = errorMessage(e)
      }
    }
  }

  /** Reloads server info (categories, versions); keeps the old value on failure. */
  async refreshInfo(): Promise<void> {
    try {
      this.info = await api.info()
    } catch {
      // The connection banner already reports an unreachable server.
    }
  }

  async login(token: string): Promise<void> {
    await api.login(token)
    await this.boot()
  }

  async logout(): Promise<void> {
    try {
      await api.logout()
    } finally {
      this.signedOut()
    }
  }

  signedOut(): void {
    live.stop()
    history.reset()
    this.info = null
    this.status = 'login'
  }
}

export const session = new Session()
setUnauthorizedHandler(() => {
  if (session.status === 'ready') session.signedOut()
})

// ---- Live jobs (WebSocket) -------------------------------------------------

export type Connection = 'connecting' | 'connected' | 'disconnected'

const isActive = (j: Job) => ACTIVE_STATUSES.includes(j.status)

class Live {
  connection = $state<Connection>('connecting')
  retryIn = $state<number | null>(null)
  /** Every job not in a finished state, by id. */
  jobs = new SvelteMap<string, Job>()

  #ws: WebSocket | null = null
  #stopped = true
  #attempt = 0
  #everConnected = false
  #timer: ReturnType<typeof setInterval> | null = null

  start(): void {
    if (!this.#stopped) return
    this.#stopped = false
    this.#attempt = 0
    this.#connect()
  }

  stop(): void {
    this.#stopped = true
    this.#clearTimer()
    this.#ws?.close()
    this.#ws = null
    this.jobs.clear()
    this.connection = 'connecting'
    this.retryIn = null
    this.#everConnected = false
  }

  /** Reconnects now (Refresh), skipping any backoff wait. */
  reconnect(): void {
    if (this.#stopped) return
    this.#clearTimer()
    this.#ws?.close()
    this.#attempt = 0
    this.#connect()
  }

  #connect(): void {
    this.connection = 'connecting'
    this.retryIn = null
    const ws = new WebSocket(wsURL())
    this.#ws = ws
    let opened = false
    ws.onopen = () => {
      opened = true
      this.#attempt = 0
      this.connection = 'connected'
    }
    ws.onmessage = (ev) => {
      let msg: WsMessage
      try {
        msg = JSON.parse(String(ev.data)) as WsMessage
      } catch {
        return
      }
      this.#apply(msg)
    }
    ws.onclose = () => {
      if (this.#ws !== ws || this.#stopped) return
      this.#ws = null
      this.connection = 'disconnected'
      if (!opened) {
        // The upgrade may have been refused because the session expired;
        // a 401 here signs the page out through the API client.
        void api.info().catch(() => {})
      }
      this.#scheduleRetry()
    }
  }

  #apply(msg: WsMessage): void {
    switch (msg.type) {
      case 'snapshot': {
        this.jobs.clear()
        for (const j of msg.jobs) this.jobs.set(j.id, j)
        // Finished jobs missed while disconnected (server spec §7).
        if (this.#everConnected) void history.catchUp()
        this.#everConnected = true
        break
      }
      case 'job_added':
      case 'job_updated':
        this.upsert(msg.job)
        break
      case 'job_removed':
        for (const id of msg.job_ids) this.jobs.delete(id)
        history.remove(msg.job_ids)
        break
    }
  }

  /** Applies a job from an event or an action response. */
  upsert(job: Job): void {
    if (isActive(job)) {
      this.jobs.set(job.id, job)
      history.remove([job.id])
    } else {
      this.jobs.delete(job.id)
      history.upsert(job)
    }
  }

  #scheduleRetry(): void {
    const delay = Math.min(30, 2 ** Math.min(this.#attempt, 5))
    this.#attempt++
    let left = delay
    this.retryIn = left
    this.#clearTimer()
    this.#timer = setInterval(() => {
      left--
      if (left > 0) {
        this.retryIn = left
        return
      }
      this.#clearTimer()
      if (!this.#stopped) this.#connect()
    }, 1000)
  }

  #clearTimer(): void {
    if (this.#timer) clearInterval(this.#timer)
    this.#timer = null
  }
}

export const live = new Live()

// ---- History ---------------------------------------------------------------

const PAGE = 50

class History {
  jobs = $state<Job[]>([])
  total = $state(0)
  loading = $state(false)
  error = $state<string | null>(null)
  loaded = $state(false)

  get done(): boolean {
    return this.loaded && this.jobs.length >= this.total
  }

  reset(): void {
    this.jobs = []
    this.total = 0
    this.loaded = false
    this.error = null
  }

  async load(): Promise<void> {
    this.loading = true
    this.error = null
    try {
      const page = await api.listJobs({ status: [...FINISHED_STATUSES], limit: PAGE, offset: 0 })
      this.jobs = page.jobs
      this.total = page.total
      this.loaded = true
    } catch (e) {
      this.error = errorMessage(e)
    } finally {
      this.loading = false
    }
  }

  async loadMore(): Promise<void> {
    if (this.loading || this.done) return
    this.loading = true
    try {
      const page = await api.listJobs({ status: [...FINISHED_STATUSES], limit: PAGE, offset: this.jobs.length })
      const seen = new Set(this.jobs.map((j) => j.id))
      this.jobs = [...this.jobs, ...page.jobs.filter((j) => !seen.has(j.id))]
      this.total = page.total
    } catch (e) {
      this.error = errorMessage(e)
    } finally {
      this.loading = false
    }
  }

  async catchUp(): Promise<void> {
    if (!this.loaded) return
    const since = this.jobs.reduce<string | null>((max, j) => (j.finished_at && (!max || j.finished_at > max) ? j.finished_at : max), null)
    if (!since) return void this.load()
    try {
      const page = await api.listJobs({ status: [...FINISHED_STATUSES], finished_after: since, limit: 500 })
      for (const j of page.jobs) this.upsert(j)
    } catch {
      // Next reconnect or a manual refresh tries again.
    }
  }

  /** Inserts or replaces a finished job, keeping newest `created_at` first. */
  upsert(job: Job): void {
    if (!this.loaded) return
    const i = this.jobs.findIndex((j) => j.id === job.id)
    if (i >= 0) {
      this.jobs[i] = job
      return
    }
    const at = this.jobs.findIndex((j) => j.created_at < job.created_at)
    if (at < 0) this.jobs.push(job)
    else this.jobs.splice(at, 0, job)
    this.total++
  }

  remove(ids: string[]): void {
    const drop = new Set(ids)
    const before = this.jobs.length
    this.jobs = this.jobs.filter((j) => !drop.has(j.id))
    this.total = Math.max(0, this.total - (before - this.jobs.length))
  }
}

export const history = new History()

// ---- Snackbar --------------------------------------------------------------

export interface Toast {
  id: number
  text: string
  action?: { label: string; run: () => void }
}

class Toasts {
  items = $state<Toast[]>([])
  #next = 1

  show(text: string, action?: Toast['action']): void {
    const id = this.#next++
    this.items = [...this.items.slice(-2), { id, text, action }]
    setTimeout(() => this.dismiss(id), action ? 8000 : 4000)
  }

  dismiss(id: number): void {
    this.items = this.items.filter((t) => t.id !== id)
  }
}

export const toasts = new Toasts()

// ---- Confirm dialogs -------------------------------------------------------

export interface ConfirmOptions {
  title: string
  body?: string
  confirm: string
  cancel?: string
  danger?: boolean
  /** An optional checkbox, e.g. "Also delete file from disk". */
  checkbox?: string
}

export interface ConfirmRequest extends ConfirmOptions {
  resolve: (r: { ok: boolean; checked: boolean }) => void
}

class Confirmer {
  current = $state<ConfirmRequest | null>(null)

  ask(opts: ConfirmOptions): Promise<{ ok: boolean; checked: boolean }> {
    this.current?.resolve({ ok: false, checked: false })
    return new Promise((resolve) => {
      this.current = {
        ...opts,
        resolve: (r) => {
          this.current = null
          resolve(r)
        },
      }
    })
  }
}

export const confirmer = new Confirmer()

/** Asks and resolves to true only when confirmed. */
export async function confirm(opts: ConfirmOptions): Promise<boolean> {
  return (await confirmer.ask(opts)).ok
}

// ---- Per-browser preferences -----------------------------------------------

const PREF_PREFIX = 'fetcharr.'

export function readPref(key: string): string | null {
  try {
    return localStorage.getItem(PREF_PREFIX + key)
  } catch {
    return null
  }
}

export function writePref(key: string, value: string | null): void {
  try {
    if (value == null) localStorage.removeItem(PREF_PREFIX + key)
    else localStorage.setItem(PREF_PREFIX + key, value)
  } catch {
    // Storage may be unavailable (private mode); preferences are optional.
  }
}
