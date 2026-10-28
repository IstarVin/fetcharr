// Presentation mapping shared by Queue and History. Ported from the Android
// app (util/Formatters.kt, ui/common/JobText.kt, ui/queue/QueueModels.kt,
// ui/history/HistoryModels.kt) so both clients read the same.
import type { Category, Job, JobStatus, Layout } from './types'

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB']

/** Binary units with one decimal, e.g. `3.1 MiB`; whole bytes below 1 KiB. Null in, null out. */
export function bytes(value: number | null | undefined): string | null {
  if (value == null || value < 0) return null
  if (value < 1024) return `${Math.trunc(value)} B`
  let v = value
  let unit = 0
  while (v >= 1024 && unit < UNITS.length - 1) {
    v /= 1024
    unit++
  }
  // 1023.96 KiB would round to "1024.0 KiB"; carry it to the next unit.
  if (Math.round(v * 10) >= 10240 && unit < UNITS.length - 1) {
    v /= 1024
    unit++
  }
  return `${v.toFixed(1)} ${UNITS[unit]}`
}

export function speed(bps: number | null | undefined): string | null {
  const b = bytes(bps)
  return b == null ? null : `${b}/s`
}

/** `h:mm:ss` from one hour up, otherwise `m:ss`. */
export function eta(seconds: number | null | undefined): string | null {
  if (seconds == null || seconds < 0) return null
  const s = Math.trunc(seconds)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s % 60)}` : `${m}:${pad(s % 60)}`
}

export function isEpisode(job: Job): boolean {
  return !!job.show_name?.trim() && (job.season != null || job.episode != null)
}

const RELEASE_TAGS =
  /^(?:(?:19|20)\d\d|\d{3,4}p|4k|uhd|web-?dl|web-?rip|blu-?ray|bdrip|brrip|hdrip|dvdrip|hdtv|remux|x26[45]|h\.?26[45]|hevc|av1|aac|ddp?\d?|atmos|mul[a-z]*|dual|ma|amzn|nf|dsnp|hmax|10bit)$/i

/** Scene-style names (`Some.Movie.2026.2160p.WEB-DL`) read as a title. Display only. */
export function tidyReleaseName(raw: string): string {
  if (/\s/.test(raw) || [...raw].filter((c) => c === '.' || c === '_').length < 2) return raw
  const words = raw.split(/[._]/).filter((w) => w.length > 0)
  const cut = words.findIndex((w) => RELEASE_TAGS.test(w))
  const kept = cut > 0 ? words.slice(0, cut) : words
  return kept.join(' ').trim() || raw
}

/** `Show — S02E03` for episodes, otherwise `Title (Year)` (falling back to the show name, then the URL). */
export function heading(job: Job): string {
  if (isEpisode(job)) {
    let code = ''
    if (job.season != null) code += 'S' + String(job.season).padStart(2, '0')
    if (job.episode != null) code += 'E' + String(job.episode).padStart(2, '0')
    return `${job.show_name!.trim()} — ${code}`
  }
  const title = job.title?.trim()
  const name = (title ? tidyReleaseName(title) : null) ?? (job.show_name?.trim() || job.url)
  return job.year != null ? `${name} (${job.year})` : name
}

/** The episode title shown under an episode heading. */
export function secondary(job: Job): string | null {
  return isEpisode(job) ? job.title?.trim() || null : null
}

export const STATUS_LABEL: Record<JobStatus, string> = {
  pending: 'Queued',
  downloading: 'Downloading',
  processing: 'Finishing…',
  paused: 'Paused',
  completed: 'Completed',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

/** 0..1 for a job's progress bar; null when unknown. */
export function jobFraction(job: Job): number | null {
  const p = job.progress
  if (!p) return null
  if (p.percent != null) return clamp01(p.percent / 100)
  if (p.total_bytes != null && p.total_bytes > 0 && p.downloaded_bytes != null) return clamp01(p.downloaded_bytes / p.total_bytes)
  return null
}

/** The bar shown on a card: null for pending (no bar) and processing (indeterminate). */
export function cardFraction(job: Job): number | null {
  return job.status === 'processing' || job.status === 'pending' ? null : jobFraction(job)
}

export function percentLabel(fraction: number): string {
  return `${Math.min(100, Math.max(0, Math.round(fraction * 100)))}%`
}

function clamp01(v: number): number {
  return Math.min(1, Math.max(0, v))
}

export interface StatParts {
  speed: string | null
  eta: string | null
  size: string | null
}

/** Live stats for downloading and paused jobs; parts the server did not send are null. */
export function statParts(job: Job): StatParts | null {
  if (job.status !== 'downloading' && job.status !== 'paused') return null
  const p = job.progress
  if (!p) return null
  const live = job.status === 'downloading'
  const done = bytes(p.downloaded_bytes)
  const total = bytes(p.total_bytes)
  const e = live ? eta(p.eta_seconds) : null
  return {
    speed: live && (p.speed_bps ?? 0) > 0 ? speed(p.speed_bps) : null,
    eta: e ? `${e} left` : null,
    size: done != null && total != null ? `${done} / ${total}` : done,
  }
}

/** `3.1 MiB/s · 1:52 left · 463.0 MiB / 720.0 MiB`, or null. */
export function statsLine(job: Job): string | null {
  const s = statParts(job)
  if (!s) return null
  const parts = [s.speed, s.eta, s.size].filter((x): x is string => x != null)
  return parts.length ? parts.join(' · ') : null
}

export type JobAction = 'pause' | 'resume' | 'cancel'

export function actionsFor(status: JobStatus): JobAction[] {
  switch (status) {
    case 'pending':
    case 'downloading':
      return ['pause', 'cancel']
    case 'processing':
      return ['cancel']
    case 'paused':
      return ['resume', 'cancel']
    default:
      return []
  }
}

// ---- Queue -----------------------------------------------------------------

export type SectionKind = 'active' | 'queued' | 'paused'

export const SECTION_LABEL: Record<SectionKind, string> = { active: 'Active', queued: 'Queued', paused: 'Paused' }

export type QueueItem =
  | { kind: 'job'; key: string; job: Job }
  | { kind: 'batch'; key: string; batchId: string; title: string; jobs: Job[]; progress: number | null }

export interface QueueSection {
  kind: SectionKind
  items: QueueItem[]
  /** Combined live speed; Active only. */
  speedLabel: string | null
}

export function buildQueueSections(jobs: Job[]): QueueSection[] {
  const active = jobs.filter((j) => j.status === 'downloading' || j.status === 'processing')
  const queued = jobs.filter((j) => j.status === 'pending').sort((a, b) => a.queued_at.localeCompare(b.queued_at))
  const paused = jobs.filter((j) => j.status === 'paused')
  const out: QueueSection[] = []
  for (const [kind, list] of [
    ['active', active],
    ['queued', queued],
    ['paused', paused],
  ] as const) {
    if (list.length === 0) continue
    out.push({ kind, items: groupItems(kind, list), speedLabel: kind === 'active' ? combinedSpeed(list) : null })
  }
  return out
}

function combinedSpeed(jobs: Job[]): string | null {
  const total = jobs.filter((j) => j.status === 'downloading').reduce((sum, j) => sum + Math.max(0, j.progress?.speed_bps ?? 0), 0)
  return total > 0 ? speed(total) : null
}

function groupItems(kind: SectionKind, jobs: Job[]): QueueItem[] {
  const byBatch = new Map<string, Job[]>()
  for (const j of jobs) {
    if (j.batch_id) byBatch.set(j.batch_id, [...(byBatch.get(j.batch_id) ?? []), j])
  }
  const emitted = new Set<string>()
  const items: QueueItem[] = []
  for (const j of jobs) {
    const members = j.batch_id ? byBatch.get(j.batch_id) : undefined
    if (!j.batch_id || !members) {
      items.push({ kind: 'job', key: `${kind}:job:${j.id}`, job: j })
    } else if (!emitted.has(j.batch_id)) {
      emitted.add(j.batch_id)
      items.push({ kind: 'batch', key: `${kind}:batch:${j.batch_id}`, batchId: j.batch_id, title: batchTitle(members), jobs: members, progress: combinedProgress(members) })
    }
  }
  return items
}

export function batchTitle(members: Job[]): string {
  const name = members.find((m) => m.show_name?.trim())?.show_name?.trim() ?? members.find((m) => m.title?.trim())?.title?.trim() ?? 'Batch'
  const seasons = [...new Set(members.map((m) => m.season).filter((s): s is number => s != null))]
  return seasons.length === 1 ? `${name} — Season ${seasons[0]}` : name
}

/** Sum downloaded / sum total when every job reports both, else the mean of known fractions. */
export function combinedProgress(members: Job[]): number | null {
  if (members.every((m) => m.progress?.downloaded_bytes != null && (m.progress.total_bytes ?? 0) > 0)) {
    const total = members.reduce((s, m) => s + m.progress!.total_bytes!, 0)
    const done = members.reduce((s, m) => s + m.progress!.downloaded_bytes!, 0)
    return clamp01(done / total)
  }
  const fractions = members.map(jobFraction).filter((f): f is number => f != null)
  return fractions.length ? fractions.reduce((a, b) => a + b, 0) / fractions.length : null
}

// ---- Add -------------------------------------------------------------------

/** `Added N downloads`, or `Added N · M already in library · K already queued` when episodes were skipped. */
export function batchAddedMessage(created: number, skipped: { reason: string }[]): string {
  if (!skipped.length) return created === 1 ? 'Added 1 download' : `Added ${created} downloads`
  const inLibrary = skipped.filter((s) => s.reason === 'already_exists').length
  const queued = skipped.length - inLibrary
  return [`Added ${created}`, inLibrary && `${inLibrary} already in library`, queued && `${queued} already queued`].filter(Boolean).join(' · ')
}

// ---- History ---------------------------------------------------------------

export function errorText(code: string | null | undefined): string {
  switch (code) {
    case 'low_disk_space':
      return 'Not enough disk space on server'
    case 'already_exists':
      return 'File already exists in library'
    case 'processing_failed':
      return "Couldn't save file"
    default:
      return 'Download failed'
  }
}

/** Completed: the file path. Failed: a short error. Cancelled: `Cancelled`. */
export function historyStatusLine(job: Job): string {
  switch (job.status) {
    case 'completed':
      return job.file_path?.trim() || 'Completed'
    case 'failed':
      return errorText(job.error_code)
    case 'cancelled':
      return 'Cancelled'
    default:
      return ''
  }
}

export function completedSize(job: Job): string | null {
  if (job.status !== 'completed') return null
  const p = job.progress
  const total = p?.total_bytes != null && p.total_bytes > 0 ? p.total_bytes : null
  const done = p?.downloaded_bytes != null && p.downloaded_bytes > 0 ? p.downloaded_bytes : null
  return bytes(total ?? done)
}

export interface DayGroup {
  key: string
  label: string
  jobs: Job[]
}

function localDayKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/** Groups jobs (kept in order) by the local day they finished: Today, Yesterday, then the date. */
export function groupByDay(jobs: Job[], now: Date = new Date(), locale?: string): DayGroup[] {
  const today = localDayKey(now)
  const y = new Date(now)
  y.setDate(y.getDate() - 1)
  const yesterday = localDayKey(y)
  const sameYear = new Intl.DateTimeFormat(locale, { weekday: 'short', day: 'numeric', month: 'short' })
  const otherYear = new Intl.DateTimeFormat(locale, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' })
  const groups = new Map<string, DayGroup>()
  for (const j of jobs) {
    const d = new Date(j.finished_at ?? j.created_at)
    const key = localDayKey(d)
    let g = groups.get(key)
    if (!g) {
      const label = key === today ? 'Today' : key === yesterday ? 'Yesterday' : (d.getFullYear() === now.getFullYear() ? sameYear : otherYear).format(d)
      g = { key, label, jobs: [] }
      groups.set(key, g)
    }
    g.jobs.push(j)
  }
  return [...groups.values()]
}

export function timeOfDay(iso: string | null, locale?: string): string {
  if (!iso) return ''
  return new Intl.DateTimeFormat(locale, { hour: 'numeric', minute: '2-digit' }).format(new Date(iso))
}

// ---- Categories ------------------------------------------------------------

export function categoryLabel(categories: Category[], id: string): string {
  return categories.find((c) => c.id === id)?.label ?? id
}

export function categoryLayout(categories: Category[], id: string): Layout | null {
  return categories.find((c) => c.id === id)?.layout ?? null
}

export const LAYOUT_LABEL: Record<Layout, string> = { movie: 'Movie', tv: 'TV show', flat: 'Flat folder' }

// ---- Byte sizes in decimal GB (server settings) -----------------------------

/** `5000000000` → `"5"`, `1500000000` → `"1.5"`. */
export function bytesToGb(value: number): string {
  const s = String(Math.trunc(value)).padStart(10, '0')
  const whole = s.slice(0, -9).replace(/^0+(?=\d)/, '')
  const frac = s.slice(-9).replace(/0+$/, '')
  return frac ? `${whole}.${frac}` : whole
}

/** Decimal GB with at most nine decimals → bytes; null when invalid. */
export function gbToBytes(text: string): number | null {
  const m = /^\s*(\d+)(?:\.(\d{0,9}))?\s*$/.exec(text)
  if (!m) return null
  const n = Number(m[1]! + (m[2] ?? '').padEnd(9, '0'))
  return Number.isSafeInteger(n) ? n : null
}
