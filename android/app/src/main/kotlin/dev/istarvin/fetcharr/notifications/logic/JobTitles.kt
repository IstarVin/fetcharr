package dev.istarvin.fetcharr.notifications.logic

import dev.istarvin.fetcharr.data.network.dto.JobDto

/** Human-readable job names (Android spec §5.3). Pure. */
object JobTitles {
    /**
     * "Show — S02E03" for episodes, "Title (Year)" for movies (just "Title" without a year).
     * Fallbacks, in order: show name alone, title, url host + last path segment, the raw url.
     */
    fun heading(job: JobDto): String {
        val show = job.showName?.trim().orEmpty()
        if (show.isNotEmpty()) {
            val season = job.season
            val episode = job.episode
            return if (season != null && episode != null) {
                "$show — S${pad(season)}E${pad(episode)}"
            } else {
                show
            }
        }
        val title = job.title?.trim().orEmpty()
        if (title.isNotEmpty()) {
            return job.year?.let { "$title ($it)" } ?: title
        }
        return fromUrl(job.url)
    }

    /** "Severance S02". Without a season just the show; without a show name "Batch". */
    fun batchHeading(showName: String?, season: Int?): String {
        val show = showName?.trim().orEmpty()
        return when {
            show.isEmpty() -> "Batch"
            season == null -> show
            else -> "$show S${pad(season)}"
        }
    }

    private fun pad(n: Int): String = n.toString().padStart(2, '0')

    private fun fromUrl(url: String): String {
        val noScheme = url.substringAfter("://", url).substringBefore('#').substringBefore('?')
        val host = noScheme.substringBefore('/').substringAfterLast('@').substringBefore(':')
        val last = noScheme.substringAfter('/', "").trimEnd('/').substringAfterLast('/')
        return when {
            host.isNotEmpty() && last.isNotEmpty() -> "$host/$last"
            host.isNotEmpty() -> host
            last.isNotEmpty() -> last
            else -> url
        }
    }
}
