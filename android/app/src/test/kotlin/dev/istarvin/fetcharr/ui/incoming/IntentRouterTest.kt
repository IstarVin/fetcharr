package dev.istarvin.fetcharr.ui.incoming

import dev.istarvin.fetcharr.incoming.IncomingRequest
import java.util.Base64
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class IntentRouterTest {
    private val router = IntentRouter()
    private val view = "android.intent.action.VIEW"
    private val send = "android.intent.action.SEND"

    private fun b64(json: String) = Base64.getEncoder().encodeToString(json.toByteArray())

    private val movieJson = """{"category":"movies","single":{"url":"https://cdn.example/m.mp4","title":"Movie","year":2026}}"""

    @Test
    fun viewWithPayloadExtraShowsRequest() {
        val d = router.route(view, "fetcharr://download", null, b64(movieJson))
        val request = (d as RouteDecision.ShowRequest).request as IncomingRequest.Single
        assertEquals("Movie", request.title)
        assertEquals("movies", request.categoryHint)
    }

    @Test
    fun viewWithPayloadQueryParameterIsAcceptedAsFallback() {
        val enc = java.net.URLEncoder.encode(b64(movieJson), "UTF-8")
        val d = router.route(view, "fetcharr://download?payload=$enc", null, null)
        assertTrue(d is RouteDecision.ShowRequest)
    }

    @Test
    fun badPayloadIsUnreadable() {
        assertEquals(RouteDecision.Unreadable, router.route(view, "fetcharr://download", null, "!!not base64!!"))
        assertEquals(RouteDecision.Unreadable, router.route(view, "fetcharr://download", null, b64("""{"x":1}""")))
        assertEquals(RouteDecision.Unreadable, router.route(view, "fetcharr://download", null, null))
        assertEquals(RouteDecision.Unreadable, router.route(view, null, null, b64(movieJson)))
    }

    @Test
    fun wrongSchemeIsUnreadable() {
        assertEquals(RouteDecision.Unreadable, router.route(view, "https://example.com/", null, b64(movieJson)))
    }

    @Test
    fun wrongOrMissingActionIsIgnored() {
        assertEquals(RouteDecision.Ignore, router.route("android.intent.action.DELETE", "fetcharr://download", null, b64(movieJson)))
        assertEquals(RouteDecision.Ignore, router.route("android.intent.action.MAIN", null, null, null))
        assertEquals(RouteDecision.Ignore, router.route(null, "fetcharr://download", "https://a.example", b64(movieJson)))
    }

    @Test
    fun sendOpensAddWithFirstUrl() {
        val text = "Watch this: https://example.com/video.m3u8?x=1, or https://other.example/b"
        assertEquals(RouteDecision.OpenAdd("https://example.com/video.m3u8?x=1"), router.route(send, null, text))
    }

    @Test
    fun sendWithoutUrlOpensAddEmpty() {
        assertEquals(RouteDecision.OpenAdd(null), router.route(send, null, "no link here ftp://x"))
        assertEquals(RouteDecision.OpenAdd(null), router.route(send, null, null))
        assertEquals(RouteDecision.OpenAdd(null), router.route(send, null, "   "))
    }

    @Test
    fun urlExtractionStripsTrailingPunctuationAndKeepsCase() {
        assertEquals("HTTP://Example.com/A", IntentRouter.firstHttpUrl("see (HTTP://Example.com/A)."))
        assertEquals("http://a.example/x", IntentRouter.firstHttpUrl("\"http://a.example/x\""))
        assertNull(IntentRouter.firstHttpUrl("http://"))
    }
}
