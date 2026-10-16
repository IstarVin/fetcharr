# Cloudstream agent prompt: Send to Fetcharr

Copy the prompt below into your coding agent while it is working in the Cloudstream repository.

---

Implement a new **Send to Fetcharr** action in this Cloudstream repository.

Fetcharr is my separate Android client for a self-hosted download server. The intended flow is:

1. Open the existing action menu for a movie or episode, alongside actions such as **Play in app** and **Auto download**.
2. Choose **Send to Fetcharr**.
3. Cloudstream resolves the available media links using its existing provider/extractor pipeline.
4. Cloudstream shows a link picker. The user explicitly chooses one source/quality.
5. Cloudstream sends that selected media URL, its required HTTP headers, available subtitles, and media metadata to the Fetcharr Android app.
6. Fetcharr opens its existing confirmation sheet. The user chooses a category and confirms there; Fetcharr submits the request to its server.

Implement the feature, rather than stopping at a proposal. Inspect the repository instructions and existing architecture first. Locate the actual action menu, action dispatch, link resolution, source picker, metadata models, and external-app launch helpers. Use the names and patterns in this checkout; the menu labels above describe the intended location and are not assumptions about exact source identifiers. Keep the change focused and preserve the existing actions.

## Cloudstream behavior

- Add the action wherever that existing movie/episode action menu is supported. Follow the project's mobile and TV conventions, including focus/navigation if those surfaces share the menu.
- Reuse the existing asynchronous link resolver and its lifecycle, progress, cancellation, and error handling. Resolve media URLs, not provider detail-page URLs. This action must not start playback or Cloudstream's local downloader.
- Reuse or adapt the existing source picker where possible. Show provider/server name, quality/resolution, and stream type when available. Do not show credentials or full signed URLs in labels. Keep distinct sources separate even if their URLs match but their required headers differ.
- Require an explicit link choice even if there is only one result. Do not automatically select the highest-quality result or send all results. Cancelling resolution or the picker must not launch Fetcharr.
- Only offer transferable HTTP/HTTPS media sources. Direct files, HLS manifests, and DASH manifests can be handed off. Sources requiring DRM or a special playback/download mechanism that cannot be represented by one URL and a shared header map must be unavailable with an understandable explanation. Do not assume every HTTP source is compatible.
- Handle no results and extraction failures with the project's normal user-visible error UI. Prevent duplicate launches from repeated taps and ignore stale results after cancellation/navigation. Catch launch failures, including Fetcharr not being installed, without crashing or falling back to a local download.
- Use existing string resources, localization conventions, and UI components. Do not add a new network client, credentials setting, or dependency if existing facilities suffice.

## Exact Fetcharr intent contract

Use an Android intent with:

- Action: `android.intent.action.VIEW` (`Intent.ACTION_VIEW`).
- Data URI: `fetcharr://download`.
- Target package: `com.istarvin.fetcharr`.
- String extra named exactly `payload`: standard Base64, without line wrapping, of UTF-8 JSON.

Keep media URLs, headers, and JSON in the extra, rather than putting them in the URI. Use the project's JSON serialization facilities; do not construct JSON by concatenating strings. Omit absent optional fields. Root JSON must contain exactly one of `single` or `batch`. This feature sends one selected movie/episode using `single`.

Illustrative launch code; adapt serialization and context handling to this repository:

```kotlin
val encodedPayload = Base64.encodeToString(
    payloadJson.toByteArray(Charsets.UTF_8),
    Base64.NO_WRAP,
)
val intent = Intent(Intent.ACTION_VIEW, Uri.parse("fetcharr://download")).apply {
    setPackage("com.istarvin.fetcharr")
    putExtra("payload", encodedPayload)
    // Add FLAG_ACTIVITY_NEW_TASK only when the launching context requires it.
}
// Use the app's existing launch/error handling; catch ActivityNotFoundException.
context.startActivity(intent)
```

Prefer attempting the package-targeted launch and handling failure. If you use package-manager availability checks, account for Android package visibility with a narrow manifest query where necessary; do not request broad package access.

Movie payload example:

