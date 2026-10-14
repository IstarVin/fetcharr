package dev.istarvin.fetcharr.ui.incoming

import dev.istarvin.fetcharr.incoming.IncomingSubtitle
import java.net.URI

/** The read-only line of the single layout: "<host> · 2 subtitles: en, es" (spec 5.1). */
object SourceLine {
    fun of(url: String, subtitles: List<IncomingSubtitle>): String {
        val host = hostOf(url)
        if (subtitles.isEmpty()) return host
        val langs = subtitles.mapNotNull { it.lang }
        val noun = if (subtitles.size == 1) "subtitle" else "subtitles"
        val tail = if (langs.isEmpty()) "${subtitles.size} $noun" else "${subtitles.size} $noun: ${langs.joinToString(", ")}"
        return "$host · $tail"
    }

    fun hostOf(url: String): String =
        try {
            URI(url.trim()).host
        } catch (_: Exception) {
            null
        } ?: url.trim().substringAfter("://").substringBefore('/').substringBefore('?').substringBefore('#')
}
