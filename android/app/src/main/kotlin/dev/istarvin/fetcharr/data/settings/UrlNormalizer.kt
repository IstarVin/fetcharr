package dev.istarvin.fetcharr.data.settings

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Turns what the user typed into a server base URL (app spec §5.5). */
object UrlNormalizer {
    private val SCHEME = Regex("^[A-Za-z][A-Za-z0-9+.-]*://")

    /**
     * Trims, adds `http://` when there is no scheme, and removes trailing slashes.
     * Returns null when [input] is blank or not a valid http(s) URL.
     */
    fun normalize(input: String): String? {
        val trimmed = input.trim()
        if (trimmed.isEmpty()) return null
        val withScheme = if (SCHEME.containsMatchIn(trimmed)) trimmed else "http://$trimmed"
        val result = withScheme.trimEnd('/')
        return result.takeIf { it.toHttpUrlOrNull() != null }
    }
}
