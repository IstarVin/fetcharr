# Intent fixtures

Each file is the decoded JSON payload (the content inside the base64 `payload` extra). `valid-*` must parse; `invalid-*` must be rejected with "Couldn't read the download request".

## Valid

| File | Covers |
| --- | --- |
| `valid-movie.json` | `single` with category hint, title, year |
| `valid-single-minimal.json` | Only required fields (`url`, `title`), no category |
| `valid-tv-episode-headers-subtitles.json` | Episode with root `headers` and two subtitles (guide example) |
| `valid-single-episode-no-title.json` | `title` omitted because `showName`, `season`, `episode` are all set (D6) |
| `valid-single-episode-specials-zero.json` | `season` 0 and `episode` 0 are allowed (≥0) |
| `valid-batch-season.json` | `batch` with year, headers, per-episode subtitles, an episode without title |
| `valid-batch-no-category.json` | `batch` with no `category`, no `year` |
| `valid-unknown-keys-ignored.json` | Unknown keys at every level are ignored |

## Invalid

| File | Rule violated / expected rejection reason |
| --- | --- |
| `invalid-neither-single-nor-batch.json` | Neither `single` nor `batch` |
| `invalid-both-single-and-batch.json` | Both `single` and `batch` |
| `invalid-single-missing-url.json` | `single.url` missing |
| `invalid-single-url-not-http.json` | `single.url` scheme is `ftp`, not http/https |
| `invalid-single-url-no-scheme.json` | `single.url` has no scheme |
| `invalid-single-missing-title.json` | `title` missing and not an episode |
| `invalid-single-no-title-partial-episode.json` | `title` missing; `showName`+`season` set but `episode` missing, so the D6 exemption does not apply |
| `invalid-single-negative-season.json` | `season` < 0 |
| `invalid-single-negative-episode.json` | `episode` < 0 |
| `invalid-single-subtitle-url-not-http.json` | Subtitle `url` is `file://` |
| `invalid-single-subtitle-missing-url.json` | Subtitle `url` missing |
| `invalid-batch-missing-showName.json` | `batch.showName` missing |
| `invalid-batch-missing-season.json` | `batch.season` missing |
| `invalid-batch-missing-episodes.json` | `batch.episodes` missing |
| `invalid-batch-empty-episodes.json` | `batch.episodes` is empty (needs ≥1) |
| `invalid-batch-episode-missing-episode.json` | Episode entry has no `episode` number |
| `invalid-batch-episode-missing-url.json` | Episode entry has no `url` |
| `invalid-batch-episode-url-not-http.json` | Episode `url` scheme is `rtsp` |
| `invalid-batch-episode-subtitle-url-not-http.json` | Episode subtitle `url` is `ftp` |

## Rules not representable as decoded JSON

These need tests built from raw intents or strings, not fixtures: wrong action, wrong URI scheme, missing `payload` extra, `payload` not valid Base64, decoded text not valid JSON.
