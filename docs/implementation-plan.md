# Fetcharr Implementation Plan

A phased plan for building the [server](fetcharr-server.md) and the [Android app](fetcharr-android.md) in parallel. The two tracks share one contract (Phase 0). After that, each track runs as its own Claude agent session, with five integration milestones where they meet.

---

## 1. How the tracks stay independent

The server and the app only talk through the REST API, the WebSocket and the Job object ([server §6–7](fetcharr-server.md#6-http-api)). If both sides test against the same example payloads, neither has to wait for the other.

- **One repo, one worktree per track.** `server/`, `android/`, `contract/` and `docs/` live in this repo. The server agent works only in `server/` and the Android agent only in `android/`, each in its own git worktree and branch. Both read `contract/` and `docs/`, but neither changes them on its own. A fixture or spec change is a separate commit on `main` that both tracks then rebase onto.
- **Phases are agent briefs.** Each phase below (S1, A1, …) is handed to its track's agent as one task, and its **Done when** line is the acceptance check. The agent works against the specs and `contract/`, not against the other track's code.
- **Contract fixtures.** A top-level `contract/` folder holds JSON examples of every response, WebSocket message and intent payload. The server checks that it produces these shapes (golden tests). The app checks that it can parse them (MockWebServer and unit tests).
- **Fake yt-dlp.** `server/testdata/fake-ytdlp` is a small script that prints realistic progress lines and writes a dummy `media.mp4`. Point `ytdlp.binary` at it (the spec's unmanaged mode, so the spec doesn't change). The Android track then gets a real server with live progress, pause and cancel, without downloading anything. Server tests use it too.
- **App before server.** Until a server endpoint exists, the app runs against MockWebServer loaded with the fixtures. Once a milestone lands, it switches to the real server on the LAN.

```
Week →      1      2      3      4      5      6      7      8
Contract   [P0]
Server            [S1 ][S2 ][S3     ][S4     ][S5  ][S6  ]
Android           [A1 ][A2      ][A3     ][A4  ][A5    ][A6  ]
Milestone          M1 ─────┘     M2 ──────┘ M3   M4 ───────┘ M5
```

The weeks are relative and show the ordering only. They are not estimates.

---

## 2. Phase 0 — Contract & scaffolding

**Goal:** freeze the wire format, and set up both projects so each can build in CI. This phase runs on `main` before the track worktrees are created.

| Task | Track |
| --- | --- |
| Create `contract/` with fixtures: `info.json`, `job-{pending,downloading,processing,paused,completed,failed,cancelled}.json`, `jobs-list.json`, `batch-create-response.json`, `error-{invalid_request,unauthorized,not_found,invalid_state,duplicate,ytdlp_unmanaged,ytdlp_busy}.json`, `ws-{snapshot,job_added,job_updated,job_removed}.json`, `ytdlp-update.json`. Every timestamp has millisecond precision (D5). | Contract |
| Add request fixtures: `create-job-{movie,tv,flat}.json`, `create-batch.json` | Contract |
| Add intent fixtures: `intent/valid-*.json` (including a single episode with no `title`, per D6) and `intent/invalid-*.json`, covering each rejection rule in the [Intent Guide](fetcharr-android-intents.md) | Contract |
| Write `server/testdata/fake-ytdlp` (respects `--progress-template`, `--print after_move:filepath`, handles SIGTERM, and has a failure mode triggered by a magic URL) | Server |
| `server/`: Go module, `cmd/fetcharr`, `internal/…` layout, `golangci-lint`, a GitHub Actions workflow filtered to `server/**` that runs `go test ./...` | Server |
| `android/`: Gradle version catalog, Compose M3, Hilt, kotlinx.serialization, `contract/` wired in as a test resource dir, a GitHub Actions workflow filtered to `android/**` that runs `./gradlew test lint` | Android |
| Both workflows also trigger on `contract/**`, so a fixture change runs both test suites | Contract |

**Done when** both CI workflows pass and I have reviewed the fixtures. After this, any change to a fixture is a contract change: it lands on `main` first, and both tracks rebase onto it.

---

## 3. Server track

Suggested libraries: `net/http` (Go 1.22+ pattern routing), `modernc.org/sqlite` (no CGO, so cross-compiling and Docker builds stay simple), `github.com/coder/websocket`, `gopkg.in/yaml.v3`, `log/slog`.

### S1 — Config, storage, REST without downloads

The server accepts and manages jobs, but nothing downloads yet. Jobs stay `pending`.

- Config loading (`--config`, `FETCHARR_CONFIG`), byte-size parsing, and every startup validation rule ([§3](fetcharr-server.md#3-configuration)).
- SQLite schema and migrations, with the DB file created as `0600`. Store headers in their own column, and never put them in the Job DTO.
- Timestamps are stored and serialized as RFC 3339 UTC with millisecond precision (D5). Use one shared formatter so no endpoint falls back to whole seconds.
- Job state machine ([§2.4](fetcharr-server.md#24-job-lifecycle)) as a pure function with table-driven tests. Model "placement started" as an input now, so S4's cancel rule (D7) is a table row, not a special case.
- Duplicate detection: use a partial unique index on `url` for active statuses, so two requests racing for the same URL cannot both succeed.
- Naming and sanitization ([§5](fetcharr-server.md#5-library-layout--naming)) as pure functions. Validation needs them already, to reject names that are empty after sanitizing. Test them heavily: UTF-8 truncation at 180 bytes, trailing dots, three-digit episode numbers, season 0, and a `tv` episode with no title.
- Endpoints: `health`, `info` (yt-dlp version read from the configured binary), `POST /jobs`, `POST /jobs/batch`, `GET /jobs` with every filter, `GET /jobs/{id}`, `pause`/`resume`/`cancel`/`retry`, both `DELETE`s, and the batch actions.
- Bearer auth middleware with constant-time compare, and the error envelope.
- Golden tests against `contract/`.

**Done when** every endpoint passes its contract tests, and a curl script can create, list, pause, resume, cancel and delete jobs.

### S2 — Event bus & WebSocket

- An in-process event bus that the job service publishes to on every change.
- A WebSocket hub that sends `snapshot` on connect, plus `job_added`, `job_updated` and `job_removed`.
- Progress-only updates are throttled to 1/s per job. Status changes skip the throttle.
- Ping every 30 s, close after 60 s without a pong, and drop a client whose send buffer is full.
- Auth on the upgrade request.

**Done when** `websocat` shows the right event stream while the S1 curl script runs.

### S3 — Download pipeline

- Worker pool sized by `concurrency`. The dispatcher pulls pending jobs in `queued_at` order and wakes on create, resume and retry.
- Give the dispatcher a **hold** switch: while held, it starts no new jobs, and it signals when no job is `downloading`. S5's update drain (D1) uses it.
- Pre-checks: free disk space on the category's filesystem, and an existing file at the target path.
- yt-dlp invocation exactly as in [§4.2](fetcharr-server.md#42-download-downloading). Run it in its own process group (`Setpgid`), parse the JSON progress lines, and use the last-`ERROR:` capture as the error message with header values redacted.
- Pause and cancel kill the process group. Pause keeps the work dir; cancel deletes it, so a retry after cancel starts from scratch (D4).
- Startup recovery (`downloading`/`processing` back to `pending`) and graceful shutdown.
- Recover panics in workers.
- Integration tests run on the fake yt-dlp.

**Done when** a job sent to the fake yt-dlp moves `pending → downloading → processing`, with live progress over WebSocket, and pause/resume continues from where it stopped.

### S4 — Processing & library placement

- Subtitle download with the job's headers, conversion to SRT with ffmpeg, and per-subtitle warnings instead of failure.
- Second existing-file check, then placement: rename on the same filesystem, or copy to `.partial` and rename across filesystems.
- **Placement lock (D7).** Mark the job as placing before the first file moves. From then on, cancel returns `409 invalid_state` and the job runs to `completed` or `failed`. Test that a cancel during subtitle conversion still works, and that a cancel during placement is refused.
- Path containment check after cleaning and resolving symlinks.
- `overwrite: true` on retry.
- `DELETE ?delete_file=true`: delete the media file and its sidecars, then remove folders left empty, up to but not including the category root.
- End-to-end test with the real yt-dlp against a local `httptest` server serving a small MP4 and a VTT file.

**Done when** finished jobs end up in correctly named folders with `.lang.srt` sidecars, and `delete_file` cleans up empty folders.

### S5 — Operations features

- Managed yt-dlp: download it from GitHub releases on first start, and run `-U` at startup and every 24 h.
- **Update drain (D1).** When an auto-update is due, put the dispatcher on hold, wait until nothing is `downloading`, update, then release the hold. Release the hold even if the update fails. `processing` jobs don't block it.
- `POST /system/ytdlp/update` (D8): update right away if nothing is `downloading`, otherwise return `409 ytdlp_busy`. It never waits.
- Media server refresh: a per-folder 30 s debounce, `path_map` rewriting, and the Jellyfin and Plex clients (tested against `httptest` fakes).
- Hourly history pruning.

### S6 — Hardening & release

- Audit logging: headers are never logged, and URLs are logged without their query string. Add a test that captures logs during a job carrying headers and asserts the header values never appear.
- Dockerfile: multi-stage build, a runtime with `ffmpeg` and `ffprobe`, a `HEALTHCHECK`, and a non-root user.
- GitHub Actions publishes `ghcr.io/istarvin/fetcharr-server` for amd64 and arm64.
- Example `config.yaml`, a systemd unit and a README.

---

## 4. Android track

Platform (D2, D3, D11): minSdk 29 (Android 10), targetSdk 36, cleartext HTTP allowed, distributed as a signed APK on GitHub Releases.

### A1 — Shell, settings, network layer

Can start right after Phase 0. It needs no running server.

- Single `singleTask` activity, a bottom bar (Queue, History, Settings) and the navigation graph.
- `res/xml/network_security_config.xml` with `cleartextTrafficPermitted="true"` in the base config (D2), referenced from the manifest.
- DataStore: server URL, token, default categories, cached categories, *last seen*.
- Settings screen: URL normalization (add `http://`, strip a trailing slash) and Test connection with all four outcomes ([§5.5](fetcharr-android.md#55-settings)).
- Retrofit/OkHttp: auth interceptor, 10 s connect and 30 s read timeouts, and DTOs for every fixture. Parse timestamps with fractional seconds (D5).
- Error mapping: turn the server's error envelope into a sealed `ApiError` that matches the table in [§7](fetcharr-android.md#7-error-handling), including `ytdlp_busy`.
- Formatters: binary byte units, speed, ETA, and hiding missing values.
- Tests: every `contract/` fixture parses, and MockWebServer tests cover each error code.

**Done when** Test connection works against MockWebServer, and against a real S1 server once one exists.

### A2 — Receiving & submitting downloads

Needs S1 for real submits. Until then it can run on MockWebServer.

- Intent payload parser: standard and URL-safe Base64 with optional padding, every validation rule, and tests over `contract/intent/`. `single.title` is optional when `showName`, `season` and `episode` are all present (D6).
- Manifest: `fetcharr://` VIEW filter and `ACTION_SEND text/plain`. Handle intents in both `onCreate` and `onNewIntent`.
- **Incoming coordinator.** This is the hardest logic in the app. Keep it as a plain Kotlin class with no Android dependencies. It owns the open sheet, the request queue, season merging (case-insensitive show name, replacing an episode number that's already listed) and the 2 s "Receiving episodes…" window. Test it with `kotlinx-coroutines-test` virtual time.
- Category preselection ([§4.2](fetcharr-android.md#42-category-preselection)).
- Incoming sheet, in both single and season modes, with fields that follow the layout. Add screen with header and subtitle rows.
- Submit through `POST /jobs` or `/jobs/batch`, with all the snackbar and error rules from [§5.1](fetcharr-android.md#51-incoming-sheet).
- Headers stay only in the ViewModel's memory and are never written to DataStore.

**Done when** each ADB command from the Intent Guide produces a job on the real server, and ten rapid single-episode intents merge into one sheet.

### A3 — Live queue

Needs S2, and S3 with the fake yt-dlp to see real progress.

- OkHttp WebSocket client with backoff (1 s, doubling, capped at 30 s), a connection-state flow and the banner.
- An active-jobs store, a singleton `StateFlow`: replaced on `snapshot`, patched by the other events. On reconnect, reload `info`.
- Queue screen: Active, Queued and Paused sections, batch group cards showing combined progress and Pause/Resume/Cancel all, job cards, pull-to-refresh, the FAB and empty states.
- Actions call REST and let the WebSocket event update the UI. `409 invalid_state` refreshes the job and shows a snackbar. This is also what a cancel during placement returns (D7).

**Done when** progress from the fake yt-dlp animates live, pause/resume/cancel work on single jobs and batches, and killing the server shows the banner and then reconnects.

### A4 — History

Needs S1, plus S4 for real file paths.

- Paged `GET /jobs?status=completed,failed,cancelled`, grouped by Today, Yesterday and then the date.
- Live insertion when a `job_updated` event moves a job into a finished status.
- Long-press menu and swipe-to-delete. Delete dialog with "Also delete file from disk". Retry, with the replace prompt for `already_exists`. Copy path. A details view for errors. Clear history.

### A5 — Notifications & background

- Channels `active` and `results`. Ask for `POST_NOTIFICATIONS` on the first submit.
- A `dataSync` foreground service (`FOREGROUND_SERVICE_DATA_SYNC` permission on API 34+). It starts after a submit or when a sync finds active jobs, and stops when no `pending`/`downloading`/`processing` jobs remain. It also stops after 15 minutes of the server being unreachable, or when `onTimeout` fires (the dataSync time limit that applies at targetSdk 35+), and posts the "Stopped tracking downloads" notice.
- Result notifications: per job for jobs without a batch, and one summary when a whole batch has finished. No notification for user cancels. The batch summary counts only downloaded and failed episodes, and is skipped if every episode was cancelled. An episode retried after its batch summary gets its own per-job notification (D9). A Retry action goes through a `BroadcastReceiver`.
- Catch-up through `finished_after=<last seen>` on each reconnect. Set *last seen* to the current time on first run.
- Test with `adb shell cmd activity` service-timeout simulation and a doze test.

### A6 — Polish & release

- Settings: yt-dlp version and Update now (hidden when unmanaged; shows "Downloads are running — try again when they finish" on `ytdlp_busy`), and the About section.
- Go through the error table in [§7](fetcharr-android.md#7-error-handling) one row at a time.
- Accessibility pass (TalkBack labels on progress and actions), dark theme, and dynamic color.
- R8 rules for kotlinx.serialization and a signed release build. The keystore and its passwords live in GitHub Actions secrets. A tag push builds the APK and attaches it to a GitHub Release (D11).

---

## 5. Integration milestones

Each milestone is a checkpoint. I merge both track branches into `main` and run the scenario by hand with a real server and a real device. Anything that fails goes back to the owning track's agent as a fix task before that track starts its next phase.

| # | Server needs | Android needs | Scenario |
| --- | --- | --- | --- |
| **M1** Submit | S1 | A1 | Test connection shows the version. The Add screen creates a job, which then appears in `curl /api/jobs`. |
| **M2** Intake | S1 | A2 | All three ADB commands from the Intent Guide land on the server. A ten-episode `single` burst becomes one batch. A duplicate shows "Already in the queue". A single episode with no title is accepted. |
| **M3** Live | S2 + S3 (fake yt-dlp) | A3 | Live progress, pause/resume, batch actions. Restart the server mid-download: the banner shows, the app reconnects, and the snapshot is correct. |
| **M4** Library | S4 | A4 | Real yt-dlp download of a public test video. History shows the path. Delete with the file removes the folder. Retry with `already_exists` offers to replace. |
| **M5** Release | S5 + S6 | A5 + A6 | Docker image on the target host. Lock the phone during a season batch and get one summary notification. Update yt-dlp from Settings, once while idle and once while downloading (expect the busy message). Install the APK from the GitHub Release and run from CloudStream itself. |

---

## 6. Decisions

These were the open questions. All of them are now decided, and each decision is written into the spec listed.

| # | Question | Decision | Written into |
| --- | --- | --- | --- |
| D1 | yt-dlp auto-update vs. dispatch | When an update is due, stop dispatching, wait until nothing is `downloading`, update, then resume. `processing` doesn't block it. | [Server §8](fetcharr-server.md#8-yt-dlp-management) |
| D2 | Cleartext HTTP | Allowed everywhere through the network security config. Use an HTTPS reverse proxy for internet-facing servers. | [Android §3.1](fetcharr-android.md#31-settings-needed) |
| D3 | SDK levels | minSdk 29 (Android 10), targetSdk 36. | [Android](fetcharr-android.md) header |
| D4 | Retry of a cancelled job | Starts from scratch. Pause is the way to keep partial files. | [Server §2.4, §4.4, §6.3](fetcharr-server.md#24-job-lifecycle) |
| D5 | `finished_after` ties | All timestamps are RFC 3339 UTC with millisecond precision. | [Server §6.1](fetcharr-server.md#61-conventions) |
| D6 | Single episode with no `title` | `title` is optional when `showName`, `season` and `episode` are all set. | [Intent Guide](fetcharr-android-intents.md#payload-schema) |
| D7 | Cancel during file placement | Placement can't be cancelled. Once it starts, cancel returns `409 invalid_state` and the job finishes. | [Server §2.4, §4.3, §4.4](fetcharr-server.md#24-job-lifecycle) |
| D8 | Manual yt-dlp update while downloading | Runs at once if idle, otherwise returns `409 ytdlp_busy`. It never waits. | [Server §6.1, §6.3](fetcharr-server.md#61-conventions), [Android §5.5](fetcharr-android.md#55-settings) |
| D9 | Cancelled episodes in a batch summary | Excluded from the counts. No summary if all of them were cancelled. A later retry gets a per-job notification. | [Android §6](fetcharr-android.md#6-notifications--background) |
| D10 | Who builds it | One developer, with the two tracks run as parallel Claude agents in separate worktrees (§1). | This plan |
| D11 | App distribution | A signed APK on GitHub Releases, built by CI on tag push. | [Android](fetcharr-android.md) header, A6 |
