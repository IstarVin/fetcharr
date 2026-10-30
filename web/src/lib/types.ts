// Wire types of the Fetcharr API (docs/fetcharr-server.md §6, contract/).
// They mirror server/internal/api/dto.go; request headers never appear here.

export type JobStatus = 'pending' | 'downloading' | 'processing' | 'paused' | 'completed' | 'failed' | 'cancelled'
export type Layout = 'movie' | 'tv' | 'flat'

export const ACTIVE_STATUSES: readonly JobStatus[] = ['pending', 'downloading', 'processing', 'paused']
export const FINISHED_STATUSES: readonly JobStatus[] = ['completed', 'failed', 'cancelled']

export interface Subtitle {
  url: string
  lang: string
}

export interface Progress {
  percent: number | null
  downloaded_bytes: number | null
  total_bytes: number | null
  speed_bps: number | null
  eta_seconds: number | null
}

export interface Job {
  id: string
  batch_id: string | null
  category: string
  url: string
  title: string | null
  show_name: string | null
  season: number | null
  episode: number | null
  year: number | null
  subtitles: Subtitle[]
  status: JobStatus
  progress: Progress | null
  file_path: string | null
  error_code: string | null
  error_message: string | null
  warnings: string[]
  /** Season-pack jobs only (absent otherwise and on older servers). */
  archive?: { episode_offset: number; episode_titles?: Record<string, string> }
  /** Episodes a completed season-pack job placed. */
  files?: string[]
  created_at: string
  queued_at: string
  started_at: string | null
  finished_at: string | null
}

export interface Category {
  id: string
  label: string
  layout: Layout
  path: string
  free_bytes: number
}

export interface Info {
  version: string
  ytdlp_version: string
  ytdlp_managed: boolean
  concurrency: number
  auth_required: boolean
  // Optional capability flags; older servers omit them (treat as false).
  settings_supported?: boolean
  setup_required?: boolean
  source_replacement_supported?: boolean
  queue_priority_supported?: boolean
  history_search_supported?: boolean
  notifications_supported?: boolean
  /** Daily window in which new downloads start; null or absent = any time. */
  download_window?: DownloadWindow | null
  categories: Category[]
}

export interface DownloadWindow {
  start: string
  end: string
  open: boolean
  /** When the window opens next; null while it is open. */
  opens_at: string | null
}

export interface JobList {
  jobs: Job[]
  total: number
}

export interface CreateJob {
  url: string
  category: string
  title?: string
  show_name?: string
  season?: number
  episode?: number
  year?: number
  headers?: Record<string, string>
  subtitles?: Subtitle[]
  /** Replace an existing library file instead of failing with `already_exists`. */
  overwrite?: boolean
}

export interface BatchEpisode {
  episode: number
  title?: string
  url: string
  headers?: Record<string, string>
  subtitles?: Subtitle[]
}

export interface CreateBatch {
  category: string
  show_name: string
  season: number
  year?: number
  headers?: Record<string, string>
  episodes: BatchEpisode[]
  /** Replace existing library files instead of skipping those episodes as `already_exists`. */
  overwrite?: boolean
}

export interface BatchCreated {
  batch_id: string
  jobs: Job[]
  skipped: { episode: number; reason: string }[]
}

export type HeadersMode = 'clear' | 'keep' | 'replace'

export interface RetryBody {
  overwrite?: boolean
  source?: {
    url: string
    headers_mode: HeadersMode
    headers?: Record<string, string>
    subtitles: Subtitle[]
  }
}

export interface Playback {
  url: string
  mime_type: string
  expires_at: string
}

export interface YtdlpCheck {
  current: string
  latest: string
  update_available: boolean
}

export interface YtdlpUpdate {
  previous: string
  current: string
}

export interface SettingsCategory {
  id: string
  label: string
  path: string
  layout: Layout
}

export type MediaServerType = 'jellyfin' | 'plex'

export interface SettingsMediaServer {
  id: string
  type: MediaServerType
  url: string
  path_map: Record<string, string>
  has_credential: boolean
  /** Write-only: sent to replace the saved credential, never returned. */
  credential?: string
}

export type NotificationType = 'ntfy' | 'webhook'

export interface SettingsNotification {
  id: string
  type: NotificationType
  url: string
  on_completed: boolean
  on_failed: boolean
  has_credential: boolean
  /** Write-only: omit to keep the saved token, '' removes it. */
  credential?: string
}

export interface SettingsDownloads {
  concurrency: number
  merge_format: string
  min_free_space: number
  // Absent on older servers; sent back only when the server returned them.
  rate_limit?: number
  window_start?: string
  window_end?: string
  auto_retries?: number
}

export interface ServerSettings {
  revision: number
  downloads: SettingsDownloads
  history: { retention_days: number }
  ytdlp: { auto_update: boolean }
  categories: SettingsCategory[]
  media_servers: SettingsMediaServer[]
  /** Absent on servers without push notifications. */
  notifications?: SettingsNotification[]
}

export type WsMessage =
  | { type: 'snapshot'; jobs: Job[] }
  | { type: 'job_added'; job: Job }
  | { type: 'job_updated'; job: Job }
  | { type: 'job_removed'; job_ids: string[] }
