package dev.istarvin.fetcharr.incoming

import java.io.ByteArrayOutputStream
import java.util.Base64
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

sealed interface ParseResult {
    data class Success(val request: IncomingRequest) : ParseResult

    /** [reason] is for tests/diagnostics only and never contains payload content. The UI shows a fixed message. */
    data class Failure(val reason: String) : ParseResult
}

/**
 * Parses and validates `fetcharr://` download payloads (Intent Guide). Never logs input.
 * No Android dependencies: the URI query is handled manually.
 */
class IntentPayloadParser {

    /** Parses a `fetcharr://...?payload=<base64>` URI string. Host and path are not restricted. */
    fun parseUri(uriString: String): ParseResult {
        val trimmed = uriString.trim()
        val colon = trimmed.indexOf(':')
        if (colon <= 0 || !trimmed.substring(0, colon).equals(SCHEME, ignoreCase = true)) {
            return fail("wrong scheme")
        }
        val beforeFragment = trimmed.substringBefore('#')
        val query = if (beforeFragment.contains('?')) beforeFragment.substringAfter('?') else ""
        var payload: String? = null
        for (pair in query.split('&')) {
            if (pair.isEmpty()) continue
            val name = pair.substringBefore('=')
            val value = if (pair.contains('=')) pair.substringAfter('=') else ""
            val decodedName = percentDecode(name) ?: return fail("malformed percent-encoding")
            if (decodedName == PAYLOAD_PARAM) {
                payload = percentDecode(value) ?: return fail("malformed percent-encoding")
                break
            }
        }
        if (payload.isNullOrBlank()) return fail("missing payload parameter")
        return parsePayload(payload)
    }

    /** Parses the Base64 (standard or URL-safe, padding optional) of the UTF-8 JSON payload. */
    fun parsePayload(base64: String): ParseResult {
        val bytes = decodeBase64(base64) ?: return fail("payload is not valid Base64")
        val text = String(bytes, Charsets.UTF_8).removePrefix(BOM.toString())
        return parseJson(text)
    }

    /** Parses and validates the decoded JSON payload text. */
    fun parseJson(json: String): ParseResult {
        val root = try {
            Json.parseToJsonElement(json)
        } catch (_: Exception) {
            return fail("payload is not valid JSON")
        }
        return try {
            ParseResult.Success(buildRequest(root))
        } catch (e: Invalid) {
            ParseResult.Failure(e.reason)
        }
    }

    // ---- validation -------------------------------------------------------------------------

    private class Invalid(val reason: String) : Exception(reason)

    private fun bad(reason: String): Nothing = throw Invalid(reason)

    private fun buildRequest(root: JsonElement): IncomingRequest {
        if (root !is JsonObject) bad("payload root is not an object")
        val category = optString(root, "category", "category")
        val headers = optHeaders(root, "headers", "headers")
        val single = root.present("single")
        val batch = root.present("batch")
        return when {
            single != null && batch != null -> bad("both single and batch present")
            single != null -> buildSingle(single, category, headers)
            batch != null -> buildBatch(batch, category, headers)
            else -> bad("neither single nor batch present")
        }
    }

    private fun buildSingle(
        element: JsonElement,
        category: String?,
        headers: Map<String, String>,
    ): IncomingRequest.Single {
        if (element !is JsonObject) bad("single is not an object")
        val url = requireUrl(element, "url", "single.url")
        val title = optString(element, "title", "single.title")?.takeIf { it.isNotBlank() }
        val showName = optString(element, "showName", "single.showName")?.takeIf { it.isNotBlank() }
        val season = optNonNegativeInt(element, "season", "single.season")
        val episode = optNonNegativeInt(element, "episode", "single.episode")
        val year = optInt(element, "year", "single.year")
        val subtitles = optSubtitles(element, "single.subtitles")
        val episodic = showName != null && season != null && episode != null
        if (title == null && !episodic) bad("single.title missing")
        return IncomingRequest.Single(category, url, title, year, showName, season, episode, headers, subtitles)
    }

    private fun buildBatch(
        element: JsonElement,
        category: String?,
        headers: Map<String, String>,
    ): IncomingRequest.Batch {
        if (element !is JsonObject) bad("batch is not an object")
        val showName = optString(element, "showName", "batch.showName")?.takeIf { it.isNotBlank() }
            ?: bad("batch.showName missing")
        val season = optNonNegativeInt(element, "season", "batch.season") ?: bad("batch.season missing")
        val year = optInt(element, "year", "batch.year")
        val episodesElement = element.present("episodes") ?: bad("batch.episodes missing")
        if (episodesElement !is JsonArray) bad("batch.episodes is not an array")
        if (episodesElement.isEmpty()) bad("batch.episodes is empty")
        val episodes = episodesElement.mapIndexed { index, item ->
            if (item !is JsonObject) bad("batch.episodes[$index] is not an object")
            val path = "batch.episodes[$index]"
            BatchEpisode(
                episode = optNonNegativeInt(item, "episode", "$path.episode") ?: bad("$path.episode missing"),
                title = optString(item, "title", "$path.title")?.takeIf { it.isNotBlank() },
                url = requireUrl(item, "url", "$path.url"),
                headers = optHeaders(item, "headers", "$path.headers"),
                subtitles = optSubtitles(item, "$path.subtitles"),
            )
        }
        return IncomingRequest.Batch(category, showName, season, year, headers, episodes)
    }

