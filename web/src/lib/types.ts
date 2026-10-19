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
  categories: Category[]
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

export interface ServerSettings {
  revision: number
  downloads: { concurrency: number; merge_format: string; min_free_space: number }
  history: { retention_days: number }
  ytdlp: { auto_update: boolean }
  categories: SettingsCategory[]
  media_servers: SettingsMediaServer[]
}

export type WsMessage =
  | { type: 'snapshot'; jobs: Job[] }
  | { type: 'job_added'; job: Job }
  | { type: 'job_updated'; job: Job }
  | { type: 'job_removed'; job_ids: string[] }
