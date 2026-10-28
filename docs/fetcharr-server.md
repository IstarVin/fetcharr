# Fetcharr Server

A self-hosted download server. It accepts download jobs from the [Fetcharr Android app](fetcharr-android.md) and its built-in [web UI](fetcharr-web.md), downloads them with yt-dlp, reports live progress over WebSocket, and files finished media into a Plex/Jellyfin-compatible library.

**Stack:** Go · SQLite · yt-dlp · ffmpeg · HTTP + WebSocket

---

## 1. Overview

### Responsibilities

- Expose a REST API to create, inspect, pause, resume, cancel, retry and delete download jobs.
- Persist jobs in SQLite so the queue and history survive restarts.
- Run downloads through yt-dlp with a fixed number of concurrent workers.
- Download sidecar subtitles and convert them to SRT.
- Move finished files into the correct category folder using Plex/Jellyfin naming.
- Push job changes to every connected client over WebSocket.
- Optionally tell Jellyfin/Plex to rescan the affected folder.
- Keep its own yt-dlp binary up to date.
- Serve the browser frontend ([web UI](fetcharr-web.md)) at `/`.

### Non-goals

- No server-rendered pages. The web UI is a static app embedded in the binary that uses the same public API as Android.
- No built-in player, transcoding, metadata scraping or library management beyond placing files. Completed files can be streamed to an external player.
- No multi-user model. One optional shared API token protects the whole server.

---

## 2. Concepts

### 2.1 Category

A category is a destination folder plus a layout rule. Categories are defined in `config.yaml`; clients fetch them from `GET /api/info` and never hardcode them.

| Layout | Used for | Required job fields |
| --- | --- | --- |
| `movie` | Films | `title` |
| `tv` | Episodic content (TV, drama, anime) | `show_name`, `season`, `episode` |
| `flat` | Anything else | `title` |

### 2.2 Job

One media file to download. A job has a URL, optional request headers, optional subtitles, metadata (title, show, season, episode, year), a category, a status and live progress.

### 2.3 Batch

A set of episode jobs created together by `POST /api/jobs/batch`. Every job in it shares a `batch_id`. Batches exist so clients can group episodes and act on them together. Each job still downloads independently.

### 2.4 Job lifecycle

| Status | Meaning |
| --- | --- |
| `pending` | Waiting for a free worker. |
| `downloading` | yt-dlp is running. |
| `processing` | Download finished; fetching subtitles, converting, moving into the library. |
| `paused` | Stopped by the user. Partial files are kept. |
| `completed` | File is in the library. |
| `failed` | Stopped with an error. See `error_code`. |
| `cancelled` | Stopped by the user. Partial files are deleted. |

The normal path is `pending → downloading → processing → completed`. A job fails from whichever step hit the error (§4). User actions:

| Action | From | To |
| --- | --- | --- |
| pause | `pending`, `downloading` | `paused` |
| resume | `paused` | `pending` |
| cancel | `pending`, `downloading`, `processing`, `paused` | `cancelled` |
| retry | `failed`, `cancelled` | `pending` |
| delete | `completed`, `failed`, `cancelled` | (removed) |

Any other action on a job returns `409 invalid_state`.

- **Cancel during processing** is accepted only until file placement (§4.3, step 3) starts. After that, cancel returns `409 invalid_state` and the job completes normally, so the library never holds a half-placed job.
- **Retry after cancel** starts the download from scratch, because cancel deletes the work directory. To stop a job and keep its partial files, pause it.

### 2.5 Queue order

Pending jobs are dispatched in ascending `queued_at` order. `queued_at` is set when a job is created, resumed or retried, so those jobs go to the back of the queue. A batch's episodes are queued in ascending episode order.

---

## 3. Configuration

The server reads deployment settings from one YAML file. Its path is given by `--config <path>` or `FETCHARR_CONFIG`, and defaults to `./config.yaml`. Host/port, authentication, log level, the web UI switch, data directory, `ytdlp.binary`, `downloads.pixeldrain_api_key`, and `downloads.extra_args` remain deployment-owned and require a restart.

