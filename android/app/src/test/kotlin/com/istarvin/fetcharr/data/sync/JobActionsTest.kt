package com.istarvin.fetcharr.data.sync

import app.cash.turbine.test
import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.Fixtures
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.ServerConfigProvider
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import kotlinx.coroutines.flow.MutableStateFlow
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

class JobActionsTest {
    private lateinit var server: MockWebServer
    private val configProvider = object : ServerConfigProvider {
        override val config = MutableStateFlow<ServerConfig?>(null)
    }
    private val store = ActiveJobsStore()
    private lateinit var actions: JobActions

    private val completedId = "91b4c6e8-0d3f-4a25-8e7b-c5a2f1d60937"

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        configProvider.config.value = ServerConfig(server.url("/").toString().trimEnd('/'), "tok")
        val client = OkHttpClient.Builder().connectTimeout(5, TimeUnit.SECONDS).readTimeout(2, TimeUnit.SECONDS).build()
        actions = JobActions(FetcharrApiProvider(FetcharrApiFactory(client, FetcharrJson), configProvider), store)
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun json(code: Int, body: String) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    private fun job(name: String): JobDto = FetcharrJson.decodeFromString(Fixtures.text(name))

    private fun activeCompletedJob() = job("job-completed.json").copy(status = JobStatus.PROCESSING)

    @Test
    fun pauseSucceedsAndUpsertsTheReturnedJob() = runBlocking {
        val downloading = job("job-downloading.json")
        store.upsert(downloading)
        server.enqueue(json(200, Fixtures.text("job-paused.json").replace("e5d81b07-2f3c-4a94-b0c8-14a7f6e9d3a2", downloading.id)))
        assertEquals(ActionOutcome.Ok, actions.pause(downloading.id))
        val request = server.takeRequest()
        assertEquals("POST", request.method)
        assertEquals("/api/jobs/${downloading.id}/pause", request.path)
        assertEquals("Bearer tok", request.getHeader("Authorization"))
        assertEquals(JobStatus.PAUSED, store.jobs.value.single().status)
    }

    @Test
    fun resumeCancelRetryHitTheirEndpoints() = runBlocking {
        val body = Fixtures.text("job-pending.json")
        repeat(3) { server.enqueue(json(200, body)) }
        assertEquals(ActionOutcome.Ok, actions.resume("j1"))
        assertEquals(ActionOutcome.Ok, actions.cancel("j2"))
        assertEquals(ActionOutcome.Ok, actions.retry("j3"))
        assertEquals(
            listOf("/api/jobs/j1/resume", "/api/jobs/j2/cancel", "/api/jobs/j3/retry"),
            List(3) { server.takeRequest().path },
        )
    }

    @Test
    fun invalidStateRefreshesTheJobAndReportsChanged() = runBlocking {
        store.upsert(activeCompletedJob())
        server.enqueue(json(409, Fixtures.text("error-invalid_state.json")))
        server.enqueue(json(200, Fixtures.text("job-completed.json")))
        store.finished.test {
            assertEquals(ActionOutcome.Changed("Job already changed"), actions.pause(completedId))
            assertEquals(completedId, awaitItem().id)
        }
        assertEquals("/api/jobs/$completedId/pause", server.takeRequest().path)
        val refresh = server.takeRequest()
        assertEquals("GET", refresh.method)
        assertEquals("/api/jobs/$completedId", refresh.path)
        assertTrue(store.jobs.value.isEmpty())
    }

    @Test
    fun invalidStateWithJobGoneRemovesItFromTheStore() = runBlocking {
        store.upsert(activeCompletedJob())
        server.enqueue(json(409, Fixtures.text("error-invalid_state.json")))
        server.enqueue(json(404, Fixtures.text("error-not_found.json")))
        assertEquals(ActionOutcome.Changed("Job already changed"), actions.cancel(completedId))
        assertTrue(store.jobs.value.isEmpty())
    }

    @Test
    fun invalidStateStillReportsChangedWhenTheRefreshFails() = runBlocking {
        store.upsert(activeCompletedJob())
        server.enqueue(json(409, Fixtures.text("error-invalid_state.json")))
        server.enqueue(json(500, """{"error":{"code":"internal","message":"x"}}"""))
        assertEquals(ActionOutcome.Changed("Job already changed"), actions.cancel(completedId))
        assertEquals(1, store.jobs.value.size)
    }

