package com.istarvin.fetcharr.ui.common

import com.istarvin.fetcharr.data.network.dto.JobDto

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
    val name = title?.trim()?.takeIf { it.isNotEmpty() }?.let(::tidyReleaseName)
        ?: showName?.trim()?.takeIf { it.isNotEmpty() } ?: url
    return if (year != null) "$name ($year)" else name
}

private val releaseTags = Regex(
    """(?i)^(?:(?:19|20)\d\d|\d{3,4}p|4k|uhd|web-?dl|web-?rip|blu-?ray|bdrip|brrip|hdrip|dvdrip|hdtv|remux|x26[45]|h\.?26[45]|hevc|av1|aac|ddp?\d?|atmos|mul[a-z]*|dual|ma|amzn|nf|dsnp|hmax|10bit)$""",
)

/**
 * Scene-style names (`Spider-Man.Brand.New.Day.2026.2160p.MA.WEB-DL`) read as a title: dots become spaces and the
 * name stops at the first year or quality tag. Names that already contain spaces are left alone. The server keeps
 * the original; only the display changes.
 */
internal fun tidyReleaseName(raw: String): String {
    if (raw.any(Char::isWhitespace) || raw.count { it == '.' || it == '_' } < 2) return raw
    val words = raw.split('.', '_').filter { it.isNotEmpty() }
    val cut = words.indexOfFirst { releaseTags.matches(it) }
    val kept = if (cut > 0) words.subList(0, cut) else words
    return kept.joinToString(" ").ifBlank { raw }
}