    private fun JsonObject.present(key: String): JsonElement? = this[key]?.takeIf { it !is JsonNull }

    private fun optString(obj: JsonObject, key: String, path: String): String? {
        val el = obj.present(key) ?: return null
        if (el !is JsonPrimitive || !el.isString) bad("$path is not a string")
        return el.content
    }

    private fun optInt(obj: JsonObject, key: String, path: String): Int? {
        val el = obj.present(key) ?: return null
        if (el !is JsonPrimitive || el.isString) bad("$path is not an integer")
        return el.content.toIntOrNull() ?: bad("$path is not an integer")
    }

    private fun optNonNegativeInt(obj: JsonObject, key: String, path: String): Int? {
        val value = optInt(obj, key, path) ?: return null
        if (value < 0) bad("$path is negative")
        return value
    }

    private fun requireUrl(obj: JsonObject, key: String, path: String): String {
        val url = optString(obj, key, path)?.trim()
        if (url.isNullOrEmpty()) bad("$path missing")
        if (!isHttpUrl(url)) bad("$path is not an http/https URL")
        return url
    }

    private fun optHeaders(obj: JsonObject, key: String, path: String): Map<String, String> {
        val el = obj.present(key) ?: return emptyMap()
        if (el !is JsonObject) bad("$path is not an object")
        val out = LinkedHashMap<String, String>()
        for ((name, value) in el) {
            if (value !is JsonPrimitive || !value.isString) bad("$path values must be strings")
            out[name] = value.content
        }
        return out
    }

    private fun optSubtitles(obj: JsonObject, path: String): List<IncomingSubtitle> {
        val el = obj.present("subtitles") ?: return emptyList()
        if (el !is JsonArray) bad("$path is not an array")
        return el.mapIndexed { index, item ->
            if (item !is JsonObject) bad("$path[$index] is not an object")
            IncomingSubtitle(
                url = requireUrl(item, "url", "$path[$index].url"),
                lang = optString(item, "lang", "$path[$index].lang")?.takeIf { it.isNotBlank() },
            )
        }
    }

    private fun isHttpUrl(url: String): Boolean {
        val lower = url.lowercase()
        val rest = when {
            lower.startsWith("http://") -> url.substring(7)
            lower.startsWith("https://") -> url.substring(8)
            else -> return false
        }
        val authority = rest.takeWhile { it != '/' && it != '?' && it != '#' }
        return authority.isNotEmpty() && authority.none { it.isWhitespace() }
    }

    // ---- encoding helpers -------------------------------------------------------------------

    private fun fail(reason: String) = ParseResult.Failure(reason)

    /** Standard or URL-safe alphabet (even mixed), whitespace ignored, padding optional. */
    private fun decodeBase64(input: String): ByteArray? {
        val normalized = buildString(input.length) {
            for (c in input) {
                when {
                    c.isWhitespace() || c == '=' -> Unit
                    c == '-' -> append('+')
                    c == '_' -> append('/')
                    else -> append(c)
                }
            }
        }
        if (normalized.isEmpty()) return null
        return try {
            Base64.getDecoder().decode(normalized)
        } catch (_: IllegalArgumentException) {
            null
        }
    }

    /** Percent-decodes UTF-8; unlike form decoding, '+' stays literal (it is part of the Base64 alphabet). */
    private fun percentDecode(s: String): String? {
        if (!s.contains('%')) return s
        val out = ByteArrayOutputStream(s.length)
        var i = 0
        while (i < s.length) {
            if (s[i] == '%') {
                if (i + 2 >= s.length) return null
                val hi = Character.digit(s[i + 1], 16)
                val lo = Character.digit(s[i + 2], 16)
                if (hi < 0 || lo < 0) return null
                out.write(hi * 16 + lo)
                i += 3
            } else {
                val cp = s.codePointAt(i)
                out.write(String(Character.toChars(cp)).toByteArray(Charsets.UTF_8))
                i += Character.charCount(cp)
            }
        }
        return String(out.toByteArray(), Charsets.UTF_8)
    }

    private companion object {
        const val BOM = '\uFEFF'
        const val SCHEME = "fetcharr"
        const val PAYLOAD_PARAM = "payload"
    }
}