The remaining settings are managed through Android or `/api/system/settings`, persisted in SQLite, and applied live. Existing YAML values are imported once when the database has no settings document; after that the database is authoritative for editable values. The full legacy example below remains valid for that initial import. New installations can use `server/config.minimal.yaml` and add categories in Android.

```yaml
server:
  host: 0.0.0.0
  port: 8080
  auth_token: ""            # empty = no auth; otherwise required as a Bearer token
  log_level: info           # debug | info | warn | error
  web_ui: true              # serve the browser UI at / (false = API only)

data_dir: /data             # SQLite DB, per-job work dirs, managed yt-dlp binary

downloads:
  concurrency: 2            # simultaneous downloads (≥1)
  merge_format: mp4         # container used when yt-dlp merges separate video/audio streams
  min_free_space: 5GB       # disk-space guard; 0 disables
  pixeldrain_api_key: ""    # HTTP Basic auth for pixeldrain.com links; empty = anonymous
  extra_args: []            # extra yt-dlp arguments appended to every run

ytdlp:
  binary: ""                # empty = server-managed binary in data_dir (auto-updatable)
  auto_update: true         # managed binary only: check on startup and every 24h

history:
  retention_days: 0         # auto-delete finished jobs older than N days; 0 = keep forever

categories:
  - id: movies
    label: Movies
    path: /media/movies
    layout: movie
  - id: tv
    label: TV Shows
    path: /media/tv
    layout: tv
  - id: asian_drama
    label: Asian Drama
    path: /media/asian_drama
    layout: tv
  - id: misc
    label: Misc
    path: /media/misc
    layout: flat

media_servers:              # optional; notified after each completed job
  - type: jellyfin
    url: http://jellyfin:8096
    api_key: "…"
    path_map: {}            # optional prefix rewrite, e.g. {"/media": "/data/media"}
  - type: plex
    url: http://plex:32400
    token: "…"
    path_map: {}
```

### Startup validation

The server refuses to start, logging a clear error, when any of these checks fail:

- Zero categories is a valid setup state; `/api/info` reports `setup_required: true`, and submissions are rejected until a category exists. Category `id`s must be unique and match `^[a-z0-9_]+$`, and `layout` must be `movie`, `tv` or `flat`.
- Every category `path` must exist and be writable.
- `data_dir` must be writable. Its subfolders are created if missing.
- `aria2c`, `ffmpeg` and `ffprobe` must be on `PATH`.
- yt-dlp must be usable. If `ytdlp.binary` is empty and no managed binary exists yet, the server downloads one first (see §8).
- `min_free_space` accepts byte sizes such as `500MB`, `5GB` or `0`.

---

## 4. Download Pipeline

Each worker takes the next pending job and runs these steps in order.

### 4.1 Pre-checks (still `pending` → `downloading`)

1. **Disk space.** If free space on the category's filesystem is below `min_free_space`, the job fails with `low_disk_space`.
2. **Existing file.** The server computes the target path (§5). If a video file with the same base name already exists in the target folder (any extension), the job fails with `already_exists`. A job created or retried with `overwrite: true` skips this check and replaces the file. Create requests run the same check up front (§6.3), so this step only fires when the file appeared after the job was created, for example from another job with a different URL.

### 4.2 Download (`downloading`)

yt-dlp runs as a subprocess in its own process group. It writes into the job's work directory, `<data_dir>/work/<job_id>/`. The invocation must:

- write to `<work_dir>/media.%(ext)s`;
- pick the best video plus best audio, falling back to the best single format. When streams are merged, use `merge_format`. Single-file sources keep their original container;
- normalize a detected `matroska` extension to `mkv` after format selection, before filename creation, while keeping yt-dlp's extension safety checks enabled;
- use aria2c for direct downloads (`--downloader aria2c`), keeping HLS and DASH on yt-dlp's native downloader (`--downloader dash,m3u8:native`). Deployment `extra_args` can override this with `--downloader native` or tune connections with `--downloader-args "aria2c:-x8 -s8"`;
- disable playlist expansion;
- emit one JSON progress line per update on stdout (`--newline`, `--progress-template`);
- print the final file path after post-processing (`--print after_move:filepath`). If nothing is printed, the server falls back to the largest media file in the work directory;
- pass each request header with `--add-header`;
- keep partial files (no `--no-part`), so a resume continues where the download stopped;
- append `extra_args`, then `--`, then the URL. The `--` stops a URL from being read as an option.

