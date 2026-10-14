package dev.istarvin.fetcharr.data.network

import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.TimeUnit

class ConnectionTesterTest {
    private lateinit var server: MockWebServer
    private val client = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(5, TimeUnit.SECONDS)
        .build()
    private val tester = ConnectionTester(FetcharrApiFactory(client, FetcharrJson))

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun config(token: String? = null) = ServerConfig(server.url("/").toString().trimEnd('/'), token)

    private fun test(token: String? = null) = runBlocking { tester.test(config(token)) }

    @Test
    fun connected() {
        server.enqueue(MockResponse().setResponseCode(200).setBody(Fixtures.text("info.json")))
        val r = test("tok") as TestConnectionResult.Connected
        assertEquals("1.0.0", r.version)
        assertTrue(r.latencyMs >= 0)
        val req = server.takeRequest()
        assertEquals("/api/info", req.path)
        assertEquals("Bearer tok", req.getHeader("Authorization"))
    }

    @Test
    fun wrongToken() {
        server.enqueue(MockResponse().setResponseCode(401).setBody(Fixtures.text("error-unauthorized.json")))
        assertEquals(TestConnectionResult.WrongToken, test("bad"))
    }

    @Test
    fun notFetcharrHtml() {
        server.enqueue(MockResponse().setResponseCode(200).setBody("<html><body>Welcome</body></html>"))
        assertEquals(TestConnectionResult.NotFetcharr, test())
    }

    @Test
    fun notFetcharrOtherJson() {
        server.enqueue(MockResponse().setResponseCode(200).setBody("""{"hello":"world"}"""))
        assertEquals(TestConnectionResult.NotFetcharr, test())
    }

    @Test
    fun notFetcharrNon2xxWithoutEnvelope() {
        server.enqueue(MockResponse().setResponseCode(404).setBody("<html>Not Found</html>"))
        assertEquals(TestConnectionResult.NotFetcharr, test())
    }

    @Test
    fun unreachable() {
        val cfg = config()
        server.shutdown()
        assertEquals(TestConnectionResult.Unreachable, runBlocking { tester.test(cfg) })
    }
}
