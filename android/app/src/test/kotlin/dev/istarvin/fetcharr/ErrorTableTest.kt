package dev.istarvin.fetcharr

import dev.istarvin.fetcharr.data.network.ApiError
import dev.istarvin.fetcharr.data.network.ApiResult
import dev.istarvin.fetcharr.data.network.FetcharrApiFactory
import dev.istarvin.fetcharr.data.network.FetcharrJson
import dev.istarvin.fetcharr.data.network.Fixtures
import dev.istarvin.fetcharr.data.network.ServerConfig
import dev.istarvin.fetcharr.data.network.safeApiCall
import dev.istarvin.fetcharr.di.NetworkModule
import dev.istarvin.fetcharr.ui.incoming.HubEvent
import dev.istarvin.fetcharr.ui.incoming.IncomingHub
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.Base64
import java.util.concurrent.TimeUnit

/**
 * App spec section 7, one row at a time, at the mapping level: a server reply (or the lack of one) goes
 * through the real Retrofit stack and `safeApiCall`, and the resulting [ApiError] must carry the exact
 * user-facing text. Where the UI shows that text (snackbar, inline) is covered by the view-model tests.
 */
class ErrorTableTest {
    private lateinit var server: MockWebServer

    // Short read timeout so the "no response" row finishes fast; the real values are asserted separately.
    private val client = OkHttpClient.Builder()
        .connectTimeout(2, TimeUnit.SECONDS)
        .readTimeout(300, TimeUnit.MILLISECONDS)
        .build()

    @Before
    fun setUp() {
        server = MockWebServer().also { it.start() }
    }

    @After
    fun tearDown() {
        runCatching { server.shutdown() }
    }

    private fun json(code: Int, body: String) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    private fun callInfo(): ApiError = runBlocking {
        val api = FetcharrApiFactory(client, FetcharrJson).create(ServerConfig(server.url("/").toString().trimEnd('/'), "tok"))
        (safeApiCall { api.info() } as ApiResult.Failure).error
    }

    private class Row(val name: String, val reply: MockResponse?, val expected: (ApiError) -> Boolean, val text: String)

    @Test
    fun serverRepliesMapToTheTableText() {
        val rows = listOf(
            Row("unreachable: connection dropped", MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START), { it == ApiError.Unreachable }, "Can't reach server"),
            Row("unreachable: read timeout", MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE), { it == ApiError.Unreachable }, "Can't reach server"),
            Row("401", json(401, Fixtures.text("error-unauthorized.json")), { it == ApiError.Unauthorized }, "Wrong API token"),
            Row("401 without an envelope", MockResponse().setResponseCode(401), { it == ApiError.Unauthorized }, "Wrong API token"),
            Row(
                "400 shows the server message",
                json(400, Fixtures.text("error-invalid_request.json")),
                { it is ApiError.BadRequest },
                "category 'foo' does not exist",
            ),
            Row("409 invalid_state", json(409, Fixtures.text("error-invalid_state.json")), { it is ApiError.InvalidState }, "Job already changed"),
            Row("500 internal", json(500, """{"error":{"code":"internal","message":"boom"}}"""), { it is ApiError.Server }, "Server error — try again"),
            Row("502 from a proxy", MockResponse().setResponseCode(502).setBody("<html>Bad gateway</html>"), { it is ApiError.Server }, "Server error — try again"),
            Row("503 empty body", MockResponse().setResponseCode(503), { it is ApiError.Server }, "Server error — try again"),
        )
        for (row in rows) {
            server.enqueue(row.reply!!)
            val error = callInfo()
            assertTrue("${row.name}: got $error", row.expected(error))
            assertEquals(row.name, row.text, error.userMessage)
        }
    }

    @Test
    fun requestsUseTenSecondConnectAndThirtySecondReadTimeouts() {
        val real = NetworkModule.provideOkHttpClient()
        assertEquals(10_000, real.connectTimeoutMillis)
        assertEquals(30_000, real.readTimeoutMillis)
    }

    @Test
    fun badIntentPayloadsShowTheUnreadableSnackbar() {
        fun b64(s: String) = Base64.getEncoder().encodeToString(s.toByteArray())
        val scope = CoroutineScope(Job() + Dispatchers.Unconfined)
        try {
            val payloads = listOf(
                "!!not base64!!",
                b64("""{"x":1}"""),
                b64(Fixtures.text("intent/invalid-single-missing-url.json")),
                b64(Fixtures.text("intent/invalid-both-single-and-batch.json")),
                null,
            )
            for (payload in payloads) {
                val hub = IncomingHub(scope)
                hub.handleIntent("android.intent.action.VIEW", "fetcharr://download", null, payload)
                val event = runBlocking { withTimeout(2_000) { hub.events.first() } }
                assertEquals("payload=$payload", HubEvent.Snackbar("Couldn't read the download request"), event)
            }
        } finally {
            scope.cancel()
        }
    }
}