**Progress.** aria2c transfers currently emit structured progress only at completion, so live percent, speed and ETA can remain unavailable during those transfers. Native HLS/DASH downloads retain live progress. Parse `downloaded_bytes`, `total_bytes` (or `total_bytes_estimate`), `speed` and `eta`. Store them on the job and broadcast at most one update per second per job.

**Failure.** If yt-dlp exits non-zero, the job fails with `download_failed`. `error_message` holds the last `ERROR:` lines from stderr, with header values redacted.

### 4.3 Processing (`processing`)

1. **Subtitles.** Download each subtitle URL using the job's headers. Anything that isn't SRT is converted to SRT with ffmpeg. A subtitle that fails to download or convert does not fail the job; it adds an entry to `warnings`.
2. **Existing file, again.** Repeat the check from §4.1, because another job may have created the file during the download.
3. **Place files.** Move the media file and subtitles to their final paths (§5), creating folders as needed. If the work directory and library are on different filesystems, copy to `<target>.partial` and then rename, so the library never contains a half-written file. Once placement starts, the job can no longer be cancelled (§2.4).
4. **Clean up.** Delete the work directory.
5. **Complete.** Set `status=completed`, `progress.percent=100` and `file_path`.
6. **Notify media servers** (§4.5). This runs asynchronously and never affects the job's status.

Any error in steps 2–4 fails the job with `processing_failed` (or `already_exists`). The work directory is kept so a retry can reuse it.

### 4.4 Pause, cancel, shutdown

- **Pause.** Kill the yt-dlp process group, keep the work directory, and set `paused`. The worker slot is freed. Resume sets the job back to `pending`; yt-dlp later continues from the partial files.
- **Cancel.** Kill the process group (yt-dlp or ffmpeg), delete the work directory, and set `cancelled`. Not allowed once placement has started (§2.4). A later retry starts from scratch.
- **Graceful shutdown (SIGINT/SIGTERM).** Stop accepting requests, kill all running processes, set `downloading` and `processing` jobs back to `pending`, close WebSocket connections, and exit.
- **Startup recovery.** Any job left in `downloading` or `processing` after a crash is set back to `pending` and keeps its work directory. Paused jobs stay paused.

### 4.5 Media server refresh

When a job completes, queue a refresh for its folder (the show folder for `tv`, the movie folder for `movie`, the category root for `flat`). Refreshes are debounced for 30 seconds per folder, so a whole batch triggers one scan. `path_map` rewrites path prefixes when the media server sees the library under a different path.

- **Jellyfin.** `POST {url}/Library/Media/Updated` with the folder path, authenticated with `api_key`.
- **Plex.** Find the library section whose location contains the folder, then call `GET {url}/library/sections/{id}/refresh?path=<folder>` with `X-Plex-Token`.

Failures are logged and otherwise ignored.

### 4.6 History pruning

If `history.retention_days > 0`, the server deletes `completed`, `failed` and `cancelled` jobs whose `finished_at` is older than the limit. This runs on startup and then hourly. It removes records only, never files.

---

## 5. Library Layout & Naming

Fixed Plex/Jellyfin conventions; not configurable. `<ext>` is whatever container yt-dlp produced.

| Layout | Path |
| --- | --- |
| `movie` | `<root>/<Title> (<Year>)/<Title> (<Year>).<ext>` |
| `tv` | `<root>/<Show> (<Year>)/Season <SS>/<Show> - S<SS>E<EE> - <Episode Title>.<ext>` |
| `flat` | `<root>/<Title>.<ext>` |

Rules:

- ` (<Year>)` is left out when `year` is unknown. For `tv`, `year` is the show's first-air year.
- ` - <Episode Title>` is left out when the episode `title` is empty.
- Season and episode numbers are zero-padded to two digits, or three if ≥100. Season `0` is valid and maps to `Season 00` (specials).
- Subtitles use the media file's name plus `.<lang>.srt`. A second subtitle with the same language gets `.<lang>.2.srt`, and so on. A missing `lang` becomes `und`.
- Sanitization applies to every name segment. Remove `< > : " / \ | ? *` and control characters, collapse whitespace, trim spaces and trailing dots, and cap the segment at 180 bytes without splitting a UTF-8 character. A request whose required name becomes empty after sanitizing is rejected with `400`.
- The resolved path must stay inside the category root after cleaning and symlink resolution. Otherwise the job fails with `processing_failed`.

