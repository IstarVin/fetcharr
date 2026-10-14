package dev.istarvin.fetcharr.data.network

import dev.istarvin.fetcharr.data.network.dto.CreateJobRequest
import dev.istarvin.fetcharr.data.network.dto.JobStatus
import dev.istarvin.fetcharr.data.network.dto.RetryRequest
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.TimeUnit

class FetcharrApiMockServerTest {
    private lateinit var server: MockWebServer
    private val client = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(300, TimeUnit.MILLISECONDS)
        .build()
    private val factory = FetcharrApiFactory(client, FetcharrJson)

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun baseUrl() = server.url("/").toString().trimEnd('/')

    private fun api(token: String? = null) = factory.create(ServerConfig(baseUrl(), token))

    private fun json(code: Int, body: String) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    private fun failure(r: ApiResult<*>): ApiError = (r as ApiResult.Failure).error

    @Test
    fun errorFixturesMapThroughSafeApiCall() {
        val cases = listOf(
            Triple("invalid_request", 400, { e: ApiError -> e is ApiError.BadRequest && e.code == "invalid_request" }),
            Triple("unauthorized", 401, { e: ApiError -> e == ApiError.Unauthorized }),
            Triple("not_found", 404, { e: ApiError -> e is ApiError.NotFound }),
            Triple("invalid_state", 409, { e: ApiError -> e is ApiError.InvalidState }),
            Triple("duplicate", 409, { e: ApiError -> e is ApiError.Duplicate }),
            Triple("ytdlp_unmanaged", 409, { e: ApiError -> e == ApiError.YtdlpUnmanaged }),
            Triple("ytdlp_busy", 409, { e: ApiError -> e == ApiError.YtdlpBusy }),
        )
        assertEquals(Fixtures.errorCodes.toSet(), cases.map { it.first }.toSet())
        for ((name, status, check) in cases) {
            server.enqueue(json(status, Fixtures.text("error-$name.json")))
            val result = runBlocking { safeApiCall { api().pauseJob("x") } }
            val error = failure(result)
            assertTrue("$name -> $error", check(error))
        }
    }

    @Test
    fun serverErrorsMapToServer() {
        server.enqueue(json(500, """{"error":{"code":"internal","message":"boom"}}"""))
        server.enqueue(MockResponse().setResponseCode(502).setBody("<html>Bad Gateway</html>"))
        assertEquals(ApiError.Server(500), failure(runBlocking { safeApiCall { api().info() } }))
        assertEquals(ApiError.Server(502), failure(runBlocking { safeApiCall { api().info() } }))
    }

    @Test
    fun successParsesBody() {
        server.enqueue(json(200, Fixtures.text("jobs-list.json")))
        val result = runBlocking { safeApiCall { api().listJobs(status = "pending,paused", limit = 10) } }
        assertEquals(7, (result as ApiResult.Success).value.jobs.size)
        val req = server.takeRequest()
        assertEquals("GET", req.method)
        assertEquals("/api/jobs?status=pending%2Cpaused&limit=10", req.path)
    }

    @Test
    fun nonJsonSuccessBodyIsInvalidResponse() {
        server.enqueue(MockResponse().setResponseCode(200).setBody("<html>hi</html>"))
        assertEquals(ApiError.InvalidResponse, failure(runBlocking { safeApiCall { api().info() } }))
    }

    @Test
    fun connectionRefusedIsUnreachable() {
        val url = baseUrl()
        server.shutdown()
        val result = runBlocking { safeApiCall { factory.create(ServerConfig(url, null)).info() } }
        assertEquals(ApiError.Unreachable, failure(result))
    }

    @Test
    fun readTimeoutIsUnreachable() {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
        assertEquals(ApiError.Unreachable, failure(runBlocking { safeApiCall { api().info() } }))
    }

