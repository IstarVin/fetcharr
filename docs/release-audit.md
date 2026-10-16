# Release audit — 2026-10-16

This pass reviewed Android sync, notifications, incoming requests, settings and
network boundaries; server request validation, authentication, job processing,
file placement, subtitle handling and deployment; and the release workflows.
It includes fixes and automated validation, rather than a claim that every
possible failure or third-party vulnerability has been ruled out.

## Findings fixed

| Priority | Finding | Resolution |
| --- | --- | --- |
| High | Concurrent jobs with different URLs could pass the same destination existence check and overwrite one library item without permission. | Serialize the existence check and placement per resolved directory and basename, across video extensions. Waiting remains cancellable; unrelated targets run independently. |
| High | Subtitle credentials could survive an HTTPS-to-HTTP redirect on the same host. | Remove supplied headers when the redirect origin changes, including its scheme. |
| Medium | Testing an unsaved server or receiving a delayed response could overwrite the saved server's categories. Versions and defaults could also describe a previous server. | Bind responses and displayed info to the connection, check persisted connection values during cache writes, and clear cached categories/defaults on URL changes. |
| Medium | Stable Docker releases never received `latest`: the check always matched the hyphen in `server-v`. | Check the stripped version. Add server race-test/vet gates before image publishing, Android release lint, tag validation, and Android prerelease marking. |
| Low | The settings endpoint's 405 response omitted `PUT` from `Allow`. | Include supported `HEAD` and `PUT` methods. |
| Low | Go lint failed on 16 existing issues. | Document exported APIs and compare wrapped errors with `errors.Is`. |

Regression tests cover overlapping destination placements with different file
extensions, cancellation while waiting, HTTPS downgrade redirects, delayed
category responses, unsaved server tests, server switches, defaults, and HTTP
method reporting.

## Validation

- `go test -race ./...`: all 19 packages passed.
- `go vet ./...` and `golangci-lint run`: passed; zero lint issues.
- `./gradlew test lint assembleDebug assembleRelease`: passed. Android unit
  suite: 430 tests, zero failures/errors; one opt-in live scenario skipped in
  the ordinary run.
- Ran that live scenario separately against an isolated server with fake
  yt-dlp: all 41 checks passed, including authentication, live updates, batch
  actions, pause/resume, cancel/retry, overwrite, placement, history and deletion.
- Workflow YAML parsing, shell syntax checks and valid/invalid release-tag
  examples passed. Remote GitHub Actions and container publishing were not run.
- Built the R8-minified release APK, applied a debug-key signature to a temporary
  smoke-test copy, and launched it on an isolated API 37 emulator. Saved and
  tested the server connection, opened and submitted a structured download
  intent, and accepted the notification permission prompt. With the app in the
  background, verified `SyncForegroundService` was foreground with a progress
  notification; after completion it stopped and posted a `Downloaded` result.
  The server-settings editor also loaded the live settings correctly. The
  emulator crash buffer remained empty.

## Release requirements and limits

The distributable release APK is unsigned in this checkout. Supply the existing
production keystore locally or configure the four Android signing secrets in
GitHub Actions as documented in the [README](../README.md). The temporary
debug-signed smoke build is not a production release.

Android lint reports zero errors and five warnings: the deliberate API 36
target, LAN cleartext HTTP support, two plural heuristics and a redundant icon
resource qualifier. Gradle also reports deprecations to review before moving
to Gradle 10. These do not block the current build.

Live client tests use fake downloads. Server tests additionally exercised real
yt-dlp and ffmpeg with local media. Real streaming providers, production media
server credentials, long-duration Android foreground-service limits, OEM
battery policies, and signed upgrade compatibility remain deployment checks.
No dependency CVE scan was performed, and no release tag was pushed or artifact
published.