Example:

```
/media/movies/Dune Part Two (2024)/Dune Part Two (2024).mp4
/media/tv/Severance (2022)/Season 02/Severance - S02E01 - Hello, Ms. Cobel.mp4
/media/tv/Severance (2022)/Season 02/Severance - S02E01 - Hello, Ms. Cobel.en.srt
/media/misc/Conference Talk.webm
```

---

## 6. HTTP API

### 6.1 Conventions

- **Base URL:** `http://<host>:<port>/api`. Bodies are JSON (`application/json`).
- **Auth:** when `server.auth_token` is set, every endpoint except `/api/health`, `/api/session` and the signed media stream requires `Authorization: Bearer <token>` or a browser session cookie (see `POST /api/session`). The WebSocket upgrade request also needs one of them. A missing or wrong credential returns `401 unauthorized`; a wrong Bearer header is rejected even when a valid cookie is present. Tokens are compared in constant time. The media endpoint requires its job-scoped playback signature, even when API authentication is disabled.
- **Cross-origin requests:** unsafe methods (`POST`, `PUT`, `DELETE`) sent by a browser from another origin are rejected with `403 forbidden`, detected with `Sec-Fetch-Site` or by comparing `Origin` with `Host`. Clients that send neither header, such as Android, are unaffected. There is no CORS support.
- **Times:** RFC 3339 in UTC with millisecond precision, e.g. `2026-10-14T14:30:00.123Z`. The precision keeps `finished_after` catch-up from skipping jobs that finish in the same second.
- **Errors:** every non-2xx response has this shape:

```json
{ "error": { "code": "invalid_request", "message": "category 'foo' does not exist" } }
```

| HTTP | `code` | When |
| --- | --- | --- |
| 400 | `invalid_request` | Validation failed |
| 401 | `unauthorized` | Missing or wrong token |
| 403 | `forbidden` | Cross-origin browser request to an unsafe method |
| 404 | `not_found` | Job or batch not found |
| 409 | `invalid_state` | Action not allowed in the current status |
| 409 | `duplicate` | The same URL is already `pending`, `downloading`, `processing` or `paused` |
| 409 | `already_exists` | Create without `overwrite`: the library already has a video file at the target (§4.1) |
| 409 | `ytdlp_unmanaged` | Update requested but `ytdlp.binary` is set |
| 409 | `ytdlp_busy` | Update requested while a job is `downloading` |
| 500 | `internal` | Unexpected server error |

### 6.2 Job object

Returned by every endpoint that returns jobs. **Request headers are never included.**

```json
{
  "id": "0f6c1a6e-6f5e-4a8a-9a0e-2b7f8f4f1c11",
  "batch_id": "b1d3…",
  "category": "tv",
  "url": "https://cdn.example/sev/s02e01.m3u8",
  "title": "Hello, Ms. Cobel",
  "show_name": "Severance",
  "season": 2,
  "episode": 1,
  "year": 2022,
  "subtitles": [{ "url": "https://cdn.example/sev/s02e01.en.vtt", "lang": "en" }],
  "status": "downloading",
  "progress": {
    "percent": 42.7,
    "downloaded_bytes": 325058560,
    "total_bytes": 754974720,
    "speed_bps": 3250585,
    "eta_seconds": 132
  },
  "file_path": null,
  "error_code": null,
  "error_message": null,
  "warnings": [],
  "created_at": "2026-10-14T14:30:00.000Z",
  "queued_at": "2026-10-14T14:30:00.000Z",
  "started_at": "2026-10-14T14:30:05.412Z",
  "finished_at": null
}
```

- `batch_id`, `show_name`, `season`, `episode` and `year` are `null` when they don't apply.
- `progress` is `null` until the first update arrives. A paused job keeps its last progress. Any progress field can be `null` if yt-dlp doesn't report it (some live HLS streams have no total size).
- `error_code` is one of `low_disk_space`, `already_exists`, `download_failed` or `processing_failed`.
- `finished_at` is set when the job reaches `completed`, `failed` or `cancelled`.

