# Product

<!-- impeccable:product-schema 1 -->

## Platform

android

## Users

A self-hoster who runs their own media server (Jellyfin or Plex) and a Fetcharr server at home or on a VPN. They find something to watch in a streaming app such as CloudStream on their phone and want it filed into their library with as little effort as possible. Technically comfortable, personal or household use. Quick glance-and-confirm sessions on a phone, not long browsing.

## Product Purpose

Fetcharr is an Android client and self-hosted download server. Share a media URL or send a `fetcharr://` request to the app; the server downloads it with yt-dlp, processes it with ffmpeg, and files it into movie, TV, or flat library folders. The Android app receives requests, lets the user confirm metadata, category and episodes, then shows live progress, history and background notifications. Success is a request confirmed in seconds and a finished file in the right library folder, with the user never needing to open the server.

## Positioning

It closes the gap between a streaming app on the phone and the media library on the home server: a request arrives from another app through an intent contract, is merged and confirmed in one sheet, and the server does the rest. Generic download managers and server web UIs do not take this handoff from apps like CloudStream or know the movie/TV/flat library layout.

## Operating Context

- Requests arrive via `fetcharr://` intents from other apps, the Android share sheet (`ACTION_SEND` text/plain), or the manual Add screen.
- Runs on Android 10+ (API 29), single activity with bottom navigation: Queue (start), History, Settings. Incoming sheet is a modal bottom sheet over any tab; Add is a full screen from the Queue FAB or the share sheet.
- Live job state comes over WebSocket; background notifications cover progress and results while the app is closed.
- Server connection is a URL plus optional token, configured in Settings. Library destinations, and optional Jellyfin/Plex connections, are managed from Settings → Server settings.
- History items can be played in an external Android player through temporary playback links.

## Capabilities and Constraints

- The server is the source of truth. No offline queue, no device-side job database; if the server is unreachable a submit fails and the user retries.
- The app never downloads media itself and has no built-in player or local media management.
- Runs of single-episode intents for the same show and season are merged into one confirmation.
- Job actions: pause, resume, cancel, retry; History actions: play, retry, copy path, delete, clear.
- The receive-and-confirm flow (Incoming sheet) is the core interaction and must stay fast and low-friction.
- Terminology: Queue, History, Settings, Incoming sheet, category, job, library.

## Brand Commitments

- Brand blue from the launcher icon (#1E6FD9) with teal as the cool accent for "done" and secondary actions.
- Material You dynamic color on Android 12+ is intended, with the brand scheme as the fallback.

## Evidence on Hand

- Product and behavior specs: `docs/fetcharr-android.md`, `docs/fetcharr-android-intents.md`, `docs/fetcharr-server.md`.
- Existing Material 3 theme and tokens in `android/app/src/main/kotlin/com/istarvin/fetcharr/ui/theme/`.
- No testimonials, user numbers, or store listing copy exist; do not fabricate them.

## Product Principles

1. Fast to confirm: the shortest path from a received request to a submitted job wins over completeness of options.
2. Truthful state: show what the server reports, including disconnected and failed states, plainly and without optimism.
3. Native first: follow Material 3 and Android conventions, intents, notifications and back behavior rather than inventing parallel patterns.
4. Glanceable: Queue and History must read in a second on a phone, in motion or at a distance.
5. The app is a remote, not a workshop: the server does the work and the app stays small.

## Accessibility & Inclusion

No product-specific requirement established beyond Android platform accessibility baselines (TalkBack labels, touch targets, contrast, font scaling, dark theme).
