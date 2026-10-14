package dev.istarvin.fetcharr.ui.common

import dev.istarvin.fetcharr.data.network.dto.JobDto

/** Job naming shared by the Queue and History screens. */

/** True for a TV episode: a show name plus a season or episode number. */
fun JobDto.isEpisode(): Boolean = !showName.isNullOrBlank() && (season != null || episode != null)

/**
 * `Show — S02E03` for episodes, otherwise the title (falling back to the show name, then the URL) with ` (Year)`
 * whenever the server sent a year (app spec §5.3), whatever the category layout. [layout] is kept for callers that
 * know it; it does not change the heading.
 */
@Suppress("UNUSED_PARAMETER")
fun JobDto.heading(layout: String?): String {
    if (isEpisode()) {
        val code = buildString {
            if (season != null) append("S").append(season.toString().padStart(2, '0'))
            if (episode != null) append("E").append(episode.toString().padStart(2, '0'))
        }
        return "${showName!!.trim()} — $code"
    }
    val name = title?.trim()?.takeIf { it.isNotEmpty() } ?: showName?.trim()?.takeIf { it.isNotEmpty() } ?: url
    return if (year != null) "$name ($year)" else name
}