### 6.3 Endpoints

#### `GET /api/health`
No auth. Returns `200 {"status":"ok"}`. For container health checks.

#### `POST /api/session` · `DELETE /api/session`
Browser sign-in for the web UI; browsers cannot send a Bearer header on a WebSocket upgrade. `POST` takes `{"token": "<auth_token>"}` and returns `204` with an `HttpOnly`, `SameSite=Strict` cookie `fetcharr_session` (30 days; `Secure` when the request arrived over HTTPS or with `X-Forwarded-Proto: https`). The cookie has no `Path` attribute, so it is scoped to the API directory, including behind a reverse-proxy prefix. A wrong token returns `401 unauthorized`. Without a configured `auth_token` the call returns `204` and sets no cookie.

The cookie holds an expiry and an HMAC keyed by the token. It survives restarts and becomes invalid when the token changes or it expires. It never contains the token and is never logged. `DELETE` clears it and returns `204`; it needs no valid session.

#### `GET /api/info`
Returns server details and the category list. Clients use this for "Test connection" and to build category pickers.

```json
{
  "version": "1.0.0",
  "ytdlp_version": "2026.09.30",
  "ytdlp_managed": true,
  "concurrency": 2,
  "auth_required": true,
  "settings_supported": true,
  "setup_required": false,
  "categories": [
    { "id": "movies", "label": "Movies", "layout": "movie", "path": "/media/movies", "free_bytes": 812345678901 },
    { "id": "tv", "label": "TV Shows", "layout": "tv", "path": "/media/tv", "free_bytes": 812345678901 }
  ]
}
```

#### `GET /api/system/settings` and `PUT /api/system/settings`

These endpoints use the same Bearer authentication as job operations. GET returns only editable settings, in this shape:

```json
{
  "revision": 1,
  "downloads": {"concurrency": 2, "merge_format": "mp4", "min_free_space": 5000000000},
  "history": {"retention_days": 0},
  "ytdlp": {"auto_update": true},
  "categories": [{"id": "movies", "label": "Movies", "path": "/media/movies", "layout": "movie"}],
  "media_servers": [{"id": "38b5c036-004d-4f3b-891b-850ea3d72a94", "type": "jellyfin", "url": "http://jellyfin:8096", "path_map": {}, "has_credential": true}]
}
```

PUT replaces the complete editable document and requires the revision returned by GET. Arrays must be present, including `[]` for an empty list. `min_free_space` is an integer byte count. Retention `0` keeps history forever; minimum space `0` disables the disk guard. Deployment fields and unknown keys are rejected with `400 invalid_request`. Success returns the normalized document with an incremented revision. A stale revision returns `409 settings_conflict`; the client must reload before saving again. Validation or database failures leave the previous settings active.

Integration IDs are stable; clients assign a unique nonempty ID when adding a connection. `credential` is an optional write-only field: omit it to retain a saved credential for the same ID and type, or supply a nonempty replacement (the Jellyfin API key or Plex token). New connections require a credential. GET and successful PUT responses omit credentials and report `has_credential` instead. Removing a connection removes its stored credential. `path_map` rewrites absolute Fetcharr prefixes to media-server paths, including Windows destinations.

Category IDs are fixed in Android after creation. Destination paths must be absolute server/container paths and writable. Saving a new category creates its missing destination folder and parent folders; creation or permission failures return `400 invalid_request`. Changing an existing category's path requires an existing writable folder. Removing a category or changing its path/layout returns `409 category_busy` if pending, paused, downloading, or processing jobs reference it. This check is coordinated with submission and retry. Labels can change at any time. Historical jobs retain their original destination root for safe file deletion; unknown legacy roots cannot be guessed. Retry revalidates against the current category and records the current root.

Live updates never kill existing downloads. Decreased concurrency takes effect as workers finish; increased concurrency wakes dispatch. Download arguments and free-space checks use a snapshot for each new attempt. Retention changes wake the pruner and remove records only. Auto-update can be toggled only for the managed binary; enabling it schedules an immediate safe check. Pending integration refreshes use the current connections at dispatch time. No filesystem browser or folder-creation API is provided.

