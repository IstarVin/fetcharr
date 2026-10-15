package com.istarvin.fetcharr.integration

import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.FetcharrApi
import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.dto.BatchEpisodeRequest
import com.istarvin.fetcharr.data.network.dto.CreateBatchRequest
import com.istarvin.fetcharr.data.network.dto.CreateJobRequest
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.data.network.dto.Rfc3339
import com.istarvin.fetcharr.data.network.dto.RetryRequest
import com.istarvin.fetcharr.data.network.dto.WsEvent
import com.istarvin.fetcharr.data.network.safeApiCall
import com.istarvin.fetcharr.data.sync.OkHttpWebSocketConnector
import com.istarvin.fetcharr.data.sync.WebSocketConnector
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File
import java.util.UUID
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.TimeUnit

/**
 * Opt-in end-to-end check of the real app network stack (FetcharrApiFactory, DTOs, OkHttpWebSocketConnector)
 * against a running Fetcharr server. Skipped unless FETCHARR_LIVE_URL and FETCHARR_LIVE_TOKEN are set.
 *
 * Server expectations: categories `movies` (movie), `tv` (tv) and `misc` (flat), concurrency >= 2, the fake
 * yt-dlp (`server/testdata/fake-ytdlp`) with FAKE_YTDLP_DURATION of about 6 seconds, a fresh data dir, and the
 * server running on the same host (library files are checked on disk when the path is visible).
 *
 *   FETCHARR_LIVE_URL=http://127.0.0.1:8080 FETCHARR_LIVE_TOKEN=secret ./gradlew testDebugUnitTest --tests '*LiveServerTest'
 *
 * Each check prints `LIVE PASS|FAIL <name>`; the test fails at the end if any check failed.
 */
class LiveServerTest {
    private val baseUrl: String? = System.getenv("FETCHARR_LIVE_URL")?.takeIf { it.isNotBlank() }
    private val token: String? = System.getenv("FETCHARR_LIVE_TOKEN")?.takeIf { it.isNotBlank() }

    private val http = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()
    private val factory = FetcharrApiFactory(http, FetcharrJson)

    private val failures = mutableListOf<String>()

    // WebSocket capture
    private val events = CopyOnWriteArrayList<WsEvent>()
    private val wsErrors = CopyOnWriteArrayList<String>()
    private val wsUnknownTypes = CopyOnWriteArrayList<String>()

    @Volatile private var wsOpen = false

