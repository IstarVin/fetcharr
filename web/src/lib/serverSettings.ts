// Editable draft of PUT /api/system/settings, mirroring the Android editor
// (ui/settings/ServerSettingsViewModel.kt). Credentials stay in memory only.
import { bytesToGb, gbToBytes, mbToRate, rateToMb } from './format'
import type { Layout, MediaServerType, NotificationType, ServerSettings } from './types'

export interface CategoryDraft {
  key: string
  id: string
  label: string
  path: string
  layout: Layout
  existing: boolean
}

export interface MappingDraft {
  key: string
  from: string
  to: string
}

export interface IntegrationDraft {
  id: string
  type: MediaServerType
  url: string
  credential: string
  hasCredential: boolean
  existing: boolean
  paths: MappingDraft[]
}

export interface NotificationDraft {
  id: string
  type: NotificationType
  url: string
  /** A typed replacement token; blank keeps the saved one. */
  credential: string
  hasCredential: boolean
  removeCredential: boolean
  onCompleted: boolean
  onFailed: boolean
  existing: boolean
}

export interface SettingsDraft {
  revision: number
  concurrency: string
  mergeFormat: string
  minFreeGb: string
  retentionDays: string
  autoUpdate: boolean
  categories: CategoryDraft[]
  integrations: IntegrationDraft[]
  /** Speed limit, download window and automatic retries; false on older servers. */
  downloadControls: boolean
  rateMb: string
  windowStart: string
  windowEnd: string
  autoRetries: string
  /** Null when the server has no push notifications. */
  notifications: NotificationDraft[] | null
}

export const MAX_AUTO_RETRIES = 5

// crypto.randomUUID needs a secure context; plain-http LAN servers are common.
function randomHex(bytes: number): string {
  const b = crypto.getRandomValues(new Uint8Array(bytes))
  return [...b].map((x) => x.toString(16).padStart(2, '0')).join('')
}

export function uuid(): string {
  const h = randomHex(16).split('')
  h[12] = '4'
  h[16] = '89ab'[parseInt(h[16]!, 16) & 3]!
  const s = h.join('')
  return `${s.slice(0, 8)}-${s.slice(8, 12)}-${s.slice(12, 16)}-${s.slice(16, 20)}-${s.slice(20)}`
}

/** A stable, server-compatible category id (`^[a-z0-9_]+$`), never shown. */
export const newCategoryId = () => randomHex(16)

export const newCategory = (): CategoryDraft => ({ key: uuid(), id: newCategoryId(), label: '', path: '', layout: 'flat', existing: false })
export const newMapping = (): MappingDraft => ({ key: uuid(), from: '', to: '' })
export const newNotification = (type: NotificationType = 'ntfy'): NotificationDraft => ({
  id: uuid(),
  type,
  url: '',
  credential: '',
  hasCredential: false,
  removeCredential: false,
  onCompleted: true,
  onFailed: true,
  existing: false,
})
export const newIntegration = (): IntegrationDraft => ({ id: uuid(), type: 'jellyfin', url: '', credential: '', hasCredential: false, existing: false, paths: [] })

/** Adds `http://` when no scheme is given and drops trailing slashes; null when not an http(s) URL. */
export function normalizeUrl(raw: string): string | null {
  let v = raw.trim()
  if (!v) return null
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(v)) v = `http://${v}`
  try {
    const u = new URL(v)
    if ((u.protocol !== 'http:' && u.protocol !== 'https:') || !u.hostname) return null
  } catch {
    return null
  }
  return v.replace(/\/+$/, '')
}

export function fromDocument(d: ServerSettings): SettingsDraft {
  return {
    revision: d.revision,
    concurrency: String(d.downloads.concurrency),
    mergeFormat: d.downloads.merge_format,
    minFreeGb: bytesToGb(d.downloads.min_free_space),
    retentionDays: String(d.history.retention_days),
    autoUpdate: d.ytdlp.auto_update,
    categories: d.categories.map((c) => ({ key: c.id, id: c.id, label: c.label, path: c.path, layout: c.layout, existing: true })),
    integrations: d.media_servers.map((s) => ({
      id: s.id,
      type: s.type,
      url: s.url,
      credential: '',
      hasCredential: s.has_credential,
      existing: true,
      paths: Object.entries(s.path_map ?? {}).map(([from, to]) => ({ key: uuid(), from, to })),
    })),
    downloadControls: d.downloads.rate_limit !== undefined,
    rateMb: rateToMb(d.downloads.rate_limit ?? 0),
    windowStart: d.downloads.window_start ?? '',
    windowEnd: d.downloads.window_end ?? '',
    autoRetries: String(d.downloads.auto_retries ?? 0),
    notifications:
      d.notifications?.map((n) => ({
        id: n.id,
        type: n.type,
        url: n.url,
        credential: '',
        hasCredential: n.has_credential,
        removeCredential: false,
        onCompleted: n.on_completed,
        onFailed: n.on_failed,
        existing: true,
      })) ?? null,
  }
}