The optional `/api/info` capability fields default to false on older servers. `settings_supported` indicates this API exists; `setup_required` means the category list is empty. Existing job responses and WebSocket events retain their shape.

#### `POST /api/jobs`
Creates one job. Returns `201` with the Job object.

```json
{
  "url": "https://cdn.example/dune2.m3u8",
  "category": "movies",
  "title": "Dune Part Two",
  "year": 2024,
  "headers": { "Referer": "https://example.com/" },
  "subtitles": [{ "url": "https://cdn.example/dune2.en.vtt", "lang": "en" }]
}
```

Validation:

- `url` must be `http` or `https`.
- `category` must exist.
- The layout's required fields (§2.1) must be present and non-empty after sanitizing.
- `season` must be ≥0 and `episode` ≥0 when given. `year` must be a 4-digit number when given.
- Each subtitle `url` must be `http` or `https`.
- If the same `url` is already active, return `409 duplicate`.
- Unless `overwrite` is `true`, if the target (§5) already has a video file with the same base name (any extension), return `409 already_exists`. The message names the existing file. Resubmit with `"overwrite": true` to replace it; the job then skips the §4.1 existing-file check.

#### `POST /api/jobs/batch`
Creates the episodes of one season as a batch. The category must have the `tv` layout. Episode-level `headers` are merged over batch-level `headers`.

```json
{
  "category": "tv",
  "show_name": "Severance",
  "season": 2,
  "year": 2022,
  "headers": { "Referer": "https://example.com/" },
  "episodes": [
    {
      "episode": 1,
      "title": "Hello, Ms. Cobel",
      "url": "https://cdn.example/sev/s02e01.m3u8",
      "subtitles": [{ "url": "https://cdn.example/sev/s02e01.en.vtt", "lang": "en" }]
    },
    { "episode": 2, "title": "Goodbye, Mrs. Selvig", "url": "https://cdn.example/sev/s02e02.m3u8", "headers": { "Cookie": "…" } }
  ]
}
```

Returns `201`:

```json
{
  "batch_id": "b1d3…",
  "jobs": [ /* Job objects, ascending episode order */ ],
  "skipped": [ { "episode": 2, "reason": "duplicate" } ]
}
```

If any episode fails validation, the whole request is rejected with `400` and nothing is created. Episodes are skipped and listed in `skipped` when their URL is already active (`reason: "duplicate"`) or, unless the batch has `"overwrite": true`, when their target already has a video file (`reason: "already_exists"`, same check as `POST /api/jobs`). If every episode is skipped, the response is `409 already_exists` when at least one of them is in the library, otherwise `409 duplicate`. `overwrite` applies to every episode of the request; to replace only some, send a batch with just those episodes.

#### `GET /api/jobs`
Lists jobs, newest `created_at` first.

| Query | Description |
| --- | --- |
| `status` | Comma-separated statuses |
| `category` | Category id |
| `batch_id` | Jobs of one batch |
| `finished_after` | RFC 3339 time; only jobs with a later `finished_at` |
| `limit` | Default 50, max 500 |
| `offset` | Default 0 |

Response: `{ "jobs": [ … ], "total": 123 }`.

#### `GET /api/jobs/{id}`
Returns one Job, or `404`.

#### `POST /api/jobs/{id}/playback`
Requires normal API authentication. Checks that the job is completed and its saved media file is available, then returns `200`:

```json
{
  "url": "jobs/<id>/media?expires=<unix-seconds>&signature=<signature>",
  "mime_type": "video/mp4",
  "expires_at": "2026-10-17T00:00:00.000Z"
}
```

Resolve `url` relative to the configured API base (`<server>/api/`), preserving any reverse-proxy prefix. The signed link authorizes only this job's media for 24 hours and never contains the API token. A server restart invalidates existing links. Responses are not cached. Returns `409 invalid_state` for unfinished jobs or a missing file path, `404 not_found` for missing jobs or unavailable files, and `400 invalid_request` for an unknown original destination or an out-of-root path.

