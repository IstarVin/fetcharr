# Fetcharr Android Intent Guide

How another Android app, such as a CloudStream plugin, sends a download request to Fetcharr. Fetcharr shows a confirmation sheet; it submits nothing to the server until the user confirms.

## Intent Contract

| Field | Value |
| --- | --- |
| Action | `android.intent.action.VIEW` |
| Data URI | `fetcharr://download` (any `fetcharr://` URI is accepted) |
| Package | `com.istarvin.fetcharr` (set it so Android skips the app chooser) |
| String extra | `payload`: Base64 (standard or URL-safe, padding optional) of UTF-8 JSON |

Put URLs and headers inside the payload, never in the URI.

## Payload Schema

Root object. It must contain **exactly one** of `single` or `batch`.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `category` | string | no | Category hint. Preselected if it matches a server category id; otherwise Fetcharr picks the user's default ([details](fetcharr-android.md#42-category-preselection)). |
| `headers` | object<string,string> | no | HTTP headers needed for the video and subtitle URLs (Referer, User-Agent, Cookie…). |
| `single` | object | one of | One movie, episode or other file. |
| `batch` | object | one of | Several episodes of one season. |

**`single`**

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `url` | string | yes | `http`/`https` media URL (direct file, HLS or DASH) |
| `title` | string | yes* | Movie title, or the episode title for episodes. *Optional when `showName`, `season` and `episode` are all set |
| `showName` | string | no | Together with `season` and `episode`, marks the request as an episode |
| `season` | int | no | ≥0 |
| `episode` | int | no | ≥0 |
| `year` | int | no | Release year (for episodes, the show's year) |
| `subtitles` | array of `{url, lang}` | no | `lang` is an ISO 639-1 code such as `en`; any subtitle format |

**`batch`**

| Field | Type | Required |
| --- | --- | --- |
| `showName` | string | yes |
| `season` | int | yes |
| `year` | int | no |
| `episodes` | array (≥1) | yes |

Each episode has `episode` (int, required), `url` (required), `title` (optional) and `subtitles` (optional).

Unknown keys are ignored. Fetcharr rejects the payload, showing "Couldn't read the download request", when:

- the action or scheme is wrong, or `payload` is missing;
- the payload isn't valid Base64 or JSON;
- it has neither or both of `single`/`batch`;
- a required field is missing, or a URL isn't `http`/`https`.

### Sending a season

There are two ways to send a season:

1. **One `batch` intent** with all episodes (preferred).
2. **One `single` intent per episode**, sent back to back. Each needs `showName`, `season` and `episode`. Fetcharr merges episodes with the same show and season into the open confirmation sheet, and each episode keeps its own `headers`.

## Example

```json
{
  "category": "tv",
  "headers": { "Referer": "https://stream.example/", "User-Agent": "CloudStream" },
  "single": {
    "url": "https://cdn.example/show/s01e01.m3u8",
    "title": "Pilot",
    "showName": "Example Show",
    "season": 1,
    "episode": 1,
    "year": 2026,
    "subtitles": [
      { "url": "https://cdn.example/show/s01e01.en.vtt", "lang": "en" },
      { "url": "https://cdn.example/show/s01e01.es.vtt", "lang": "es" }
    ]
  }
}
```

## Kotlin Sender

```kotlin
@Serializable data class FetcharrPayload(
    val category: String? = null,
    val headers: Map<String, String> = emptyMap(),
    val single: Single? = null,
    val batch: Batch? = null,
)
@Serializable data class Single(
    val url: String, val title: String? = null,
    val showName: String? = null, val season: Int? = null, val episode: Int? = null,
    val year: Int? = null, val subtitles: List<Subtitle> = emptyList(),
)
@Serializable data class Batch(
    val showName: String, val season: Int, val year: Int? = null,
    val episodes: List<Episode>,
)
@Serializable data class Episode(
    val episode: Int, val url: String, val title: String? = null,
    val subtitles: List<Subtitle> = emptyList(),
)
@Serializable data class Subtitle(val url: String, val lang: String)

fun sendToFetcharr(context: Context, payload: FetcharrPayload) {
    val json = Json { explicitNulls = false }.encodeToString(payload)
    val intent = Intent(Intent.ACTION_VIEW, Uri.parse("fetcharr://download")).apply {
        setPackage("com.istarvin.fetcharr")
        putExtra("payload", Base64.encodeToString(json.toByteArray(Charsets.UTF_8), Base64.NO_WRAP))
        addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    }
    context.startActivity(intent) // throws ActivityNotFoundException if Fetcharr isn't installed
}
```

## ADB Test Commands

```bash
send() { adb shell am start -a android.intent.action.VIEW -d 'fetcharr://download' \
  -n com.istarvin.fetcharr/.MainActivity --es payload "$1"; }

# Movie
send 'eyJjYXRlZ29yeSI6Im1vdmllcyIsInNpbmdsZSI6eyJ1cmwiOiJodHRwczovL2Nkbi5leGFtcGxlL21vdmllLm1wNCIsInRpdGxlIjoiRXhhbXBsZSBNb3ZpZSIsInllYXIiOjIwMjZ9fQ=='

# Episode with headers and a subtitle
send 'eyJjYXRlZ29yeSI6InR2IiwiaGVhZGVycyI6eyJSZWZlcmVyIjoiaHR0cHM6Ly9zdHJlYW0uZXhhbXBsZS8ifSwic2luZ2xlIjp7InVybCI6Imh0dHBzOi8vY2RuLmV4YW1wbGUvc2hvdy9zMDFlMDEubXA0IiwidGl0bGUiOiJQaWxvdCIsInNob3dOYW1lIjoiRXhhbXBsZSBTaG93Iiwic2Vhc29uIjoxLCJlcGlzb2RlIjoxLCJzdWJ0aXRsZXMiOlt7InVybCI6Imh0dHBzOi8vY2RuLmV4YW1wbGUvc2hvdy9zMDFlMDEuZW4udnR0IiwibGFuZyI6ImVuIn1dfX0='

# Batch (2 episodes, no category hint)
send 'eyJiYXRjaCI6eyJzaG93TmFtZSI6IkV4YW1wbGUgU2hvdyIsInNlYXNvbiI6MSwiZXBpc29kZXMiOlt7ImVwaXNvZGUiOjEsInRpdGxlIjoiUGlsb3QiLCJ1cmwiOiJodHRwczovL2Nkbi5leGFtcGxlL3Nob3cvczAxZTAxLm1wNCJ9LHsiZXBpc29kZSI6MiwidGl0bGUiOiJTZWNvbmQgRXBpc29kZSIsInVybCI6Imh0dHBzOi8vY2RuLmV4YW1wbGUvc2hvdy9zMDFlMDIubXA0In1dfX0='
```

To build your own payload: `printf '%s' '<json>' | base64 -w0`.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Nothing opens / `ActivityNotFoundException` | Fetcharr is installed; the package is `com.istarvin.fetcharr` |
| App opens but no sheet appears | Action is `VIEW`, the URI scheme is `fetcharr`, and the extra is named exactly `payload` |
| "Couldn't read the download request" | Base64 decodes to valid JSON; exactly one of `single`/`batch`; required fields are present |
| Episodes show up as separate sheets | Each `single` needs the same `showName` and `season`, plus an `episode` |
| Download fails on the server with 403/404 | Send the headers the stream needs in `headers` |
