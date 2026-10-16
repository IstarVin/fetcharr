package com.istarvin.fetcharr.ui.history

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.lifecycle.viewModelScope
import com.istarvin.fetcharr.data.CategoryRepository
import com.istarvin.fetcharr.data.history.HistoryRepository
import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.Fixtures
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobListDto
import com.istarvin.fetcharr.data.settings.SettingsRepository
import com.istarvin.fetcharr.data.sync.ActiveJobsStore
import com.istarvin.fetcharr.data.sync.JobActions
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.encodeToString
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.util.concurrent.TimeUnit
import java.util.concurrent.CountDownLatch

@OptIn(ExperimentalCoroutinesApi::class)
class HistoryViewModelTest {
    @get:Rule
    val tmp = TemporaryFolder()

    private val ioScope = CoroutineScope(Job() + Dispatchers.IO)
    private val server = MockWebServer()
    private lateinit var vm: HistoryViewModel
    private lateinit var settings: SettingsRepository

    /** What `GET /api/jobs` returns. */
    @Volatile
    private var listed: List<JobDto> = emptyList()

    /** When set, `DELETE /api/jobs/{id}`, `DELETE /api/jobs` and retries fail with this status. */
    @Volatile
    private var failWith: Int? = null

    @Volatile
    private var playbackUrl = "jobs/done/media?expires=9999999999&signature=scoped-link"