#### `GET /api/jobs/{id}/media` · `HEAD /api/jobs/{id}/media`
Requires the `expires` and `signature` query parameters issued by the playback endpoint; missing, modified or expired links return `401 unauthorized`. No Bearer header is needed by the external player. Streams the completed file directly, with `Content-Type`, `Content-Length`, `Accept-Ranges: bytes`, and standard range responses (`206` / `416`) for seeking. HEAD returns headers only. There is no transcoding. Each request rechecks the job and file, confined to the job's saved original destination using symlink-safe filesystem access. Removing the history record makes its link unavailable, even if the file is kept. Query strings are excluded from request logs.

#### `POST /api/jobs/{id}/pause` · `/resume` · `/cancel`
Apply the transition (§2.4). Return `200` with the updated Job.

#### `POST /api/jobs/{id}/retry`
Requeues a `failed` or `cancelled` job with the same URL, headers and subtitles. A cancelled job starts from scratch, because its work directory was deleted. A failed job reuses whatever its work directory still holds. It clears `error_*`, `warnings` and `progress`. Optional body: `{ "overwrite": true }` replaces an existing library file (see `already_exists`). Returns `200` with the Job.

**Replace source and retry.** The optional body also accepts `source`:

```json
{"overwrite": false, "source": {"url": "https://media.example/fresh.m3u8", "headers_mode": "clear", "subtitles": []}}
```

`source` is a complete replacement for one failed or cancelled job. Its HTTP(S)
`url`, `headers_mode` and `subtitles` array are required. `headers_mode` is `clear`
(discard saved headers), `keep` (reuse private saved headers), or `replace`
(require a `headers` object replacing the saved map). Only `replace` accepts
`headers`. Subtitle entries use creation validation and language normalization;
`[]` removes all subtitles. Headers remain absent from responses, events and logs.

Source replacement preserves job/batch identity, creation time and naming metadata,
revalidates the current category/destination, and queues at the end. It atomically
updates source fields, resets retry state and marks partial files for deletion.
The runner must successfully delete the old work directory before downloading;
this requirement survives restart, cleanup failure and subsequent ordinary retries.
Cleanup failure uses `download_failed`. Library destination files are untouched
unless the usual explicit overwrite option is used during placement.

Invalid input returns `400 invalid_request`, unknown jobs `404 not_found`,
ineligible states `409 invalid_state`, and an already-active replacement URL
`409 duplicate`. Rejected requests leave the previous source and partial files
intact. Batch retry rejects `source` with `400 invalid_request`. Omitting `source`
retains ordinary retry behavior, including partial-file reuse.

`GET /api/info` advertises `source_replacement_supported: true`; clients treat a
missing capability as false. Deploy the updated server before enabling the Android
feature. Responses retain the existing Job shape with updated public source fields.

#### `DELETE /api/jobs/{id}`
Removes a finished job's record. Returns `204`.

- `?delete_file=true` also deletes the media file and its subtitle sidecars, then removes any folders this leaves empty, up to but not including the category root. This applies only to `completed` jobs; it is ignored otherwise.
- Returns `409 invalid_state` if the job is still active.

#### `DELETE /api/jobs`
Clears history. Deletes every finished job matching `?status=` (default `completed,failed,cancelled`; active statuses are rejected with `400`). Never deletes files. Returns `200 {"deleted": 42}`.

#### `POST /api/batches/{batch_id}/pause` · `/resume` · `/cancel` · `/retry`
Applies the action to each job in the batch where the transition is allowed and skips the rest. Returns `200 { "jobs": [ …updated jobs… ] }`, or `404` if the batch doesn't exist.

#### `GET /api/system/ytdlp/check`
Checks the latest stable yt-dlp release without installing it or interrupting downloads. Returns `200 {"current": "2026.09.01", "latest": "2026.09.30", "update_available": true}`. Returns `409 ytdlp_unmanaged` when `ytdlp.binary` is set. Both versions must be available for a successful response.

#### `POST /api/system/ytdlp/update`
Updates the managed yt-dlp binary right away. Returns `200 {"previous": "2026.09.01", "current": "2026.09.30"}`. Returns `409 ytdlp_unmanaged` when `ytdlp.binary` is set, and `409 ytdlp_busy` while any job is `downloading`. A manual update never waits; the client tries again later.