```json
{
  "headers": {
    "Referer": "https://provider.example/",
    "User-Agent": "the actual user agent required by this source"
  },
  "single": {
    "url": "https://cdn.example/movie/master.m3u8?token=example",
    "title": "Example Movie",
    "year": 2026,
    "subtitles": [
      { "url": "https://cdn.example/movie/en.vtt", "lang": "en" }
    ]
  }
}
```

Episode payload example:

```json
{
  "headers": { "Referer": "https://provider.example/" },
  "single": {
    "url": "https://cdn.example/show/s01e02.mp4",
    "title": "Episode Two",
    "showName": "Example Show",
    "season": 1,
    "episode": 2,
    "year": 2026,
    "subtitles": [
      { "url": "https://cdn.example/show/s01e02.en.vtt", "lang": "en" }
    ]
  }
}
```

Mapping requirements:

- `headers` is a root-level string-to-string map. Preserve the selected source's effective HTTP headers, including Referer, User-Agent, Cookie, or source Authorization when actually required and available. Inspect how Cloudstream constructs requests for that source and mirror its header precedence, including case-insensitive header names. Do not invent headers, copy unrelated account credentials, or discard extractor headers in favor of generic defaults.
- Preserve media URLs exactly, including query parameters and signatures. Unwrap any internal Cloudstream URL representation through existing helpers. Do not strip tokens, decode/re-encode signed URLs unnecessarily, or forward an internal wrapper as the media URL.
- `single.url` is required and must be HTTP/HTTPS.
- For movies, `single.title` is required. For episodes, populate `showName`, `season`, and `episode` from real metadata; `title` is the episode title when available. Season and episode are nonnegative JSON integers; preserve legitimate zero values. Do not fabricate missing season/episode numbers. If complete episode metadata is unavailable, still provide a meaningful title so Fetcharr accepts the request and the user can review it.
- `year` is an optional JSON integer; for episodes use the show's year when available. Omit unknown values.
- `subtitles` is an optional array of HTTP/HTTPS `{url, lang}` objects. Include applicable resolved subtitle tracks, deduplicated without changing their URLs. Normalize known languages to ISO 639-1 codes using existing utilities. If a language cannot be mapped reliably, omit `lang` rather than inventing it: Fetcharr's current parser accepts a missing language.
- The current contract has one shared header map for the video and subtitles, without separate subtitle headers. Do not claim independent subtitle headers or other unsupported fields will work. Omit tracks that cannot be represented correctly, and document any resulting limitation.
- `category` is an optional root string matching a user-configured Fetcharr server category ID. Omit it by default: IDs are not guaranteed to be `movies` or `tv`. Fetcharr handles category selection/defaults.

Fetcharr owns its server URL, API token, category selection, confirmation, and server submission. Cloudstream only launches the Android intent. Successful launch means the request was handed off; it does not mean a download was queued or completed. Do not show a misleading success message or log payloads, signed URLs, cookies, or headers.

Whole-season sending and `batch` support are outside this change.

## Verification and delivery

Use the repository's appropriate build/check commands. Add focused tests for payload mapping/encoding and action dispatch where existing test infrastructure supports them. Cover movie and episode metadata, season/episode zero, unknown optional metadata, Unicode titles, signed URLs, required header precedence, subtitle language mapping, and the absence of `batch`/hardcoded category IDs. Check that cancellation, no results, unsupported sources, repeated taps, and a missing Fetcharr installation cannot trigger an unintended handoff or local download.

Manually verify on an Android device/emulator when available:

1. A movie with several sources opens the picker and sends only the chosen source.
2. An episode arrives with the correct show/season/episode metadata.
3. Fetcharr opens its confirmation sheet with headers/subtitles preserved; it does not submit until the user confirms.
4. Cancelling the picker opens nothing, and missing Fetcharr produces a useful message.
5. Existing playback and auto-download actions still behave as before.

If Fetcharr source is available alongside this checkout, inspect `docs/fetcharr-android-intents.md`, `android/app/src/main/kotlin/com/istarvin/fetcharr/ui/incoming/IntentRouter.kt`, `android/app/src/main/kotlin/com/istarvin/fetcharr/incoming/IntentPayloadParser.kt`, and `contract/intent/` to verify compatibility. These paths belong to Fetcharr, not Cloudstream. No Fetcharr source change is needed for this single-item handoff.

Finish with a concise report of the files changed, behavior implemented, checks actually run, and any limitations or device checks that remain unverified.
