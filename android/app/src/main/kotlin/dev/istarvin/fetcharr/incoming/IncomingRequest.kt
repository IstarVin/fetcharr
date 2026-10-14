package dev.istarvin.fetcharr.incoming

import java.util.Locale

/** A subtitle track attached to a request. [lang] is an ISO 639-1 code when the sender provided one. */
data class IncomingSubtitle(val url: String, val lang: String?)

/**
 * A decoded, validated `fetcharr://` download request. Mirrors the payload schema of the Intent Guide.
 * Pure Kotlin: no Android types.
 */
sealed interface IncomingRequest {
    /** The payload's root `category` hint (matched against server category ids). */
    val categoryHint: String?

    /** HTTP headers declared at the payload root. */
    val headers: Map<String, String>

    /** True for an episode `single` (showName + season + episode) or a `batch`. */
    val isEpisodic: Boolean

    data class Single(
        override val categoryHint: String?,
        val url: String,
        /** Optional only when [showName], [season] and [episode] are all present (decision D6). */
        val title: String?,
        val year: Int?,
        val showName: String?,
        val season: Int?,
        val episode: Int?,
        override val headers: Map<String, String>,
        val subtitles: List<IncomingSubtitle>,
    ) : IncomingRequest {
        override val isEpisodic: Boolean
            get() = !showName.isNullOrBlank() && season != null && episode != null
    }

    data class Batch(
        override val categoryHint: String?,
        val showName: String,
        val season: Int,
        val year: Int?,
        /** Root headers; each episode's own [BatchEpisode.headers] are merged on top by [toEpisodeEntries]. */
        override val headers: Map<String, String>,
        val episodes: List<BatchEpisode>,
    ) : IncomingRequest {
        override val isEpisodic: Boolean get() = true
    }
}

data class BatchEpisode(
    val episode: Int,
    val title: String?,
    val url: String,
    /** Optional per-episode headers (extension over the guide's schema); merged over the root headers. */
    val headers: Map<String, String>,
    val subtitles: List<IncomingSubtitle>,
)

/** One episode of a season, with headers already merged (root headers overridden by per-episode ones). */
data class EpisodeEntry(
    val episode: Int,
    val title: String?,
    val url: String,
    val headers: Map<String, String>,
    val subtitles: List<IncomingSubtitle>,
)

/** Identifies a season of a show; the show name is compared trimmed and case-insensitively. */
data class SeasonKey(val normalizedShowName: String, val season: Int) {
    companion object {
        fun normalize(showName: String): String = showName.trim().lowercase(Locale.ROOT)

        fun of(showName: String, season: Int) = SeasonKey(normalize(showName), season)
    }
}

/** Season identity for episodic requests, or null for movies / flat items. */
fun IncomingRequest.seasonKey(): SeasonKey? = when (this) {
    is IncomingRequest.Batch -> SeasonKey.of(showName, season)
    is IncomingRequest.Single ->
        if (isEpisodic) SeasonKey.of(showName.orEmpty(), season ?: 0) else null
}

/**
 * Normalizes an episodic request into episode entries for season mode.
 * A single episode yields one entry; a batch yields its episodes in payload order with the last
 * occurrence of a duplicated number winning. Non-episodic requests yield an empty list.
 */
fun IncomingRequest.toEpisodeEntries(): List<EpisodeEntry> = when (this) {
    is IncomingRequest.Single ->
        if (isEpisodic) {
            listOf(EpisodeEntry(episode ?: 0, title, url, headers, subtitles))
        } else {
            emptyList()
        }
    is IncomingRequest.Batch -> episodes
        .map { EpisodeEntry(it.episode, it.title, it.url, headers + it.headers, it.subtitles) }
        .associateBy { it.episode }
        .values
        .toList()
}
