package dev.istarvin.fetcharr.ui.incoming

import dev.istarvin.fetcharr.incoming.CategoryOption

/**
 * The editable text fields shared by the Incoming sheet and the Add screen. Everything is a string so the
 * text fields can hold half-typed values; the `*Value` helpers parse them.
 */
data class ItemForm(
    val title: String = "",
    val year: String = "",
    val show: String = "",
    val season: String = "",
    val episode: String = "",
    val episodeTitle: String = "",
) {
    /** Blank or a 4-digit number (the server requires 4 digits). */
    val yearValid: Boolean get() = year.isBlank() || YEAR.matches(year.trim())

    val yearValue: Int? get() = year.trim().takeIf { it.isNotEmpty() }?.toIntOrNull()
    val seasonValue: Int? get() = season.trim().toIntOrNull()?.takeIf { it >= 0 }
    val episodeValue: Int? get() = episode.trim().toIntOrNull()?.takeIf { it >= 0 }

    /** True when every field the category [layout] requires is filled (spec 5.1: movie/tv/flat). */
    fun isCompleteFor(layout: String): Boolean = when (layout) {
        CategoryOption.LAYOUT_MOVIE -> title.isNotBlank() && yearValid
        CategoryOption.LAYOUT_TV -> show.isNotBlank() && seasonValue != null && episodeValue != null && yearValid
        else -> title.isNotBlank()
    }

    /** The season layout needs Show and Season only; Year is optional but must be valid when given. */
    fun isCompleteForSeason(): Boolean = show.isNotBlank() && seasonValue != null && yearValid

    private companion object {
        val YEAR = Regex("""\d{4}""")
    }
}

/** http/https check used by the Add screen (the parser's own check is private). */
fun isHttpUrl(url: String): Boolean {
    val trimmed = url.trim()
    val rest = when {
        trimmed.startsWith("http://", ignoreCase = true) -> trimmed.substring(7)
        trimmed.startsWith("https://", ignoreCase = true) -> trimmed.substring(8)
        else -> return false
    }
    val authority = rest.takeWhile { it != '/' && it != '?' && it != '#' }
    return authority.isNotEmpty() && trimmed.none { it.isWhitespace() }
}
