package com.istarvin.fetcharr.data.history

import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.Fixtures
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.ServerConfigProvider
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobListDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.data.network.dto.WsEvent
import com.istarvin.fetcharr.data.sync.ActiveJobsStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.encodeToString
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.TimeUnit

class HistoryRepositoryTest {
    private lateinit var server: MockWebServer
    private val configProvider = object : ServerConfigProvider {
        override val config = MutableStateFlow<ServerConfig?>(null)
    }
    private val store = ActiveJobsStore()

    // Unconfined: the store's `finished` events are handled inline, so the tests need no waiting.
    private val scope = CoroutineScope(Job() + Dispatchers.Unconfined)
    private lateinit var repo: HistoryRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        configProvider.config.value = ServerConfig(server.url("/").toString().trimEnd('/'), "tok")
        val client = OkHttpClient.Builder().connectTimeout(5, TimeUnit.SECONDS).readTimeout(2, TimeUnit.SECONDS).build()
        repo = HistoryRepository(FetcharrApiProvider(FetcharrApiFactory(client, FetcharrJson), configProvider), configProvider, store, scope)
    }

    @After
    fun tearDown() {
        scope.cancel()
        server.shutdown()
    }

    private fun fixture(name: String): JobDto = FetcharrJson.decodeFromString(Fixtures.text(name))

    /** Finished jobs `base + 1 .. base + count`: a higher number finished later. */
    private fun jobs(count: Int, base: Int = 0): List<JobDto> {
        val template = fixture("job-completed.json")
        return (1..count).map { i ->
            val n = base + i
            template.copy(id = "job-$n", finishedAt = 1_000_000L + n * 1_000L, createdAt = 1_000_000L + n * 1_000L)
        }
    }

    private fun page(jobs: List<JobDto>, total: Int) =
        MockResponse().setResponseCode(200).setHeader("Content-Type", "application/json")
            .setBody(FetcharrJson.encodeToString(JobListDto(jobs = jobs, total = total)))

    private fun idsOf(state: HistoryState = repo.state.value) = state.items.map { it.id }

    @Test
    fun startsEmptyAndNotLoaded() {
        assertFalse(repo.state.value.loaded)
        assertTrue(repo.state.value.items.isEmpty())
    }

    @Test
    fun firstPageAsksForFinishedStatusesWithLimit50() = runBlocking {
        server.enqueue(page(jobs(3), total = 3))
        assertNull(repo.refresh())
        val request = server.takeRequest()
        assertEquals("GET", request.method)
        assertEquals("/api/jobs", request.requestUrl!!.encodedPath)
        assertEquals("completed,failed,cancelled", request.requestUrl!!.queryParameter("status"))
        assertEquals("50", request.requestUrl!!.queryParameter("limit"))
        assertNull(request.requestUrl!!.queryParameter("offset"))
        assertEquals("Bearer tok", request.getHeader("Authorization"))
        assertTrue(repo.state.value.loaded)
    }

    @Test
    fun itemsAreNewestFinishedFirst() = runBlocking {
        server.enqueue(page(jobs(3), total = 3))
        repo.refresh()
        assertEquals(listOf("job-3", "job-2", "job-1"), idsOf())
    }

    @Test
    fun loadMoreRequestsOffset50AndAppends() = runBlocking {
        server.enqueue(page(jobs(50, base = 50), total = 120))
        repo.refresh()
        assertFalse(repo.state.value.endReached)
        server.enqueue(page(jobs(50), total = 120))
        assertNull(repo.loadMore())

        server.takeRequest()
        val second = server.takeRequest()
        assertEquals("50", second.requestUrl!!.queryParameter("offset"))
        assertEquals("50", second.requestUrl!!.queryParameter("limit"))
        assertEquals("completed,failed,cancelled", second.requestUrl!!.queryParameter("status"))
        assertEquals(100, repo.state.value.items.size)
        assertFalse(repo.state.value.loadingMore)
        assertFalse(repo.state.value.endReached)
    }

    @Test
    fun endReachedWhenAllRowsAreLoaded() = runBlocking {
        server.enqueue(page(jobs(50, base = 50), total = 100))
        repo.refresh()
        assertFalse(repo.state.value.endReached)
        server.enqueue(page(jobs(50), total = 100))
        repo.loadMore()
        assertTrue(repo.state.value.endReached)

        // Nothing more is requested once the end is reached.
        assertNull(repo.loadMore())
        assertEquals(2, server.requestCount)
    }

    @Test
    fun endReachedOnAShortPage() = runBlocking {
        server.enqueue(page(jobs(20), total = 500)) // the total is wrong or stale; a short page still means the end
        repo.refresh()
        assertTrue(repo.state.value.endReached)
        repo.loadMore()
        assertEquals(1, server.requestCount)
    }

    @Test
    fun loadMoreBeforeTheFirstPageDoesNothing() = runBlocking {
        assertNull(repo.loadMore())
        assertEquals(0, server.requestCount)
    }

    @Test
    fun loadMoreDedupesRowsThatShiftedBetweenPages() = runBlocking {
        server.enqueue(page(jobs(50, base = 1), total = 80))
        repo.refresh()
        // The second page repeats job-51 (already inserted live as the first page was shifted by one).
        server.enqueue(page(jobs(30, base = 0).takeLast(10) + jobs(10, base = 100), total = 80))
        repo.loadMore()
        val ids = idsOf()
        assertEquals(ids.distinct(), ids)
        assertEquals(60, ids.size)
    }

    @Test
    fun loadMoreFailureKeepsItemsAndReportsTheError() = runBlocking {
        server.enqueue(page(jobs(50), total = 100))
        repo.refresh()
        server.enqueue(MockResponse().setResponseCode(503))
        val error = repo.loadMore()
        assertTrue(error is ApiError.Server)
        assertEquals(50, repo.state.value.items.size)
        assertFalse(repo.state.value.loadingMore)
        assertEquals(error, repo.state.value.error)
        // A later page succeeds from the same offset.
        server.enqueue(page(jobs(50, base = 50), total = 100))
        assertNull(repo.loadMore())
        server.takeRequest(); server.takeRequest()
        assertEquals("50", server.takeRequest().requestUrl!!.queryParameter("offset"))
        assertNull(repo.state.value.error)
    }

    @Test
    fun refreshFailureBeforeLoadingLeavesItNotLoaded() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(401).setBody(Fixtures.text("error-unauthorized.json")))
        assertEquals(ApiError.Unauthorized, repo.refresh())
        assertFalse(repo.state.value.loaded)
        assertEquals(ApiError.Unauthorized, repo.state.value.error)
    }

    @Test
    fun noServerIsReportedAsUnreachable() = runBlocking {
        configProvider.config.value = null
        assertEquals(ApiError.Unreachable, repo.refresh())
    }

    @Test
    fun aJobThatFinishesIsInsertedAtTheTopWhenLoaded() = runBlocking {
        server.enqueue(page(jobs(3), total = 3))
        repo.refresh()
        val active = fixture("job-failed.json").copy(id = "live", status = JobStatus.DOWNLOADING, finishedAt = null)
        store.apply(WsEvent.JobAdded(active))
        store.apply(WsEvent.JobUpdated(active.copy(status = JobStatus.FAILED, finishedAt = 9_000_000L)))
        assertEquals(listOf("live", "job-3", "job-2", "job-1"), idsOf())
        assertEquals(4, repo.state.value.total)
        assertEquals(4, repo.state.value.nextOffset)
    }

    @Test
    fun aJobThatFinishesIsIgnoredWhenHistoryIsNotLoaded() {
        val active = fixture("job-failed.json").copy(id = "live", status = JobStatus.DOWNLOADING, finishedAt = null)
        store.apply(WsEvent.JobAdded(active))
        store.apply(WsEvent.JobUpdated(active.copy(status = JobStatus.COMPLETED, finishedAt = 9_000_000L)))
        assertTrue(repo.state.value.items.isEmpty())
        assertFalse(repo.state.value.loaded)
    }

    @Test
    fun aFinishedJobAlreadyInTheListIsReplacedNotDuplicated() = runBlocking {
        server.enqueue(page(jobs(3), total = 3))
        repo.refresh()
        store.upsert(jobs(3)[1].copy(status = JobStatus.CANCELLED, finishedAt = 5_000_000L))
        assertEquals(listOf("job-2", "job-3", "job-1"), idsOf())
        assertEquals(JobStatus.CANCELLED, repo.state.value.items.first().status)
        assertEquals(3, repo.state.value.items.size)
        assertEquals(3, repo.state.value.total)
    }

    @Test
    fun removeAndClearUpdateTheList() = runBlocking {
        server.enqueue(page(jobs(3), total = 3))
        repo.refresh()
        repo.remove("job-2")
        assertEquals(listOf("job-3", "job-1"), idsOf())
        assertEquals(2, repo.state.value.total)
        repo.remove("unknown")
        assertEquals(2, repo.state.value.items.size)
        repo.clear()
        assertTrue(repo.state.value.items.isEmpty())
        assertTrue(repo.state.value.loaded)
        assertTrue(repo.state.value.endReached)
    }

    @Test
    fun changingTheServerDropsTheCachedHistory() = runBlocking {
        server.enqueue(page(jobs(3), total = 3))
        repo.refresh()
        configProvider.config.value = ServerConfig("http://other.invalid:8080", null)
        // The config collector runs on the Unconfined scope but waits for the mutex; give it a moment.
        withTimeoutPoll { !repo.state.value.loaded }
        assertTrue(repo.state.value.items.isEmpty())
    }

    @Test
    fun aFilterAndSearchAreSentToTheServer() = runBlocking {
        server.enqueue(page(jobs(3), total = 3))
        repo.refresh()
        server.takeRequest()
        val failed = jobs(1, base = 10).map { it.copy(status = JobStatus.FAILED, title = "Dune") }
        server.enqueue(page(failed, total = 1))
        assertNull(repo.setQuery(HistoryQuery(HistoryFilter.Failed, "dune")))
        val request = server.takeRequest().requestUrl!!
        assertEquals("failed,cancelled", request.queryParameter("status"))
        assertEquals("dune", request.queryParameter("q"))
        assertEquals(listOf("job-11"), idsOf())
        assertEquals(HistoryQuery(HistoryFilter.Failed, "dune"), repo.state.value.query)

        server.enqueue(page(failed, total = 1))
        repo.loadMore() // the end was reached: nothing is requested
        repo.refresh()
        assertEquals("failed,cancelled", server.takeRequest().requestUrl!!.queryParameter("status"))
    }

    @Test
    fun liveResultsFollowTheQuery() = runBlocking {
        server.enqueue(page(emptyList(), total = 0))
        repo.setQuery(HistoryQuery(HistoryFilter.Completed, "sever"))
        val running = fixture("job-failed.json").copy(id = "live", status = JobStatus.DOWNLOADING, finishedAt = null, showName = "Severance")
        store.apply(WsEvent.JobAdded(running))
        store.apply(WsEvent.JobUpdated(running.copy(status = JobStatus.FAILED, finishedAt = 9_000_000L)))
        assertTrue("a failed job is not shown under Completed", idsOf().isEmpty())
        val other = running.copy(id = "other", showName = "Other Show")
        store.apply(WsEvent.JobAdded(other))
        store.apply(WsEvent.JobUpdated(other.copy(status = JobStatus.COMPLETED, finishedAt = 9_100_000L)))
        assertTrue("a non-matching title is not shown", idsOf().isEmpty())
        val match = running.copy(id = "match")
        store.apply(WsEvent.JobAdded(match))
        store.apply(WsEvent.JobUpdated(match.copy(status = JobStatus.COMPLETED, finishedAt = 9_200_000L)))
        assertEquals(listOf("match"), idsOf())
    }

    @Test
    fun anUnchangedQueryDoesNotReload() = runBlocking {
        server.enqueue(page(jobs(1), total = 1))
        repo.refresh()
        assertNull(repo.setQuery(HistoryQuery()))
        assertEquals(1, server.requestCount)
    }

    private suspend fun withTimeoutPoll(condition: () -> Boolean) {
        val deadline = System.nanoTime() + 3_000_000_000L
        while (!condition() && System.nanoTime() < deadline) kotlinx.coroutines.delay(10)
        assertTrue("condition not met in time", condition())
    }
}