    private suspend fun step(name: String, block: suspend () -> Unit) {
        try {
            block()
            println("LIVE PASS $name")
        } catch (e: Throwable) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            failures += name
            println("LIVE FAIL $name: ${e::class.simpleName}: ${e.message}")
        }
    }

    private fun <T> ok(result: ApiResult<T>): T = when (result) {
        is ApiResult.Success -> result.value
        is ApiResult.Failure -> throw AssertionError("API call failed: ${result.error}")
    }

    private fun <T> err(result: ApiResult<T>): ApiError = when (result) {
        is ApiResult.Success -> throw AssertionError("expected an error but got ${result.value}")
        is ApiResult.Failure -> result.error
    }

    private fun eventJobs(e: WsEvent): List<JobDto> = when (e) {
        is WsEvent.Snapshot -> e.jobs
        is WsEvent.JobAdded -> listOf(e.job)
        is WsEvent.JobUpdated -> listOf(e.job)
        else -> emptyList()
    }

    /** Waits for a WS event (from index [from] on) carrying job [id] that satisfies [pred]. */
    private suspend fun awaitJob(id: String, from: Int = 0, timeoutMs: Long = 60_000, pred: (JobDto) -> Boolean): JobDto {
        val deadline = System.currentTimeMillis() + timeoutMs
        var i = from
        while (System.currentTimeMillis() < deadline) {
            while (i < events.size) {
                val hit = eventJobs(events[i]).firstOrNull { it.id == id && pred(it) }
                i++
                if (hit != null) return hit
            }
            delay(50)
        }
        val seen = events.drop(from).flatMap { eventJobs(it) }.filter { it.id == id }
            .joinToString { "${it.status}/${it.progress?.percent}" }
        throw AssertionError("timed out waiting for WS event on $id; saw: $seen")
    }

    private suspend fun awaitRemoved(id: String, from: Int, timeoutMs: Long = 10_000) {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < deadline) {
            if (events.drop(from).any { it is WsEvent.JobRemoved && id in it.jobIds }) return
            delay(50)
        }
        throw AssertionError("no job_removed for $id")
    }

    @Test
    fun liveScenario() {
        assumeTrue("FETCHARR_LIVE_URL / FETCHARR_LIVE_TOKEN not set", baseUrl != null && token != null)
        runBlocking { scenario(baseUrl!!, token!!) }
        assertTrue("failed checks: $failures", failures.isEmpty())
    }

    private suspend fun scenario(url: String, tok: String) {
        val config = ServerConfig(url, tok)
        val api: FetcharrApi = factory.create(config)
        val run = UUID.randomUUID().toString().take(8)
        val startedAt = System.currentTimeMillis()
        fun u(name: String) = "https://example.com/live/$run/$name"

        // ---- connectivity / auth / info -----------------------------------------------------------------
        step("health parses") { assertEquals("ok", ok(safeApiCall { api.health() }).status) }
        step("info parses") {
            val info = ok(safeApiCall { api.info() })
            assertTrue(info.version.isNotBlank())
            assertTrue("authRequired", info.authRequired)
            assertFalse("ytdlpManaged", info.ytdlpManaged)
            assertEquals(2, info.concurrency)
            val byId = info.categories.associateBy { it.id }
            assertEquals("movie", byId.getValue("movies").layout)
            assertEquals("tv", byId.getValue("tv").layout)
            assertEquals("flat", byId.getValue("misc").layout)
            assertTrue("free_bytes", info.categories.all { it.freeBytes != null && it.path != null })
            assertNotNull(info.ytdlpVersion)
        }
        step("wrong token -> Unauthorized") {
            val bad = factory.create(ServerConfig(url, "definitely-wrong"))
            assertEquals(ApiError.Unauthorized, err(safeApiCall { bad.info() }))
        }
        step("unknown job -> NotFound") {
            val e = err(safeApiCall { api.getJob("00000000-0000-0000-0000-000000000000") })
            assertTrue(e.toString(), e is ApiError.NotFound)
        }
        step("update yt-dlp -> YtdlpUnmanaged") {
            assertEquals(ApiError.YtdlpUnmanaged, err(safeApiCall { api.updateYtdlp() }))
        }

        // ---- websocket ------------------------------------------------------------------------------------
        val connector = OkHttpWebSocketConnector(http)
        val handle = connector.connect(
            config,
            object : WebSocketConnector.Listener {
                override fun onOpen() { wsOpen = true }

                override fun onMessage(text: String) {
                    try {
                        val e = WsEvent.parse(FetcharrJson, text)
                        if (e is WsEvent.Unknown) wsUnknownTypes += e.type
                        events += e
                    } catch (t: Throwable) {
                        wsErrors += "${t.message} :: ${text.take(300)}"
                    }
                }

                override fun onClosed() { wsOpen = false }

                override fun onFailure(error: Throwable) { wsErrors += "ws failure: $error" }
            },
        )
        try {
            step("ws opens and sends snapshot first") {
                val deadline = System.currentTimeMillis() + 10_000
                while (events.isEmpty() && System.currentTimeMillis() < deadline) delay(50)
                assertTrue("no message; wsErrors=$wsErrors", events.isNotEmpty())
                assertTrue("first=${events.first()}", events.first() is WsEvent.Snapshot)
                assertTrue(wsOpen)
            }

            // ---- create ------------------------------------------------------------------------------------
            val title = "Live Movie $run"
            val movieUrl = u("movie-a")
            val tvUrl = u("tv-single")
            lateinit var movie: JobDto
            lateinit var tv: JobDto
            val afterSnapshot = events.size

            step("create movie job") {
                movie = ok(safeApiCall {
                    api.createJob(
                        CreateJobRequest(
                            url = movieUrl, category = "movies", title = title, year = 2024,
                            headers = mapOf("Referer" to "https://example.com/"),
                            subtitles = null,
                        ),
                    )
                })
                assertEquals(JobStatus.PENDING, movie.status)
                assertEquals("movies", movie.category)
                assertEquals(title, movie.title)
                assertEquals(2024, movie.year)
                assertNull(movie.batchId)
                assertTrue(movie.createdAt >= startedAt - 60_000)
            }
            step("create tv job without title") {
                tv = ok(safeApiCall {
                    api.createJob(CreateJobRequest(url = tvUrl, category = "tv", showName = "Live Show $run", season = 1, episode = 1, year = 2020))
                })
                assertNull(tv.title)
                assertEquals(1, tv.season)
                assertEquals(1, tv.episode)
            }
            lateinit var batchId: String
            val batchJobs = mutableListOf<JobDto>()
            step("create batch with one duplicate URL") {
                val r = ok(safeApiCall {
                    api.createBatch(
                        CreateBatchRequest(
                            category = "tv", showName = "Batch Show $run", season = 2, year = 2022,
                            headers = mapOf("X-Batch" to "1"),
                            episodes = listOf(
                                BatchEpisodeRequest(episode = 1, title = "One", url = u("b1")),
                                BatchEpisodeRequest(episode = 2, title = "Two (dup)", url = tvUrl),
                                BatchEpisodeRequest(episode = 3, url = u("b3"), headers = mapOf("X-Ep" to "3")),
                                BatchEpisodeRequest(episode = 4, title = "Four", url = u("b4")),
                            ),
                        ),
                    )
                })
                batchId = r.batchId
                batchJobs += r.jobs
                assertEquals(listOf(1, 3, 4), r.jobs.map { it.episode })
                assertTrue(r.jobs.all { it.batchId == r.batchId && it.season == 2 && it.showName == "Batch Show $run" })
                assertEquals(1, r.skipped.size)
                assertEquals(2, r.skipped[0].episode)
                assertEquals("duplicate", r.skipped[0].reason)
            }
            step("ws job_added for created jobs") {
                awaitJob(movie.id, afterSnapshot, 10_000) { true }
                awaitJob(tv.id, afterSnapshot, 10_000) { true }
                for (j in batchJobs) awaitJob(j.id, afterSnapshot, 10_000) { true }
                assertTrue(events.drop(afterSnapshot).any { it is WsEvent.JobAdded && it.job.id == movie.id })
            }

            // ---- errors ------------------------------------------------------------------------------------
            step("duplicate URL -> Duplicate") {
                val e = err(safeApiCall { api.createJob(CreateJobRequest(url = movieUrl, category = "movies", title = "Other")) })
                assertTrue(e.toString(), e is ApiError.Duplicate)
            }
            step("resume of non-paused job -> InvalidState") {
                val e = err(safeApiCall { api.resumeJob(batchJobs.last().id) })
                assertTrue(e.toString(), e is ApiError.InvalidState)
            }
            step("delete of active job -> InvalidState") {
                val e = err(safeApiCall { api.deleteJob(batchJobs.last().id) })
                assertTrue(e.toString(), e is ApiError.InvalidState)
            }
            step("unknown category -> BadRequest") {
                val e = err(safeApiCall { api.createJob(CreateJobRequest(url = u("x1"), category = "nope", title = "t")) })
                assertTrue(e.toString(), e is ApiError.BadRequest && e.code == "invalid_request")
            }
            step("tv without episode -> BadRequest") {
                val e = err(safeApiCall { api.createJob(CreateJobRequest(url = u("x2"), category = "tv", showName = "S", season = 1)) })
                assertTrue(e.toString(), e is ApiError.BadRequest)
            }

            // ---- list with filters -------------------------------------------------------------------------
            step("list: status filter + total") {
                val r = ok(safeApiCall { api.listJobs(status = "pending,downloading", limit = 100) })
                val ids = r.jobs.map { it.id }
                assertTrue(movie.id in ids && tv.id in ids)
                assertTrue(r.jobs.all { it.status == JobStatus.PENDING || it.status == JobStatus.DOWNLOADING })
                assertEquals(r.jobs.size, r.total)
                assertEquals(r.jobs.sortedByDescending { it.createdAt }.map { it.id }, ids)
            }
            step("list: category + batch_id + limit/offset") {
                val tvJobs = ok(safeApiCall { api.listJobs(category = "tv", limit = 100) })
                assertTrue(tvJobs.jobs.all { it.category == "tv" })
                assertEquals(4, tvJobs.total)
                val b = ok(safeApiCall { api.listJobs(batchId = batchId) })
                assertEquals(3, b.total)
                assertEquals(batchJobs.map { it.id }.toSet(), b.jobs.map { it.id }.toSet())
                val page = ok(safeApiCall { api.listJobs(limit = 2, offset = 1) })
                assertEquals(2, page.jobs.size)
                assertEquals(5, page.total)
            }
            step("get job") {
                val j = ok(safeApiCall { api.getJob(movie.id) })
                assertEquals(movie.id, j.id)
            }
            step("list: bad status -> BadRequest") {
                val e = err(safeApiCall { api.listJobs(status = "bogus") })
                assertTrue(e.toString(), e is ApiError.BadRequest)
            }

            step("cancel a pending batch job") {
                val target = batchJobs.last()
                val from = events.size
                val j = ok(safeApiCall { api.cancelJob(target.id) })
                assertEquals(JobStatus.CANCELLED, j.status)
                assertNotNull(j.finishedAt)
                val e = err(safeApiCall { api.cancelJob(target.id) })
                assertTrue("second cancel: $e", e is ApiError.InvalidState)
                awaitJob(target.id, from, 10_000) { it.status == JobStatus.CANCELLED }
            }

            // ---- progress, pause, resume -------------------------------------------------------------------
            var pausedAt = 0
            step("ws progress update for movie") {
                val j = awaitJob(movie.id, afterSnapshot) { it.status == JobStatus.DOWNLOADING && (it.progress?.percent ?: 0.0) > 0.0 }
                val p = j.progress!!
                assertNotNull(p.downloadedBytes)
                assertNotNull(p.totalBytes)
                assertNotNull(j.startedAt)
            }
            step("pause downloading movie") {
                val j = ok(safeApiCall { api.pauseJob(movie.id) })
                assertEquals(JobStatus.PAUSED, j.status)
                pausedAt = events.size
                // progress is kept
                assertNotNull(ok(safeApiCall { api.getJob(movie.id) }).progress)
                awaitJob(movie.id, 0, 10_000) { it.status == JobStatus.PAUSED }
            }
            step("pause again -> InvalidState") {
                val e = err(safeApiCall { api.pauseJob(movie.id) })
                assertTrue(e.toString(), e is ApiError.InvalidState)
            }
            step("batch pause/resume") {
                val paused = ok(safeApiCall { api.pauseBatch(batchId) })
                assertTrue(paused.jobs.isNotEmpty())
                assertTrue(paused.jobs.all { it.batchId == batchId })
                val resumed = ok(safeApiCall { api.resumeBatch(batchId) })
                assertTrue(resumed.jobs.isNotEmpty())
            }
            step("resume movie, it continues and completes") {
                val j = ok(safeApiCall { api.resumeJob(movie.id) })
                assertEquals(JobStatus.PENDING, j.status)
                val from = events.size
                awaitJob(movie.id, from) { it.status == JobStatus.DOWNLOADING }
                val done = awaitJob(movie.id, from) { it.status == JobStatus.COMPLETED }
                assertEquals(100.0, done.progress?.percent ?: -1.0, 0.01)
                assertNotNull(done.filePath)
                assertNotNull(done.finishedAt)
                movie = done
            }
            step("movie went through processing (ws)") {
                val seen = events.drop(afterSnapshot).flatMap { eventJobs(it) }.filter { it.id == movie.id }.map { it.status }
                println("LIVE INFO movie statuses seen: ${seen.distinct()}")
                assertTrue(JobStatus.DOWNLOADING in seen && JobStatus.COMPLETED in seen)
                if (JobStatus.PROCESSING !in seen) println("LIVE INFO no processing event observed for movie (may be too brief)")
            }
            step("movie file placed per library layout") {
                val path = movie.filePath!!
                println("LIVE INFO movie file_path=$path")
                assertTrue(path.endsWith("/$title (2024)/$title (2024).mp4"))
                val f = File(path)
                if (f.parentFile?.exists() == true) assertTrue("file missing on disk: $path", f.isFile)
            }

            step("tv jobs complete with S/E naming") {
                val t = awaitJob(tv.id, 0, 90_000) { it.status == JobStatus.COMPLETED }
                tv = t
                println("LIVE INFO tv file_path=${t.filePath}")
                assertTrue(t.filePath!!.endsWith("/Live Show $run (2020)/Season 01/Live Show $run - S01E01.mp4"))
                for (b in batchJobs.dropLast(1)) {
                    val d = awaitJob(b.id, 0, 90_000) { it.status == JobStatus.COMPLETED }
                    println("LIVE INFO batch file_path=${d.filePath}")
                }
            }

            // ---- failed / retry ----------------------------------------------------------------------------
            lateinit var failed: JobDto
            step("failing URL -> failed with download_failed") {
                val j = ok(safeApiCall { api.createJob(CreateJobRequest(url = u("fail-me"), category = "misc", title = "Live Fail $run")) })
                failed = awaitJob(j.id, 0, 30_000) { it.status == JobStatus.FAILED }
                assertEquals("download_failed", failed.errorCode)
                assertTrue(failed.errorMessage.orEmpty(), failed.errorMessage.orEmpty().contains("simulated failure"))
                assertNotNull(failed.finishedAt)
            }
            step("retry without body requeues and clears error") {
                val j = ok(safeApiCall { api.retryJob(failed.id) })
                assertEquals(JobStatus.PENDING, j.status)
                assertNull(j.errorCode)
                assertNull(j.errorMessage)
                awaitJob(failed.id, events.size - 1, 30_000) { it.status == JobStatus.FAILED }
            }
            step("retry of completed -> InvalidState") {
                val e = err(safeApiCall { api.retryJob(movie.id) })
                assertTrue(e.toString(), e is ApiError.InvalidState)
            }
            step("already_exists then retry with overwrite") {
                val j = ok(safeApiCall { api.createJob(CreateJobRequest(url = u("movie-b"), category = "movies", title = title, year = 2024)) })
                val f = awaitJob(j.id, 0, 30_000) { it.status == JobStatus.FAILED }
                assertEquals("already_exists", f.errorCode)
                val r = ok(safeApiCall { api.retryJobWithBody(f.id, RetryRequest(overwrite = true)) })
                assertEquals(JobStatus.PENDING, r.status)
                val from = events.size - 1
                val done = awaitJob(f.id, from, 30_000) { it.status == JobStatus.COMPLETED }
                assertEquals(movie.filePath, done.filePath)
                // keep this one as the job for the delete_file check
                movie = done
            }
            step("cancel a downloading job, then retry with overwrite body") {
                val j = ok(safeApiCall { api.createJob(CreateJobRequest(url = u("cancel-me"), category = "misc", title = "Live Cancel $run")) })
                awaitJob(j.id, 0, 30_000) { it.status == JobStatus.DOWNLOADING && (it.progress?.percent ?: 0.0) > 0.0 }
                val c = ok(safeApiCall { api.cancelJob(j.id) })
                assertEquals(JobStatus.CANCELLED, c.status)
                assertNotNull(c.finishedAt)
                val r = ok(safeApiCall { api.retryJobWithBody(j.id, RetryRequest(overwrite = true)) })
                assertEquals(JobStatus.PENDING, r.status)
                awaitJob(j.id, events.size - 1, 30_000) { it.status == JobStatus.COMPLETED }
            }
            step("batch cancel/retry endpoints parse") {
                val c = ok(safeApiCall { api.cancelBatch(batchId) })
                assertNotNull(c.jobs)
                val r = ok(safeApiCall { api.retryBatch(batchId) })
                assertNotNull(r.jobs)
                for (j in r.jobs) awaitJob(j.id, 0, 30_000) { it.status.isFinished || it.status == JobStatus.PENDING || it.status == JobStatus.DOWNLOADING }
            }
            step("batch unknown -> NotFound") {
                val e = err(safeApiCall { api.pauseBatch("00000000-0000-0000-0000-000000000000") })
                assertTrue(e.toString(), e is ApiError.NotFound)
            }
            // Let everything settle: wait until nothing is active.
            step("queue drains") {
                val deadline = System.currentTimeMillis() + 90_000
                while (System.currentTimeMillis() < deadline) {
                    if (ok(safeApiCall { api.listJobs(status = "pending,downloading,processing") }).total == 0) return@step
                    delay(500)
                }
                throw AssertionError("jobs still active")
            }

            // ---- history -----------------------------------------------------------------------------------
            step("history: status=completed,failed,cancelled") {
                val r = ok(safeApiCall { api.listJobs(status = "completed,failed,cancelled", limit = 500) })
                assertTrue(r.jobs.all { it.status.isFinished && it.finishedAt != null })
                val ids = r.jobs.map { it.id }.toSet()
                assertTrue(movie.id in ids && tv.id in ids && failed.id in ids)
                assertTrue(r.jobs.map { it.status }.toSet().containsAll(listOf(JobStatus.COMPLETED, JobStatus.FAILED)))
                assertEquals(r.jobs.size, r.total)
            }
            step("history: finished_after (millisecond precision)") {
                val since = Rfc3339.format(startedAt - 2_000)
                val all = ok(safeApiCall { api.listJobs(status = "completed,failed,cancelled", finishedAfter = since, limit = 500) })
                assertTrue(movie.id in all.jobs.map { it.id })
                assertTrue(all.jobs.all { it.finishedAt!! > startedAt - 2_000 })
                val latest = all.jobs.maxOf { it.finishedAt!! }
                // exactly the latest finished_at -> that job must be excluded (strictly later)
                val atLatest = ok(safeApiCall { api.listJobs(finishedAfter = Rfc3339.format(latest), limit = 500) })
                assertTrue(atLatest.jobs.none { it.finishedAt == latest && it.status.isFinished })
                val none = ok(safeApiCall { api.listJobs(status = "completed,failed,cancelled", finishedAfter = Rfc3339.format(latest + 1) , limit = 500) })
                assertEquals(0, none.total)
            }

            // ---- delete ------------------------------------------------------------------------------------
            step("delete completed with delete_file=true") {
                val path = movie.filePath!!
                val f = File(path)
                val visible = f.isFile
                println("LIVE INFO movie file visible on disk before delete: $visible")
                val from = events.size
                ok(safeApiCall { api.deleteJob(movie.id, deleteFile = true) })
                val e = err(safeApiCall { api.getJob(movie.id) })
                assertTrue(e.toString(), e is ApiError.NotFound)
                awaitRemoved(movie.id, from)
                if (visible) {
                    assertFalse("file still exists", f.exists())
                    assertFalse("empty movie folder not removed", f.parentFile!!.exists())
                }
            }
            step("delete failed job (204) and clear history") {
                ok(safeApiCall { api.deleteJob(failed.id) })
                val e = err(safeApiCall { api.getJob(failed.id) })
                assertTrue(e.toString(), e is ApiError.NotFound)
                val c = ok(safeApiCall { api.clearHistory("failed,cancelled") })
                assertTrue(c.deleted >= 0)
                val bad = err(safeApiCall { api.clearHistory("pending") })
                assertTrue(bad.toString(), bad is ApiError.BadRequest)
            }
            step("ws saw no parse errors or unknown types") {
                assertTrue("wsErrors=$wsErrors", wsErrors.isEmpty())
                assertTrue("unknown ws types=$wsUnknownTypes", wsUnknownTypes.isEmpty())
            }
        } finally {
            handle.close()
        }
    }
}
