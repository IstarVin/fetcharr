package com.istarvin.fetcharr.ui.incoming

import com.istarvin.fetcharr.data.settings.UrlNormalizer
import com.istarvin.fetcharr.incoming.IncomingRequest
import com.istarvin.fetcharr.incoming.IntentPayloadParser
import com.istarvin.fetcharr.incoming.ParseResult
import java.net.URLDecoder

sealed interface RouteDecision {
    /** A valid `fetcharr://` request: hand it to the coordinator. */
    data class ShowRequest(val request: IncomingRequest) : RouteDecision

    /** A `fetcharr://` intent that could not be read: snackbar "Couldn't read the download request". */
    data object Unreadable : RouteDecision

    /** Open the Add screen, with [url] filled in when non-null. */
    data class OpenAdd(val url: String?) : RouteDecision

    /** A `fetcharr://connect` pairing link (from the web UI's QR code): offer to save this server. */
    data class Connect(val url: String, val token: String) : RouteDecision

    /** A `fetcharr://connect` link without a valid server address. */
    data object BadConnectLink : RouteDecision

    /** Not one of ours (for example MAIN): do nothing. */
    data object Ignore : RouteDecision
}

/**
 * Decides what an incoming Android intent means, from plain strings (no Android types, no logging).
 *
 * - `ACTION_VIEW` + `fetcharr:` data: the Intent Guide sends the Base64 payload in the string extra `payload`
 *   ([payloadExtra]); a `?payload=` query parameter on the URI is accepted as a fallback.
 * - `ACTION_SEND` (text/plain): the first http(s) URL in [extraText] opens Add pre-filled; no URL opens it empty.
 * - `fetcharr://connect?url=…&token=…` (no payload): a pairing link; the app asks before saving it.
 * - [ACTION_ADD_DOWNLOAD] (launcher shortcut, Quick Settings tile): opens Add empty.
 */
class IntentRouter(private val parser: IntentPayloadParser = IntentPayloadParser()) {

    fun route(action: String?, dataString: String?, extraText: String?, payloadExtra: String? = null): RouteDecision =
        when (action) {
            ACTION_VIEW -> routeView(dataString, payloadExtra)
            ACTION_SEND -> RouteDecision.OpenAdd(firstHttpUrl(extraText))
            ACTION_MAIN -> RouteDecision.Ignore
            ACTION_ADD_DOWNLOAD -> RouteDecision.OpenAdd(null)
            // Any other action that still carries a fetcharr URI or a payload is a rejected request (Intent Guide:
            // wrong action -> "Couldn't read the download request"); anything else is not ours.
            else -> if (carriesFetcharrData(dataString, payloadExtra)) RouteDecision.Unreadable else RouteDecision.Ignore
        }

    private fun carriesFetcharrData(dataString: String?, payloadExtra: String?): Boolean =
        !payloadExtra.isNullOrBlank() || dataString?.trim()?.startsWith("$SCHEME:", ignoreCase = true) == true

    private fun routeView(dataString: String?, payloadExtra: String?): RouteDecision {
        val data = dataString?.trim().orEmpty()
        if (!data.startsWith("$SCHEME:", ignoreCase = true)) return RouteDecision.Unreadable
        if (payloadExtra.isNullOrBlank()) parseConnect(data)?.let { return it }
        val result = if (!payloadExtra.isNullOrBlank()) parser.parsePayload(payloadExtra) else parser.parseUri(data)
        return when (result) {
            is ParseResult.Success -> RouteDecision.ShowRequest(result.request)
            is ParseResult.Failure -> RouteDecision.Unreadable
        }
    }

    /** Null when [data] is not a connect link, so it is read as a download request. */
    private fun parseConnect(data: String): RouteDecision? {
        val rest = data.substring(SCHEME.length + 1).removePrefix("//")
        val host = rest.takeWhile { it != '/' && it != '?' && it != '#' }
        if (!host.equals(CONNECT_HOST, ignoreCase = true)) return null
        val query = rest.substringBefore('#').substringAfter('?', "")
        val params = query.split('&').filter { it.isNotEmpty() }.associate { pair ->
            val name = decode(pair.substringBefore('='))
            name to decode(pair.substringAfter('=', ""))
        }
        if (params.containsKey("payload")) return null
        val url = params["url"]?.let(UrlNormalizer::normalize) ?: return RouteDecision.BadConnectLink
        return RouteDecision.Connect(url, params["token"]?.trim().orEmpty())
    }

    private fun decode(value: String): String = try {
        URLDecoder.decode(value, "UTF-8")
    } catch (_: IllegalArgumentException) {
        ""
    }

    companion object {
        const val ACTION_VIEW = "android.intent.action.VIEW"
        const val ACTION_ADD_DOWNLOAD = "com.istarvin.fetcharr.action.ADD_DOWNLOAD"
        private const val CONNECT_HOST = "connect"
        const val ACTION_SEND = "android.intent.action.SEND"
        const val ACTION_MAIN = "android.intent.action.MAIN"
        const val SCHEME = "fetcharr"

        private val URL = Regex("""https?://[^\s<>"']+""", RegexOption.IGNORE_CASE)
        private const val TRAILING = ".,;:!?)]}"

        /** The first `http(s)` URL in [text], without trailing sentence punctuation; null if there is none. */
        fun firstHttpUrl(text: String?): String? {
            if (text.isNullOrBlank()) return null
            val match = URL.find(text)?.value?.trimEnd { it in TRAILING } ?: return null
            return match.takeIf { isHttpUrl(it) }
        }
    }
}