    @Before
    fun setUp() {
        Dispatchers.setMain(StandardTestDispatcher())
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.requestUrl!!.encodedPath
                val fail = failWith
                return when {
                    request.method == "GET" && path == "/api/jobs" ->
                        json(200, FetcharrJson.encodeToString(JobListDto(jobs = listed, total = listed.size)))
                    fail != null && fail == 409 -> json(409, Fixtures.text("error-invalid_state.json"))
                    fail != null -> MockResponse().setResponseCode(fail)
                    request.method == "POST" && path.endsWith("/playback") -> json(200,
                        """{"url":"$playbackUrl","mime_type":"video/mp4","expires_at":"2026-10-17T00:00:00.000Z"}""")
                    request.method == "POST" && path.endsWith("/retry") -> json(200, Fixtures.text("job-pending.json"))
                    request.method == "DELETE" && path == "/api/jobs" -> json(200, """{"deleted": 3}""")
                    request.method == "DELETE" -> MockResponse().setResponseCode(204)
                    else -> MockResponse().setResponseCode(404)
                }
            }
        }
        server.start()
    }

    @After
    fun tearDown() {
        if (::vm.isInitialized) vm.viewModelScope.cancel() // stop collectors that dispatch on Main before Main is reset
        runCatching { server.shutdown() }
        ioScope.cancel()
        Dispatchers.resetMain()
    }

    private fun json(code: Int, body: String) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    private fun fixture(name: String): JobDto = FetcharrJson.decodeFromString(Fixtures.text(name))

    private val completed = fixture("job-completed.json").copy(id = "done", finishedAt = System.currentTimeMillis() - 1_000)
    private val failed = fixture("job-failed.json").copy(id = "failed", finishedAt = System.currentTimeMillis() - 2_000)
    private val alreadyExists = failed.copy(id = "exists", errorCode = "already_exists", finishedAt = System.currentTimeMillis() - 3_000)
    private val cancelled = fixture("job-cancelled.json").copy(id = "cancelled", finishedAt = System.currentTimeMillis() - 4_000)

    /** Builds the graph with real classes against MockWebServer and waits for the first page to show. */
    private suspend fun TestScope.build(jobs: List<JobDto>) {
        listed = jobs
        val client = OkHttpClient.Builder().connectTimeout(3, TimeUnit.SECONDS).readTimeout(3, TimeUnit.SECONDS).build()
        val factory = FetcharrApiFactory(client, FetcharrJson)
        settings = SettingsRepository(
            PreferenceDataStoreFactory.create(scope = ioScope) { tmp.newFile("settings.preferences_pb") },
            FetcharrJson,
            ioScope,
        )
        settings.setServer(server.url("/").toString().trimEnd('/'), "")
        settings.config.first { it != null }
        val provider = FetcharrApiProvider(factory, settings, ioScope)
        val store = ActiveJobsStore()
        val history = HistoryRepository(provider, settings, store, ioScope)
        vm = HistoryViewModel(settings, history, JobActions(provider, store), CategoryRepository(factory, settings, ioScope), provider)
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.state.collect { } }
        // DataStore emits from a real thread: wait for the state instead of running the test scheduler by hand.
        vm.state.first { it.historyLoaded && it.groups.sumOf { g -> g.items.size } == jobs.size }
    }

    private fun ids(): List<String> = vm.state.value.groups.flatMap { g -> g.items.map { it.id } }

    /** The next recorded request that is not a history page load. */
    private fun nextAction(): RecordedRequest {
        while (true) {
            val request = checkNotNull(server.takeRequest(5, TimeUnit.SECONDS)) { "no request arrived" }
            if (!(request.method == "GET" && request.requestUrl!!.encodedPath == "/api/jobs")) return request
        }
    }

    @Test
    fun playRequestsAScopedLinkAndEmitsAnExternalPlayerEvent() = runTest {
        build(listOf(completed))
        vm.play("done")
        vm.play("done") // repeated taps while the request is running are ignored
        val event = vm.events.first() as HistoryEvent.OpenPlayer
        assertEquals(server.url("/api/$playbackUrl").toString(), event.url)
        assertEquals("video/mp4", event.mimeType)
        assertEquals("Oppenheimer (2023)", event.title)
        assertTrue(vm.isCurrentPlayback(event))
        assertEquals("/api/jobs/done/playback", nextAction().path)
        assertEquals(2, server.requestCount) // one history load and one playback link
        vm.state.first { it.preparingPlayback.isEmpty() }

        // Even an already buffered event is invalidated when the token changes.
        settings.setServer(server.url("/").toString().trimEnd('/'), "new-token")
        settings.config.first { it?.token == "new-token" }
        assertTrue(!vm.isCurrentPlayback(event))
    }

    @Test
    fun playFailureShowsTheApiError() = runTest {
        build(listOf(completed))
        failWith = 401
        vm.play("done")
        assertEquals(HistoryEvent.Snackbar("Wrong API token", openSettings = true), vm.events.first())
        vm.state.first { it.preparingPlayback.isEmpty() }
    }

    @Test
    fun playRejectsAUrlOutsideTheJobMediaEndpoint() = runTest {
        build(listOf(completed))
        playbackUrl = "https://other.example/video.mp4"
        vm.play("done")
        assertEquals(HistoryEvent.Snackbar(ApiError.InvalidResponse.userMessage), vm.events.first())
    }

    @Test
    fun playIgnoresFailedCancelledAndMissingFiles() = runTest {
        build(listOf(failed, cancelled, completed.copy(filePath = null)))
        vm.play("failed")
        vm.play("cancelled")
        vm.play("done")
        vm.play("gone")
        assertEquals(1, server.requestCount)
    }

    @Test
    fun alreadyExistsRetryAsksFirstThenPostsOverwrite() = runTest {
        build(listOf(alreadyExists, completed))

        vm.retry("exists")
        val dialog = vm.state.first { it.dialog != null }.dialog
        assertTrue(dialog is HistoryDialog.ConfirmReplace)
        assertEquals("exists", (dialog as HistoryDialog.ConfirmReplace).jobId)
        assertEquals(1, server.requestCount) // only the first page so far: nothing was retried

        vm.confirmReplace()
        vm.state.first { "exists" !in ids() }
        val request = nextAction()
        assertEquals("POST", request.method)
        assertEquals("/api/jobs/exists/retry", request.requestUrl!!.encodedPath)
        assertEquals("""{"overwrite":true}""", request.body.readUtf8().replace(" ", ""))
        assertNull(vm.state.value.dialog)
        assertEquals(listOf("done"), ids())
    }

    @Test
    fun cancellingTheReplacePromptSendsNothing() = runTest {
        build(listOf(alreadyExists))
        vm.retry("exists")
        vm.state.first { it.dialog is HistoryDialog.ConfirmReplace }
        vm.dismissDialog()
        vm.confirmReplace() // nothing pending any more
        vm.state.first { it.dialog == null }
        assertEquals(1, server.requestCount)
        assertEquals(listOf("exists"), ids())
    }

    @Test
    fun otherFailuresAndCancelledJobsRetryImmediatelyWithNoBody() = runTest {
        build(listOf(failed, cancelled, completed))

        vm.retry("failed")
        vm.state.first { "failed" !in ids() }
        val first = nextAction()
        assertEquals("POST", first.method)
        assertEquals("/api/jobs/failed/retry", first.requestUrl!!.encodedPath)
        assertEquals(0L, first.bodySize)
        assertNull(vm.state.value.dialog)

        vm.retry("cancelled")
        vm.state.first { "cancelled" !in ids() }
        val second = nextAction()
        assertEquals("/api/jobs/cancelled/retry", second.requestUrl!!.encodedPath)
        assertEquals(0L, second.bodySize)
        assertEquals(listOf("done"), ids())
    }

    @Test
    fun deleteSendsDeleteFileOnlyWhenCheckedAndOnlyForCompletedJobs() = runTest {
        build(listOf(completed, failed, cancelled))

        // Completed, box left unchecked (the default): plain delete.
        vm.requestDelete("done")
        val unchecked = vm.state.first { it.dialog != null }.dialog as HistoryDialog.Delete
        assertTrue(unchecked.canDeleteFile)
        assertEquals(false, unchecked.deleteFile)
        vm.confirmDelete()
        vm.state.first { "done" !in ids() }
        val plain = nextAction()
        assertEquals("DELETE", plain.method)
        assertEquals("/api/jobs/done", plain.requestUrl!!.encodedPath)
        assertNull(plain.requestUrl!!.queryParameter("delete_file"))

        // Failed: the checkbox is not offered and cannot be forced on.
        vm.requestDelete("failed")
        val noBox = vm.state.first { it.dialog != null }.dialog as HistoryDialog.Delete
        assertEquals(false, noBox.canDeleteFile)
        vm.setDeleteFile(true)
        assertEquals(false, (vm.state.value.dialog as HistoryDialog.Delete).deleteFile)
        vm.confirmDelete()
        vm.state.first { "failed" !in ids() }
        assertNull(nextAction().requestUrl!!.queryParameter("delete_file"))
    }

    @Test
    fun checkingAlsoDeleteFileSendsDeleteFileTrue() = runTest {
        build(listOf(completed, cancelled))
        vm.requestDelete("done")
        vm.state.first { it.dialog != null }
        vm.setDeleteFile(true)
        vm.state.first { (it.dialog as? HistoryDialog.Delete)?.deleteFile == true }
        vm.confirmDelete()
        vm.state.first { "done" !in ids() }
        val request = nextAction()
        assertEquals("DELETE", request.method)
        assertEquals("true", request.requestUrl!!.queryParameter("delete_file"))
        assertEquals(listOf("cancelled"), ids())
    }

    @Test
    fun cancellingTheDeleteDialogSendsNothing() = runTest {
        build(listOf(completed))
        vm.requestDelete("done")
        vm.state.first { it.dialog != null }
        vm.dismissDialog()
        vm.confirmDelete() // nothing pending any more
        vm.state.first { it.dialog == null }
        assertEquals(1, server.requestCount)
        assertEquals(listOf("done"), ids())
    }

    @Test
    fun clearHistoryAsksFirstThenCallsDeleteJobsAndRefreshes() = runTest {
        build(listOf(completed, failed))

        vm.requestClear()
        vm.state.first { it.dialog == HistoryDialog.ClearAll }
        assertEquals(1, server.requestCount)

        listed = emptyList() // what the server holds after the clear
        vm.confirmClear()
        vm.state.first { it.isEmpty }
        val clear = nextAction()
        assertEquals("DELETE", clear.method)
        assertEquals("/api/jobs", clear.requestUrl!!.encodedPath)
        assertNull(clear.requestUrl!!.query)
        // ...followed by a refresh of the first page.
        val refresh = checkNotNull(server.takeRequest(5, TimeUnit.SECONDS))
        assertEquals("GET", refresh.method)
        assertEquals("/api/jobs", refresh.requestUrl!!.encodedPath)
        assertEquals("completed,failed,cancelled", refresh.requestUrl!!.queryParameter("status"))
    }

    @Test
    fun copyPathEmitsTheFinalFilePath() = runTest {
        build(listOf(completed))
        vm.copyPath("done")
        assertEquals(HistoryEvent.CopyPath(completed.filePath!!), vm.events.first())
    }

    @Test
    fun aFailedDeleteShowsASnackbarAndKeepsTheRow() = runTest {
        build(listOf(completed))
        failWith = 503
        vm.requestDelete("done")
        vm.state.first { it.dialog != null }
        vm.confirmDelete()
        assertEquals(HistoryEvent.Snackbar("Server error — try again"), vm.events.first())
        assertEquals(listOf("done"), ids())
    }

    @Test
    fun deletionStaysPendingUntilTheRequestFails() = runTest {
        build(listOf(completed))
        val responseGate = CountDownLatch(1)
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                check(responseGate.await(10, TimeUnit.SECONDS))
                return MockResponse().setResponseCode(503)
            }
        }
        vm.requestDelete("done")
        vm.state.first { it.dialog is HistoryDialog.Delete }
        try {
            vm.confirmDelete()
            val deleting = vm.state.first { "done" in it.deletingJobs }
            assertNull(deleting.dialog)
            assertEquals(listOf("done"), ids())
        } finally {
            responseGate.countDown()
        }
        assertEquals(HistoryEvent.Snackbar("Server error — try again"), vm.events.first())
        vm.state.first { it.deletingJobs.isEmpty() }
        assertEquals(listOf("done"), ids())
    }

    @Test
    fun aConflictShowsJobAlreadyChanged() = runTest {
        build(listOf(failed))
        failWith = 409
        vm.retry("failed")
        assertEquals(HistoryEvent.Snackbar("Job already changed"), vm.events.first())
    }

    @Test
    fun aWrongTokenOffersTheSettingsAction() = runTest {
        build(listOf(completed))
        failWith = 401
        vm.requestClear()
        vm.state.first { it.dialog != null }
        vm.confirmClear()
        // The 401 body is empty here, so only the status decides.
        assertEquals(HistoryEvent.Snackbar("Wrong API token", openSettings = true), vm.events.first())
    }
}
