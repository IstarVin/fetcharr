# Fetcharr Web

The browser client built into the [Fetcharr server](fetcharr-server.md). It covers the same features as the [Android app](fetcharr-android.md), except for intent handling and notifications. Open `http://<server>:<port>/`.

**Stack:** Svelte 5 · TypeScript · Vite. The build is embedded in the server binary (`server/internal/webui`), so it needs no separate deployment.
**Source:** [`web/`](../web)

---

## 1. Connection and sign-in

- The UI talks to the server it was loaded from. API URLs are resolved relative to the page, so a reverse-proxy prefix is preserved ([server §10](fetcharr-server.md#reverse-proxy)).
- On load it calls `GET /api/info`. A `401` shows the sign-in screen. The token is posted once to `POST /api/session`, which sets an `HttpOnly` cookie; JavaScript never stores the token. Without a configured token there is no sign-in screen.
- Any later `401` (expired cookie, changed token) returns to sign-in. **Settings → Sign out** calls `DELETE /api/session`.
- One WebSocket serves the whole page. While it is not connected, a banner shows "Connecting…" or "Disconnected · retrying in N s". Reconnects back off up to 30 s, and **Retry now** skips the wait. After a reconnect, History catches up with `GET /api/jobs?finished_after=` ([server §7](fetcharr-server.md#7-websocket)).
- Mutations apply the job returned by the API right away; the WebSocket event that follows is an idempotent upsert.

## 2. Layout

Header tabs **Queue**, **History** and **Settings**, plus **Add download**. Below 640 px the tabs become a bottom navigation bar and Add becomes an icon button. Routes are hash-based (`#/queue`, `#/history`, `#/add`, `#/settings`, `#/server-settings`). Light and dark themes follow the system and use the Android brand scheme.

If `setup_required` is true, a banner links to Server settings. While downloads run, the tab title shows them, e.g. `(2) 43% · Queue · Fetcharr`.

`#/add?url=…` opens Add download with the URL filled in. The page also accepts `?url=` or `?text=` (the first http(s) URL is used) and turns it into that route, which serves the **Send to Fetcharr** bookmark (Settings) and the installed web app's share target (`manifest.webmanifest`; installing needs HTTPS).

## 3. Screens

**Queue.** Same sections, batch grouping, card content, actions and cancel confirmation as [Android §5.3](fetcharr-android.md#53-queue). The Active heading shows the combined speed. Queued jobs and batches other than the first offer **Download next**. While the server's download window is closed, the Queued heading and cards say when downloads start. Refresh reconnects the WebSocket and reloads server info.

**History.** Same rows, error texts, day grouping and actions as [Android §5.4](fetcharr-android.md#54-history), with All / Completed / Failed filters and a title search (`q`, when the server supports it), both applied by the server, and paging as the list scrolls. A row expands to show the saved path (with Copy path), the error message or warnings, and the source host and subtitles.

- **Play** requests a signed link (`POST /api/jobs/{id}/playback`) and plays it in an in-page `<video>`. If the browser can't decode the container or codec, the dialog says so and offers **Copy link** and **Open** for an external player. The link never contains the API token.
- **Replace source & retry** is shown for failed and cancelled jobs when the server advertises `source_replacement_supported`. The editor behaves like Android's: URL, Clear/Keep/Replace headers (saved headers are never shown), subtitle rows, overwrite confirmation for `already_exists`, and a leave confirmation while submitting.
- Copy uses the Clipboard API, falling back to a hidden text field on plain-HTTP origins.

**Add download.** Two modes:

- *Single:* URL, category chips, the fields of the category's layout (movie: Title, Year; tv: Show, Season, Episode, Episode title, Year; flat: Title), and optional request headers and subtitles under **Advanced**. Sends `POST /api/jobs`.
- *Season* (tv categories only): Show, Season, Year, shared headers, and episode rows (number, optional title, URL). **Paste several URLs** turns one URL per line into numbered episodes. Sends `POST /api/jobs/batch`; per-episode headers and subtitles are not editable here. The result reads "Added 6 · 2 already in library · 1 already queued"; episodes already in the library are skipped.

`409 duplicate` shows "Already in the queue". `409 already_exists` asks "Already in library" with **Replace** (sends again with `"overwrite": true`) or **Keep existing** (stays on the form with the error "Already in library"). Other errors stay on the form. The category is preselected from this browser's default for the request type (Settings → Defaults), otherwise the first category that fits.

**Settings.** Server address, version, access mode and concurrency; **Test connection** (version and latency); library folders with free space; per-browser default categories (stored in `localStorage`); **Android app** connection QR code (`fetcharr://connect?url=…&token=…`; the token is typed on the page only to draw the code), the **Send to Fetcharr** bookmark, opt-in browser notifications for downloads that finish while the tab is in the background; yt-dlp version, **Check for update** and **Update to latest** (managed binary only, "Downloads are running — try again when they finish" on `ytdlp_busy`); Sign out.

**Server settings.** The same full-document editor as Android ([Android §5.5](fetcharr-android.md#55-settings)): downloads (including speed limit, download window and automatic retries), maintenance, notification targets with **Send test**, categories with generated hidden IDs, and Jellyfin/Plex connections with write-only credentials and path mappings. Validation messages match Android. Save sends `PUT /api/system/settings` with the loaded revision. `settings_conflict` shows a reload prompt. Leaving the page or closing the tab with unsaved edits asks first. Servers without `settings_supported` show an upgrade message.

## 4. Development

```sh
cd web
npm ci
npm run dev        # http://localhost:5173, proxies /api (and the WebSocket) to FETCHARR_URL or http://localhost:8083
npm run check      # svelte-check + TypeScript
npm test           # vitest, including the shared contract/ fixtures
npm run build      # writes server/internal/webui/dist/app for embedding
```

Rebuild the server after `npm run build` to embed the new files. A server built without the frontend still runs and serves a short notice at `/`. The Docker image builds both.
