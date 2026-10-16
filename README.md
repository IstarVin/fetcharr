# Fetcharr

An Android client and self-hosted download server. Share a media URL or send a
`fetcharr://` request to Android; the server downloads it with yt-dlp, processes
it with ffmpeg, and files it into movie, TV, or flat library folders. Android
shows the live queue, history, and background notifications. Server settings
and optional Jellyfin/Plex connections can be managed in the app.

Tap **Play** on a completed History item to stream the saved file in an external
Android player. Both the app and server need the playback update. Seeking is
supported; temporary playback links keep the API token inside Fetcharr.

## Run the server

Follow the [server quick start](server/README.md) for Docker or a standalone
binary. Configure a token when access needs authentication. Add the server URL
and token in Android, then open **Settings → Server settings** to add writable
library destinations.

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
```

Android tests use the shared [API fixtures](contract/README.md). The optional
`LiveServerTest` exercises the real Android HTTP/WebSocket stack against a
running server; its source documents the required fake-download setup.

## Release

Tags `android-vX.Y.Z` and `server-vX.Y.Z` trigger the separate release workflows.
Prerelease suffixes such as `-rc.1` are supported. Android runs tests and lint,
builds a signed APK, and verifies its signature before publishing. Configure
GitHub Actions secrets `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`,
`ANDROID_KEY_ALIAS`, and `ANDROID_KEY_PASSWORD` first. The server runs vet and
race tests before publishing its multi-platform container image.

See the [Android spec](docs/fetcharr-android.md),
[intent guide](docs/fetcharr-android-intents.md),
[server spec](docs/fetcharr-server.md), and [release audit](docs/release-audit.md)
for behavior and validation details.
