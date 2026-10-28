# Fetcharr

An Android client, a web UI and a self-hosted download server. Share a media URL
or send a `fetcharr://` request to Android, or paste one into the web UI; the
server downloads it with yt-dlp, processes it with ffmpeg, and files it into
movie, TV, or flat library folders. Both clients show the live queue and
history; Android adds background notifications. Server settings and optional
Jellyfin/Plex connections can be managed in either client.

The server can push results to ntfy or a webhook, limit download speed, start
downloads only inside a daily window, and retry failed downloads automatically.
Pair a phone by scanning the QR code under **Settings → Android app** in the web UI.

Tap **Play** on a completed History item to stream the saved file in an external
Android player. Both the app and server need the playback update. Seeking is
supported; temporary playback links keep the API token inside Fetcharr.

## Run the server

Follow the [server quick start](server/README.md) for Docker or a standalone
binary. Configure a token when access needs authentication. Add the server URL
and token in Android, then open **Settings → Server settings** to add writable
library destinations.

## Web UI

The server serves a browser UI at `http://<server>:<port>/`, using the same API
as Android. Sign in with the server's API token when one is configured. See the
[web spec](docs/fetcharr-web.md). Set `server.web_ui: false` for an API-only
server.

## Build the web UI

Use Node 24. The build is embedded into the server binary, so build it before
building the server (the Docker image does both):

```sh
cd web
npm ci
npm run build    # writes server/internal/webui/dist/app
```

`npm run dev` serves a hot-reloading UI that proxies `/api` to a running server
(`FETCHARR_URL`, default `http://localhost:8083`).

## Build Android

Use JDK 21 and Android SDK platform 37. The Gradle wrapper provisions its declared
daemon/toolchain when available. Set `sdk.dir` in `android/local.properties` to
your SDK location, or configure `ANDROID_HOME`.

```sh
cd android
./gradlew test lint assembleDebug assembleRelease
```

The app supports Android 10+ (API 29), targets API 36, and uses compile SDK 37.
APKs are written under `android/app/build/outputs/apk/`. The release build uses
R8 and resource shrinking. Without signing credentials, it produces an
**unsigned** APK.

For a signed local release, provide these environment variables or Gradle
properties using your existing release key:

- `FETCHARR_KEYSTORE_PATH`
- `FETCHARR_KEYSTORE_PASSWORD`
- `FETCHARR_KEY_ALIAS`
- `FETCHARR_KEY_PASSWORD`

Keep the key and passwords outside the repository. Updates to an installed app
must use its original signing key.

## Verify changes

```sh
cd server
go vet ./...
go test -race ./...
golangci-lint run

cd ../web
npm run check
npm test
```

Android tests use the shared [API fixtures](contract/README.md). The optional
`LiveServerTest` exercises the real Android HTTP/WebSocket stack against a
running server; its source documents the required fake-download setup.

## Release

Tags `android-vX.Y.Z` and `server-vX.Y.Z` trigger the separate release workflows.
Prerelease suffixes such as `-rc.1` are supported. Android runs tests and lint,
builds a signed APK, and verifies its signature before publishing. The server image includes the web UI. Configure
GitHub Actions secrets `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`,
`ANDROID_KEY_ALIAS`, and `ANDROID_KEY_PASSWORD` first. The server runs vet and
race tests before publishing its multi-platform container image.

See the [Android spec](docs/fetcharr-android.md), [web spec](docs/fetcharr-web.md),
[intent guide](docs/fetcharr-android-intents.md),
[server spec](docs/fetcharr-server.md), and [release audit](docs/release-audit.md)
for behavior and validation details.
