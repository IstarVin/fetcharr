package com.istarvin.fetcharr.notifications

import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.Fixtures
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.ServerConfigProvider
import com.istarvin.fetcharr.data.network.dto.Rfc3339
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertFalse
import org.junit.Before
import org.junit.Test
import java.util.concurrent.TimeUnit

class ApiJobsGatewayTest {
    private lateinit var server: MockWebServer
    private val config = MutableStateFlow<ServerConfig?>(null)
    private lateinit var gateway: ApiJobsGateway

    @Before
    fun setUp() {
        server = MockWebServer().also { it.start() }
        config.value = ServerConfig(server.url("/").toString().trimEnd('/'), null)
        val client = OkHttpClient.Builder().connectTimeout(5, TimeUnit.SECONDS).readTimeout(2, TimeUnit.SECONDS).build()
        val provider = FetcharrApiProvider(
            FetcharrApiFactory(client, FetcharrJson),
            object : ServerConfigProvider {
                override val config = this@ApiJobsGatewayTest.config
            },
        )
        gateway = ApiJobsGateway(provider)
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    /** A jobs page built from the completed-job fixture, with each job given one of [ids]. */
    private fun page(ids: List<String>, total: Int): MockResponse {
        val job = FetcharrJson.parseToJsonElement(Fixtures.text("job-completed.json")).toString()
        val fixtureId = Regex("\"id\":\"([^\"]+)\"").find(job)!!.groupValues[1]
        val jobs = ids.joinToString(",") { job.replace(fixtureId, it) }
        return MockResponse().setResponseCode(200).setHeader("Content-Type", "application/json")
            .setBody("""{"jobs":[$jobs],"total":$total}""")
    }

    @Test
    fun finishedAfterSendsTheStatusesAndTimeAndPages() = runBlocking {
        server.enqueue(page(listOf("00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"), total = 3))
        server.enqueue(page(listOf("00000000-0000-0000-0000-000000000003"), total = 3))

        val jobs = gateway.listFinishedAfter(1_700_000_000_123L)!!

        assertEquals(3, jobs.size)
        val first = server.takeRequest()
        assertEquals("/api/jobs", first.requestUrl!!.encodedPath)
        assertEquals("completed,failed,cancelled", first.requestUrl!!.queryParameter("status"))
        assertEquals(Rfc3339.format(1_700_000_000_123L), first.requestUrl!!.queryParameter("finished_after"))
        assertEquals("0", first.requestUrl!!.queryParameter("offset"))
        assertEquals("2", server.takeRequest().requestUrl!!.queryParameter("offset"))
    }

    @Test
    fun batchFetchUsesBatchIdWithTheMaximumLimit() = runBlocking {
        server.enqueue(page(listOf("00000000-0000-0000-0000-000000000001"), total = 1))
        assertEquals(1, gateway.listBatch("b-1")!!.size)
        val url = server.takeRequest().requestUrl!!
        assertEquals("b-1", url.queryParameter("batch_id"))
        assertEquals("500", url.queryParameter("limit"))
    }

    @Test
    fun historyBeyondTenPagesAndBatchBeyondFiveHundredAreComplete() = runBlocking {
        val historyIds = (1..2_001).map { "history-$it" }
        historyIds.chunked(200).forEach { server.enqueue(page(it, historyIds.size)) }
        assertEquals(historyIds, gateway.listFinishedAfter(0)!!.map { it.id })
        repeat(11) { server.takeRequest() }

        val batchIds = (1..501).map { "batch-$it" }
        batchIds.chunked(500).forEach { server.enqueue(page(it, batchIds.size)) }
        assertEquals(batchIds, gateway.listBatch("large")!!.map { it.id })
        assertEquals("0", server.takeRequest().requestUrl!!.queryParameter("offset"))
        assertEquals("500", server.takeRequest().requestUrl!!.queryParameter("offset"))
    }

    @Test
    fun pageErrorsAndPrematureEmptyPagesNeverReturnPartialResults() = runBlocking {
        server.enqueue(page(listOf("first"), total = 2))
        server.enqueue(MockResponse().setResponseCode(500))
        assertNull(gateway.listFinishedAfter(0))
        server.enqueue(page(listOf("first"), total = 2))
        server.enqueue(page(emptyList(), total = 2))
        assertNull(gateway.listBatch("incomplete"))
    }

    @Test
    fun connectionChangedDuringDelayedResponseInvalidatesHistoryAndBatchSession() = runBlocking {
        val session = gateway.captureSession()!!
        assertSame(session.connectionIdentity, gateway.captureSession()!!.connectionIdentity)
        server.enqueue(page(listOf("old-server-job"), total = 1).setBodyDelay(200, TimeUnit.MILLISECONDS))
        val result = async(Dispatchers.IO) { session.listFinishedAfter(0) }
        server.takeRequest(5, TimeUnit.SECONDS)!!
        // Even a token change on the same URL replaces the connection generation.
        config.value = config.value!!.copy(token = "new-token")
        assertNull(result.await())
        assertFalse(session.isCurrent())
        assertNotSame(session.connectionIdentity, gateway.captureSession()!!.connectionIdentity)
        assertNull(session.listBatch("old-batch"))
        assertEquals(1, server.requestCount)
    }

    @Test
    fun failuresAndMissingServerReturnNull() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(500))
        assertNull(gateway.listFinishedAfter(0))
        config.value = null
        assertNull(gateway.listBatch("b"))
    }
}
