# Fetcharr Android

The Android client for the [Fetcharr server](fetcharr-server.md). It receives download requests from other apps (mainly CloudStream), lets the user confirm them, sends them to the server, and shows live progress and history. The app never downloads media itself.

**Stack:** Kotlin · Jetpack Compose (Material 3) · Hilt · Retrofit/OkHttp · kotlinx.serialization · DataStore
**Package:** `com.istarvin.fetcharr`
**Platform:** minSdk 29 (Android 10) · targetSdk 36 · signed APK on GitHub Releases

Related contracts:

- Server REST API, WebSocket events and the Job object: [server §6–7](fetcharr-server.md#6-http-api)
- Incoming intent format for other apps: [Fetcharr Android Intent Guide](fetcharr-android-intents.md)

---

## 1. Overview

### Responsibilities

- Accept download requests from `fetcharr://` intents, the Android share sheet, and a manual add form.
- Merge a run of single-episode intents for the same show and season into one confirmation.
- Let the user review metadata, pick a category, and choose episodes before submitting.
- Show active jobs live over WebSocket, with pause, resume, cancel and retry.
- Show the download history, with external playback, retry, copy path, delete and clear.
- Post a notification when downloads complete or fail, including while the app is in the background.

### Non-goals

- No downloading, built-in player or local media management.
- No offline queue. If the server is unreachable, a submit fails and the user retries.
- No device-side job database. The server is the source of truth.

---

## 2. App Structure

Single activity with a bottom navigation bar.

| Destination | Type | Purpose |
| --- | --- | --- |
| Queue | Tab (start) | Active jobs |
| History | Tab | Finished jobs |
| Settings | Tab | Server connection and defaults |
| Incoming sheet | Modal bottom sheet over any tab | Confirm a received request |
| Add screen | Full screen (from Queue FAB or the share sheet) | Create a job by hand |

Entry points:

- **Launcher** opens the Queue.
- **`fetcharr://` intent** opens the Incoming sheet (§4.1).
- **Share sheet**: `ACTION_SEND` with `text/plain` opens the Add screen with the first `http(s)` URL in the text filled in.
- **Notification tap** opens the Queue (from a progress notification) or History (from a result notification).

The activity uses `singleTask`, so it receives intents in both `onCreate` and `onNewIntent`.

---

## 3. Server Connection & Sync

### 3.1 Settings needed

The server URL and an optional API token, both stored in DataStore. Until a URL is saved, the Queue shows a "Set up your server" empty state that links to Settings.

Plain `http://` URLs are allowed. The app's network security config permits cleartext traffic, because most servers run on a LAN or VPN without TLS. The token is then sent unencrypted, so a server reachable from the internet should sit behind an HTTPS reverse proxy.

Every REST call and the WebSocket send `Authorization: Bearer <token>` when a token is set. Changing either setting reconnects right away.

### 3.2 State model

- **Active jobs** are held in memory as one list. That list is replaced by each WebSocket `snapshot` and updated by `job_added`, `job_updated` and `job_removed`. Every screen reads from this list.
- **History** comes from REST, `GET /api/jobs?status=completed,failed,cancelled`, paged 50 at a time. When a `job_updated` event moves a job into a finished status, it is added to the top of history if history is loaded.
- **Categories** come from `GET /api/info`. They are fetched on every successful connection and cached in DataStore, so pickers still work when the server is unreachable.
  The cache and default category choices belong to the saved server and are cleared when its URL changes. Testing an unsaved connection does not replace that cache; replies from a previous connection are ignored.
- **Last seen time**: the newest `finished_at` the app has processed, stored in DataStore. It is used to catch up on missed results (§6).

### 3.3 WebSocket lifecycle

- The app connects while it is in the foreground, and while the background service runs (§6).
- After a disconnect it reconnects with exponential backoff: 1 s, doubling, capped at 30 s, reset after a successful connection.
- On each new connection, the `snapshot` replaces the active list, the app calls `GET /api/info`, and it catches up on missed results.
- Connection state is `connected`, `connecting` or `disconnected (retrying in Ns)`. A banner at the top of the Queue shows it whenever the app isn't connected.

### 3.4 Formatting

The server sends raw numbers; the app formats them. Bytes use binary units (`MiB`, `GiB`), speed as `3.1 MiB/s`, ETA as `h:mm:ss` or `m:ss`. A missing value is hidden, not shown as zero. Progress bars animate between updates.

---

## 4. Receiving Downloads

### 4.1 `fetcharr://` intents

The payload format and its validation rules are in the [Intent Guide](fetcharr-android-intents.md). If a payload can't be read, the app opens normally and shows the snackbar "Couldn't read the download request". The payload is never logged, because it may contain headers.

### 4.2 Category preselection

The intent's `category` is a hint.

1. If it matches a server category `id`, that category is preselected.
2. Otherwise, if the request has a season and episode (or is a batch), the **Default TV category** setting is used.
3. Otherwise, the **Default movie category** setting is used.
4. If none of these applies, the first category whose layout fits is used.

The user can always change the category in the sheet.

### 4.3 Season aggregation

CloudStream may send a season as many `single` intents in a row, one per episode. The app merges them as follows.

- An *episodic* request is a `single` with `showName`, `season` and `episode`, or a `batch`.
- If the Incoming sheet is open for the same show and season (show name compared case-insensitively), the new episodes are **added to that sheet**. An episode number that is already listed is replaced. New episodes start selected.
- If the sheet is open for something else, the new request is **queued**, and the sheet shows "1 more request waiting". Queued requests open one after another once the current sheet closes.
- Each episode keeps its own headers and subtitles from its intent.
- While episodes are still arriving (any arrival in the last 2 s), the sheet shows "Receiving episodes…" and the Download button is disabled.

Merged requests are kept in memory only. If the process dies while a sheet is open, the request is lost and the user sends it again.

---

## 5. Screens

### 5.1 Incoming sheet

Modal bottom sheet. Swiping it down or tapping **Cancel** discards the request.

**Single item** (a movie, one episode, or a flat item):

- Category chips, from the server categories.
- Editable fields. The visible fields follow the selected category's layout:
  - `movie`: Title, Year
  - `tv`: Show, Season, Episode, Episode title, Year
  - `flat`: Title
- A read-only line showing the host of the source URL and a subtitle count (for example "2 subtitles: en, es").

**Season** (two or more episodes):

- Category chips, limited to `tv`-layout categories.
- Editable Show, Season and Year.
- An episode checklist: a checkbox, `E01` and the episode title on each row, with a select-all checkbox in the header showing "8 of 10 selected".

**Footer:** **Cancel** and **Download (N)**. Download is disabled when nothing is selected, a field the layout requires is empty, or episodes are still arriving.

**Submitting:**

- One item is sent with `POST /api/jobs`. Two or more selected episodes go to `POST /api/jobs/batch`.
- On success, the sheet closes and shows the snackbar "Added N downloads". If some episodes were skipped as duplicates, the snackbar says "Added 6 · 2 already queued".
- On `409 duplicate` for a single item, the snackbar reads "Already in the queue" and the sheet closes.
- Any other error is shown inside the sheet, and the sheet stays open so the user can retry.
- If no server is configured, the sheet shows "Server not set up" with a button to Settings. The request stays in memory and the sheet reopens afterwards.

### 5.2 Add screen

The same fields as a single-item Incoming sheet, plus:

- **URL** (required, `http`/`https`).
- **Headers**: optional key/value rows.
- **Subtitles**: optional URL and language rows.

It is opened from the Queue FAB, with empty fields, or from the share sheet, with the URL filled in. It submits with `POST /api/jobs`.

### 5.3 Queue

Shows every active job: `pending`, `downloading`, `processing` and `paused`.

- **Sections:** *Active* (downloading and processing), *Queued* (pending, in `queued_at` order) and *Paused*. Empty sections are hidden.
- **Batch grouping:** jobs with the same `batch_id` are shown as one expandable group card per section, titled "Severance — Season 2". The collapsed card shows the episode count, combined progress, and a menu with Pause all, Resume all and Cancel all, which call the batch endpoints. Expanded, it lists the episode cards.
- **Pull to refresh** reconnects the WebSocket and reloads categories.
- **FAB** opens the Add screen.
- **Empty state:** "Nothing downloading".
- The connection banner (§3.3) is shown when the app isn't connected.

**Job card**

```
┌─────────────────────────────────────────────────┐
│ [TV Shows]                        [Downloading] │
│ Severance — S02E03                              │
│ Who Is Alive?                                   │
│ ████████████████░░░░░░░  64%                    │
│ 3.1 MiB/s · 1:52 left · 463 MiB / 720 MiB       │
│                              [Pause] [Cancel]   │
└─────────────────────────────────────────────────┘
```

The heading is `Show — SxxEyy` for episodes and `Title (Year)` for movies. The second line shows the episode title when there is one.

| Status | Shows | Actions |
| --- | --- | --- |
| `pending` | "Queued" | Pause, Cancel |
| `downloading` | Progress bar, speed, ETA, size | Pause, Cancel |
| `processing` | Indeterminate bar, "Finishing…" | Cancel |
| `paused` | Last progress, "Paused" | Resume, Cancel |

Cancel asks for confirmation.

### 5.4 History

Shows `completed`, `failed` and `cancelled` jobs, newest first, grouped by date (Today, Yesterday, then the date). It loads more pages as the user scrolls.

| Status | Shows | Actions |
| --- | --- | --- |
| `completed` | Final file path; a warning icon if `warnings` isn't empty | Play, Copy path, Delete |
| `failed` | A short error for `error_code`, with `error_message` behind "Details" | Retry, Delete |
| `cancelled` | "Cancelled" | Retry, Delete |

- Actions open from a long-press menu. Swipe-to-dismiss also triggers Delete.
- **Play** also appears as a button on completed rows with a file path. It requests a temporary stream link from `POST /api/jobs/{id}/playback`, then opens an external player with Android `ACTION_VIEW` and the media MIME type. Android uses the default player or offers a choice. Fetcharr shows a message if no player is installed or the file is unavailable. The player streams the saved server file with support for seeking; Fetcharr does not download a local copy. The API token is never passed to the player. Links expire after 24 hours or a server restart; tap Play again to renew one. This requires a server with the playback endpoints.
- **Delete** asks for confirmation. For completed jobs the dialog has an "Also delete file from disk" checkbox (off by default), which maps to `DELETE /api/jobs/{id}?delete_file=true`.
- **Retry** on `already_exists` asks "A file with this name already exists. Replace it?" and then sends `{"overwrite": true}`. Other failures retry straight away.
- Error text by `error_code`:
  - `low_disk_space`: "Not enough disk space on server"
  - `already_exists`: "File already exists in library"
  - `download_failed`: "Download failed"
  - `processing_failed`: "Couldn't save file"
- The overflow menu has **Clear history**, with a confirmation that says files are kept. It calls `DELETE /api/jobs`.
- **Empty state:** "No finished downloads".

### 5.5 Settings

```
Server
  Server URL              http://192.168.1.50:8080
  API token               ••••••••   (optional)
  Test connection         → "Connected · v1.0.0 · 12 ms" or the error
  Server settings         → live server configuration
Defaults
  Default movie category  [movie/flat-layout categories]
  Default TV category     [tv-layout categories]
yt-dlp
  Version                 2026.09.30
  Update now              (hidden when the server's yt-dlp is unmanaged)
About
  App version · Server version · Source code link
```

- **Test connection** calls `GET /api/info` and reports one of: connected (with version and latency), "Can't reach server", "Wrong API token" (`401`), or "Not a Fetcharr server" (the response doesn't parse).
- A URL entered without a scheme gets `http://` added. A trailing slash is removed.
- **Update now** calls `POST /api/system/ytdlp/update` and shows the new version. On `409 ytdlp_busy` it shows "Downloads are running — try again when they finish".

**Server settings** opens a separate editor for download concurrency, merge format, minimum free space (decimal GB), history retention, managed yt-dlp auto-update, categories, and Jellyfin/Plex connections with optional path mappings. Save applies the whole document through `PUT /api/system/settings`. Category IDs stay fixed after creation; users enter server/container destination paths, and saving a new category creates missing folders, including parents. Destinations must be writable; changing an existing category's path requires an existing folder. Docker mounts are configured on the host. Saved credentials are never displayed; blank fields retain them and typed values replace them.

The editor loads from the saved connection, keeps drafts and secrets in memory only, and discards them when that connection changes. Back and Reload prompt before discarding unsaved edits. Errors preserve the draft; stale revisions require a reload. Successful saves refresh categories and clear defaults that no longer match the available layouts. A server without `settings_supported` shows an upgrade message and receives no settings request. Connection settings, default categories, and the existing manual yt-dlp update remain available as before.

---

## 6. Notifications & Background

### Channels

| Channel | Importance | Used for |
| --- | --- | --- |
| `active` | Low, silent | Foreground service notification |
| `results` | Default | Completed and failed results |

`POST_NOTIFICATIONS` is requested the first time the user submits a download.

### Foreground service

- It runs while the server has at least one `pending`, `downloading` or `processing` job. Paused jobs don't count.
- It starts after a successful submit, or when a sync finds active jobs. It stops as soon as no active jobs remain.
- It keeps the WebSocket connected, so the app gets live updates while in the background.
- **Notification:** One silent, expandable live-progress notification. The compact native header prioritizes percentage, speed, and single-download ETA, followed by the media title or job counts: Android's compact progress template replaces the body text with the progress bar. Expanded, a single active download uses its full movie or episode title, with status and queued count in the subtext; multiple active jobs use downloading/finishing/queued counts and omit a combined ETA. Expand to see transferred bytes and up to two active jobs (downloading first, then processing), each with a bounded title and available metrics, followed by remaining active/queued counts. Partial aggregate sizes and speeds are labelled; missing or invalid metrics are hidden. Processing/queued jobs do not show old transfer metrics. While reconnecting or awaiting a fresh snapshot, the notification shows that state and hides stale transfer metrics with indeterminate progress. Identical presentation updates are deduplicated. Tapping it opens the Queue.
- **Service type:** `dataSync`. If the system ends the service early (Android 15's dataSync time limit) or the server has been unreachable for 15 minutes, the service stops and posts "Stopped tracking downloads — open Fetcharr to resume" on `results`.

### Result notifications

| Event | Notification |
| --- | --- |
| Job without a batch completes | "Downloaded · Dune Part Two (2024)" |
| Job without a batch fails | "Failed · Dune Part Two (2024) — Download failed", with a **Retry** action |
| Every job in a batch has finished | One summary, e.g. "Severance S02 — 8 downloaded, 1 failed" |

- On first run, *last seen* is set to the current time, so older history never triggers notifications.
- **Catch-up:** after reconnecting, the app fetches `GET /api/jobs?finished_after=<last seen>` and posts results for anything it missed. It then advances *last seen*.
- No result notification is posted when the user cancels a job. A batch summary counts only downloaded and failed episodes, and is not posted if every episode was cancelled. An episode retried after its batch summary gets its own per-job notification.

---

## 7. Error Handling

| Situation | UI |
| --- | --- |
| Server unreachable or timed out (10 s connect, 30 s read) | Connection banner; actions show the snackbar "Can't reach server" |
| `401` | Snackbar "Wrong API token" with a Settings action |
| `400` | The server's `message`, shown inline on the form or sheet |
| `409 invalid_state` | Refresh that job silently; snackbar "Job already changed" |
| `5xx` | Snackbar "Server error — try again" |
| Bad intent payload | Snackbar "Couldn't read the download request" |

Request headers are kept only in memory for an open Incoming sheet or Add screen. They are never written to disk, shown in the UI after submission, or logged.