    @Test
    fun notConfigured() = runBlocking {
        configProvider.config.value = null
        assertEquals(ActionOutcome.NotConfigured, actions.pause("x"))
        assertEquals(ActionOutcome.NotConfigured, actions.cancelAll("b"))
        assertEquals(ActionOutcome.NotConfigured, actions.delete("x", false))
        configProvider.config.value = ServerConfig("not a url", null)
        assertEquals(ActionOutcome.NotConfigured, actions.pause("x"))
    }

    @Test
    fun unreachable() = runBlocking {
        server.shutdown()
        assertEquals(ActionOutcome.Failed("Can't reach server"), actions.pause("x"))
    }

    @Test
    fun serverError() = runBlocking {
        server.enqueue(json(500, """{"error":{"code":"internal","message":"boom"}}"""))
        assertEquals(ActionOutcome.Failed("Server error — try again"), actions.retry("x"))
    }

    @Test
    fun otherErrorsCarryTheirMessage() = runBlocking {
        server.enqueue(json(401, Fixtures.text("error-unauthorized.json")))
        assertEquals(ActionOutcome.Failed("Wrong API token"), actions.pause("x"))
        server.enqueue(json(404, Fixtures.text("error-not_found.json")))
        val outcome = actions.resume("x")
        assertTrue(outcome is ActionOutcome.Failed)
    }

    @Test
    fun batchActionUpsertsAllReturnedJobs() = runBlocking {
        val a = job("job-pending.json")
        val b = job("job-downloading.json")
        store.upsert(a)
        store.upsert(b)
        val paused = listOf(a.copy(status = JobStatus.PAUSED), b.copy(status = JobStatus.PAUSED))
        server.enqueue(json(200, FetcharrJson.encodeToString(com.istarvin.fetcharr.data.network.dto.BatchJobsResponse.serializer(), com.istarvin.fetcharr.data.network.dto.BatchJobsResponse(paused))))
        assertEquals(ActionOutcome.Ok, actions.pauseAll("batch-1"))
        val request = server.takeRequest()
        assertEquals("POST", request.method)
        assertEquals("/api/batches/batch-1/pause", request.path)
        assertTrue(store.jobs.value.all { it.status == JobStatus.PAUSED })
    }

    @Test
    fun batchEndpointsAndInvalidStateRefreshByBatchId() = runBlocking {
        store.upsert(activeCompletedJob())
        server.enqueue(json(409, Fixtures.text("error-invalid_state.json")))
        server.enqueue(json(200, """{"jobs":[${Fixtures.text("job-completed.json")}],"total":1}"""))
        assertEquals(ActionOutcome.Changed("Job already changed"), actions.cancelAll("batch-1"))
        assertEquals("/api/batches/batch-1/cancel", server.takeRequest().path)
        assertEquals("/api/jobs?batch_id=batch-1&limit=500", server.takeRequest().path)
        assertTrue(store.jobs.value.isEmpty())
    }

    @Test
    fun resumeAllAndRetryAllEndpoints() = runBlocking {
        repeat(2) { server.enqueue(json(200, """{"jobs":[]}""")) }
        assertEquals(ActionOutcome.Ok, actions.resumeAll("b"))
        assertEquals(ActionOutcome.Ok, actions.retryAll("b"))
        assertEquals(listOf("/api/batches/b/resume", "/api/batches/b/retry"), List(2) { server.takeRequest().path })
    }

    @Test
    fun deleteSendsDeleteFileOnlyWhenAsked() = runBlocking {
        repeat(2) { server.enqueue(MockResponse().setResponseCode(204)) }
        assertEquals(ActionOutcome.Ok, actions.delete("j", true))
        assertEquals(ActionOutcome.Ok, actions.delete("j", false))
        val first = server.takeRequest()
        assertEquals("DELETE", first.method)
        assertEquals("/api/jobs/j?delete_file=true", first.path)
        assertEquals("/api/jobs/j", server.takeRequest().path)
    }

    @Test
    fun deleteOfActiveJobIsChanged() = runBlocking {
        server.enqueue(json(409, Fixtures.text("error-invalid_state.json")))
        assertEquals(ActionOutcome.Changed("Job already changed"), actions.delete("j", false))
    }
}
