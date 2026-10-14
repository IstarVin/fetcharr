package dev.istarvin.fetcharr.ui.incoming

import dev.istarvin.fetcharr.incoming.IncomingRequest
import dev.istarvin.fetcharr.incoming.IntentPayloadParser
import dev.istarvin.fetcharr.incoming.ParseResult

sealed interface RouteDecision {
    /** A valid `fetcharr://` request: hand it to the coordinator. */
    data class ShowRequest(val request: IncomingRequest) : RouteDecision

    /** A `fetcharr://` intent that could not be read: snackbar "Couldn't read the download request". */
    data object Unreadable : RouteDecision

    /** Open the Add screen, with [url] filled in when non-null. */
    data class OpenAdd(val url: String?) : RouteDecision

    /** Not one of ours (for example MAIN): do nothing. */
    data object Ignore : RouteDecision
}

/**
 * Decides what an incoming Android intent means, from plain strings (no Android types, no logging).
 *
 * - `ACTION_VIEW` + `fetcharr:` data: the Intent Guide sends the Base64 payload in the string extra `payload`
 *   ([payloadExtra]); a `?payload=` query parameter on the URI is accepted as a fallback.
 * - `ACTION_SEND` (text/plain): the first http(s) URL in [extraText] opens Add pre-filled; no URL opens it empty.
 */
class IntentRouter(private val parser: IntentPayloadParser = IntentPayloadParser()) {

    fun route(action: String?, dataString: String?, extraText: String?, payloadExtra: String? = null): RouteDecision =
        when (action) {
            ACTION_VIEW -> routeView(dataString, payloadExtra)
            ACTION_SEND -> RouteDecision.OpenAdd(firstHttpUrl(extraText))
            else -> RouteDecision.Ignore
        }

    private fun routeView(dataString: String?, payloadExtra: String?): RouteDecision {
        val data = dataString?.trim().orEmpty()
        if (!data.startsWith("$SCHEME:", ignoreCase = true)) return RouteDecision.Unreadable
        val result = if (!payloadExtra.isNullOrBlank()) parser.parsePayload(payloadExtra) else parser.parseUri(data)
        return when (result) {
            is ParseResult.Success -> RouteDecision.ShowRequest(result.request)
            is ParseResult.Failure -> RouteDecision.Unreadable
        }
    }

    companion object {
        const val ACTION_VIEW = "android.intent.action.VIEW"
        const val ACTION_SEND = "android.intent.action.SEND"
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