const CLOCK = /^([01]\d|2[0-3]):[0-5]\d$/

/** An absolute http(s) URL typed with its scheme (notification targets often need https). */
function explicitHttpUrl(v: string): boolean {
  try {
    const u = new URL(v.trim())
    return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.hostname
  } catch {
    return false
  }
}

const isWholeNumber = (v: string) => /^\d+$/.test(v.trim())

/** Field errors keyed like Android: `concurrency`, `category.0.path`, `integration.1.path.0`. */
export function validate(d: SettingsDraft): Record<string, string> {
  const e: Record<string, string> = {}
  if (!isWholeNumber(d.concurrency) || Number(d.concurrency) < 1) e.concurrency = 'Enter a whole number of at least 1.'
  if (!d.mergeFormat.trim()) e.mergeFormat = 'This field is required.'
  if (gbToBytes(d.minFreeGb) == null) e.minFreeGb = 'Enter a nonnegative size in GB, with at most nine decimal places.'
  if (!isWholeNumber(d.retentionDays)) e.retentionDays = 'Enter a whole number of 0 or more.'
  d.categories.forEach((c, i) => {
    if (!/^[a-z0-9_]+$/.test(c.id) || d.categories.filter((o) => o.id === c.id).length > 1) {
      e[`category.${i}.id`] = 'Use a unique ID with lowercase letters, digits, and underscores.'
    }
    if (!c.label.trim()) e[`category.${i}.label`] = 'This field is required.'
    if (!c.path.trim().startsWith('/') || c.path.includes('\u0000')) e[`category.${i}.path`] = 'Enter an absolute server path beginning with /.'
  })
  if (d.downloadControls) {
    if (mbToRate(d.rateMb) == null) e.rateMb = 'Enter a speed in MB/s with at most two decimals, or leave it blank.'
    const start = d.windowStart.trim()
    const end = d.windowEnd.trim()
    if (start || end) {
      if (!CLOCK.test(start) || !CLOCK.test(end)) e.window = 'Enter both times as HH:MM, or leave both blank.'
      else if (start === end) e.window = 'Start and end must differ.'
    }
    if (!isWholeNumber(d.autoRetries) || Number(d.autoRetries) > MAX_AUTO_RETRIES) e.autoRetries = `Enter a whole number from 0 to ${MAX_AUTO_RETRIES}.`
  }
  d.notifications?.forEach((n, i) => {
    if (!explicitHttpUrl(n.url)) e[`notification.${i}.url`] = 'Enter a full http:// or https:// address.'
  })
  d.integrations.forEach((s, i) => {
    if (normalizeUrl(s.url) == null) e[`integration.${i}.url`] = 'Enter a valid http:// or https:// address'
    if (!s.hasCredential && !s.credential.trim()) e[`integration.${i}.credential`] = 'This field is required.'
    s.paths.forEach((p, j) => {
      const absTo = p.to.startsWith('/') || p.to.startsWith('\\\\') || /^[A-Za-z]:[\\/]/.test(p.to)
      if (!p.from.startsWith('/') || !absTo || s.paths.filter((o) => o.from === p.from).length > 1) {
        e[`integration.${i}.path.${j}`] = 'Use absolute paths and a unique Fetcharr prefix for each mapping.'
      }
    })
  })
  return e
}

/** The PUT body; call only when validate() is empty. */
export function toDocument(d: SettingsDraft): ServerSettings {
  return {
    revision: d.revision,
    downloads: {
      concurrency: Number(d.concurrency.trim()),
      merge_format: d.mergeFormat.trim(),
      min_free_space: gbToBytes(d.minFreeGb)!,
      ...(d.downloadControls
        ? {
            rate_limit: mbToRate(d.rateMb)!,
            window_start: d.windowStart.trim(),
            window_end: d.windowEnd.trim(),
            auto_retries: Number(d.autoRetries.trim()),
          }
        : {}),
    },
    history: { retention_days: Number(d.retentionDays.trim()) },
    ytdlp: { auto_update: d.autoUpdate },
    categories: d.categories.map((c) => ({ id: c.id, label: c.label.trim(), path: c.path.trim(), layout: c.layout })),
    media_servers: d.integrations.map((s) => ({
      id: s.id,
      type: s.type,
      url: normalizeUrl(s.url)!,
      path_map: Object.fromEntries(s.paths.map((p) => [p.from, p.to])),
      has_credential: s.hasCredential,
      ...(s.credential.trim() ? { credential: s.credential.trim() } : {}),
    })),
    ...(d.notifications
      ? {
          notifications: d.notifications.map((n) => ({
            id: n.id,
            type: n.type,
            url: n.url.trim(),
            on_completed: n.onCompleted,
            on_failed: n.onFailed,
            has_credential: n.hasCredential,
            ...(n.credential.trim() ? { credential: n.credential.trim() } : n.removeCredential ? { credential: '' } : {}),
          })),
        }
      : {}),
  }
}
