# Fetcharr contract fixtures

Wire-format fixtures shared by the server and Android tests. Any change here is a contract change (implementation-plan §2). All timestamps are RFC 3339 UTC with millisecond precision. Jobs never contain `headers`.

| File | Represents | Spec |
| --- | --- | --- |
| `server-settings.json`, `update-server-settings.json` | `GET`/`PUT /api/system/settings` (credentials are write-only) | server §6.3 |
| `error-settings_conflict.json`, `error-category_busy.json` | Settings conflict envelopes | server §6.3 |
| `info.json` | `GET /api/info` | server §6.3 |
| `job-<status>.json` (pending, downloading, processing, paused, completed, failed, cancelled) | One Job object per status | server §2.4, §6.2 |
| `jobs-list.json` | `GET /api/jobs` (all 7 jobs, newest `created_at` first) | server §6.3 |
| `batch-create-response.json` | `201` from `POST /api/jobs/batch` (2 created, 1 skipped) | server §6.3 |
| `error-<code>.json` (invalid_request, unauthorized, not_found, invalid_state, duplicate, ytdlp_unmanaged, ytdlp_busy) | Error envelope | server §6.1 |
| `ws-snapshot.json`, `ws-job_added.json`, `ws-job_updated.json`, `ws-job_removed.json` | WebSocket messages | server §7 |
| `ytdlp-check.json` | `GET /api/system/ytdlp/check` | server §6.3 |
| `ytdlp-update.json` | `POST /api/system/ytdlp/update` | server §6.3 |
| `create-job-movie.json`, `create-job-tv.json`, `create-job-flat.json` | `POST /api/jobs` bodies, one per layout | server §2.1, §6.3 |
| `create-batch.json` | `POST /api/jobs/batch` body (episode 3 is the duplicate that gets skipped) | server §6.3 |
| `intent/` | Decoded Android intent payloads, see `intent/README.md` | intent guide |

Ids are shared across fixtures: `create-job-movie` -> `job-processing`, `create-job-tv` -> `job-failed`, `create-job-flat` -> `job-paused`, `create-batch` -> `job-downloading`/`job-pending`/`batch-create-response`. `ws-snapshot` holds the four active jobs; `ws-job_added` is `job-pending`; `ws-job_updated` is `job-completed`; `ws-job_removed` lists the failed and cancelled ids.

## Assumptions (where the spec is silent)

- Tests assert on `error.code`, never on `error.message` wording.
- `warnings` entries are plain strings, e.g. `"subtitle es (<url>): download failed: HTTP 404"`.
- A `tv` job with no title has `"title": null` (non-applicable fields are `null`).
- `pending`: `progress`, `started_at`, `finished_at` are `null`. `processing` and `completed`: `percent` 100, `speed_bps` 0, `eta_seconds` 0. `cancelled` keeps its last progress.
- Episodes of one batch get `created_at`/`queued_at` 1 ms apart so the queue order is observable.
- `create-batch.json` episode 3 is skipped as `duplicate`; server tests must seed an active job with that URL.
- WebSocket `snapshot` and `GET /jobs` list jobs `created_at` descending.
- A subtitle with no `lang` becomes `und` on the server; only a missing subtitle `url` is invalid in an intent.
- Intent rules that can't be expressed as decoded JSON (wrong action/scheme, missing `payload`, bad Base64, bad JSON) are raw-input tests; see `intent/README.md`.

Settings responses include a revision, editable fields, and credential-presence flags only. PUT echoes that revision and may include a replacement `credential`; deployment fields and job destination-root snapshots are never part of this contract. `min_free_space` is an integer byte count. The `/api/info` settings capability flags are optional for older servers and default to false in clients.