    @Test
    fun bearerHeaderPresentWhenTokenSet() {
        server.enqueue(json(200, Fixtures.text("info.json")))
        runBlocking { api("secret-token").info() }
        assertEquals("Bearer secret-token", server.takeRequest().getHeader("Authorization"))
    }

    @Test
    fun bearerHeaderAbsentWhenTokenBlankOrNull() {
        server.enqueue(json(200, Fixtures.text("info.json")))
        server.enqueue(json(200, Fixtures.text("info.json")))
        runBlocking {
            api("   ").info()
            api(null).info()
        }
        assertNull(server.takeRequest().getHeader("Authorization"))
        assertNull(server.takeRequest().getHeader("Authorization"))
    }

    @Test
    fun trailingSlashInBaseUrlIsTolerated() {
        server.enqueue(json(200, Fixtures.text("info.json")))
        runBlocking { factory.create(ServerConfig(baseUrl() + "/", null)).info() }
        assertEquals("/api/info", server.takeRequest().path)
    }

    @Test
    fun createJobSendsRequestWithoutNulls() {
        server.enqueue(json(201, Fixtures.text("job-processing.json")))
        val job = runBlocking {
            api().createJob(CreateJobRequest(url = "https://a/b.m3u8", category = "movies", title = "T", year = 2024))
        }
        assertEquals(JobStatus.PROCESSING, job.status)
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertEquals("/api/jobs", req.path)
        assertEquals("""{"url":"https://a/b.m3u8","category":"movies","title":"T","year":2024}""", req.body.readUtf8())
    }

    @Test
    fun deleteAndRetryAndYtdlpEndpoints() {
        server.enqueue(MockResponse().setResponseCode(204))
        server.enqueue(json(200, """{"deleted":3}"""))
        server.enqueue(json(200, Fixtures.text("job-pending.json")))
        server.enqueue(json(200, Fixtures.text("job-pending.json")))
        server.enqueue(json(200, Fixtures.text("ytdlp-update.json")))
        server.enqueue(json(200, """{"jobs":[]}"""))
        runBlocking {
            val a = api()
            a.deleteJob("j1", deleteFile = true)
            assertEquals(3, a.clearHistory().deleted)
            a.retryJob("j1")
            a.retryJobWithBody("j1", RetryRequest(overwrite = true))
            assertEquals("2026.09.30", a.updateYtdlp().current)
            assertTrue(a.cancelBatch("b1").jobs.isEmpty())
        }
        assertEquals("DELETE /api/jobs/j1?delete_file=true", server.takeRequest().let { "${it.method} ${it.path}" })
        assertEquals("DELETE /api/jobs", server.takeRequest().let { "${it.method} ${it.path}" })
        val retry = server.takeRequest()
        assertEquals("POST /api/jobs/j1/retry", "${retry.method} ${retry.path}")
        val retryBody = server.takeRequest()
        assertEquals("""{"overwrite":true}""", retryBody.body.readUtf8())
        assertEquals("POST /api/system/ytdlp/update", server.takeRequest().let { "${it.method} ${it.path}" })
        assertEquals("POST /api/batches/b1/cancel", server.takeRequest().let { "${it.method} ${it.path}" })
    }

    @Test
    fun providerCachesPerConfigAndReturnsNullWithoutConfig() {
        val flow = kotlinx.coroutines.flow.MutableStateFlow<ServerConfig?>(null)
        val provider = FetcharrApiProvider(factory, object : ServerConfigProvider {
            override val config = flow
        })
        assertNull(provider.current())
        flow.value = ServerConfig(baseUrl(), "t")
        val first = provider.current()!!
        assertTrue(first === provider.current())
        flow.value = ServerConfig(baseUrl(), "other")
        assertTrue(first !== provider.current())
        flow.value = null
        assertNull(provider.current())
    }

    @Test
    fun serverConfigToStringHidesToken() {
        assertTrue(!ServerConfig("http://h", "abc123").toString().contains("abc123"))
    }
}