#### `GET /api/ws`
WebSocket upgrade. See §7.

---

## 7. WebSocket

`GET /api/ws`, authenticated the same way as REST (Bearer or the browser session cookie). The upgrade's `Origin`, when present, must match `Host`; reverse proxies must forward the original `Host` header. Messages go from server to client only; anything a client sends is ignored. Every message is a JSON object with a `type`.

| `type` | Payload | When |
| --- | --- | --- |
| `snapshot` | `jobs`: every job not in `completed`/`failed`/`cancelled` | Immediately after connecting |
| `job_added` | `job` | A job was created |
| `job_updated` | `job` (full object) | Any change: status, progress, error, warnings. Progress-only updates are limited to 1/s per job; status changes are sent immediately |
| `job_removed` | `job_ids` (array) | A job was deleted, or history was cleared |

```json
{ "type": "job_updated", "job": { "id": "…", "status": "completed", "file_path": "/media/…", "…": "…" } }
```

The server sends a ping every 30 seconds. A connection that hasn't answered with a pong within 60 seconds is closed. A slow client whose send buffer fills is disconnected; it is expected to reconnect and use the new snapshot.

Clients recover finished jobs they missed while disconnected with `GET /api/jobs?finished_after=<last seen>`.

---

## 8. yt-dlp Management

- **Managed mode** (`ytdlp.binary` empty). The binary lives at `<data_dir>/bin/yt-dlp`. If it is missing at startup, the server downloads the latest release from the official yt-dlp GitHub releases.
- **Auto-update** (`ytdlp.auto_update: true`, managed mode only). The server runs `yt-dlp -U` at startup and every 24 hours. If downloads are running when an update is due, the dispatcher stops starting new jobs, waits until no job is `downloading`, runs the update, and then resumes dispatching. A busy queue can therefore delay an update only by as long as the current downloads take. Jobs in `processing` don't block the update. If the update fails, the error is logged and dispatching resumes.
- **Manual update.** `POST /api/system/ytdlp/update` (§6.3).
- **Unmanaged mode** (`ytdlp.binary` set). The server uses that binary and never updates it.

The current version is shown in `GET /api/info`.

---

## 9. Security & Logging

- Request headers are stored in SQLite so retry can reuse them. They are never returned by the API, sent over WebSocket, or logged. The database file is created with `0600` permissions.
- URLs are logged without their query string.
- The web UI is served with a strict `Content-Security-Policy` (same-origin scripts, API, WebSocket and media only), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and framing disabled. Static UI files need no authentication; every API call does.
- Logs are structured (`log/slog`) and go to stdout. At `info` the server logs startup, job created/started/completed/failed, WebSocket connect/disconnect, yt-dlp updates and media-server refresh failures.
- A panic inside a worker is recovered. That job fails with `processing_failed` and the worker keeps running.

---

## 10. Deployment

### Docker (primary)

The image contains the server binary, `aria2c`, `ffmpeg` and `ffprobe`. yt-dlp is managed in the `/data` volume. The container exposes port `8080` and has a health check on `/api/health`.

```yaml
services:
  fetcharr:
    image: ghcr.io/istarvin/fetcharr-server:latest
    user: "1000:1000"          # must be able to write to /data and the media folders
    ports: ["8080:8080"]
    environment:
      FETCHARR_CONFIG: /config/config.yaml
    volumes:
      - ./config:/config
      - ./data:/data
      - /mnt/media:/media
    restart: unless-stopped
```

### Bare metal (alternative)

Requirements: the `fetcharr` binary, plus `aria2c`, `ffmpeg` and `ffprobe` on `PATH`. Run `fetcharr --config /etc/fetcharr/config.yaml`. For a long-running install, use a systemd unit with `Restart=on-failure` that runs as a user who can write to `data_dir` and every category path.

### Reverse proxy

The web UI uses relative URLs and hash routing, so it works at the root or under a path prefix such as `https://home.example/fetcharr/`. Forward WebSocket upgrades for `/api/ws` and keep the original `Host` header (nginx: `proxy_set_header Host $host;`); otherwise the WebSocket origin check fails. Set `X-Forwarded-Proto: https` when TLS terminates at the proxy so the session cookie is marked `Secure`.
