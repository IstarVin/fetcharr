package com.istarvin.fetcharr.ui.queue

import androidx.lifecycle.viewModelScope
import com.istarvin.fetcharr.data.CategoryRepository
import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.Fixtures
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.data.settings.InMemoryPreferencesDataStore
import com.istarvin.fetcharr.data.settings.SettingsRepository
import com.istarvin.fetcharr.data.sync.ActiveJobsStore
import com.istarvin.fetcharr.data.sync.ConnectionState
import com.istarvin.fetcharr.data.sync.JobActions
import com.istarvin.fetcharr.data.sync.SyncManager
import com.istarvin.fetcharr.data.sync.WebSocketConnector
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
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.TimeUnit

@OptIn(ExperimentalCoroutinesApi::class)
class QueueViewModelTest {
    private class FakeConnector : WebSocketConnector {
        class Attempt(val listener: WebSocketConnector.Listener) : WebSocketConnector.Handle {
            override fun close() = Unit
            fun open() = listener.onOpen()
        }

        val attempts = CopyOnWriteArrayList<Attempt>()
        override fun connect(config: ServerConfig, listener: WebSocketConnector.Listener): WebSocketConnector.Handle =
            Attempt(listener).also { attempts += it }
    }

    private val ioScope = CoroutineScope(Job() + Dispatchers.IO)
    private val server = MockWebServer()
    private val store = ActiveJobsStore()
    private val connector = FakeConnector()
    private lateinit var settings: SettingsRepository
    private lateinit var categories: CategoryRepository
    private lateinit var actions: JobActions
    private lateinit var sync: SyncManager
    private lateinit var vm: QueueViewModel

    @Before
    fun setUp() {
        Dispatchers.setMain(StandardTestDispatcher())
        server.start()
    }

    @After
    fun tearDown() {
        if (::vm.isInitialized) vm.viewModelScope.cancel() // stop collectors that dispatch on Main before Main is reset
        runCatching { server.shutdown() }
        ioScope.cancel()
        Dispatchers.resetMain()
    }

    /** Builds the graph with real classes against MockWebServer. [configured] saves the server URL first. */
    private suspend fun TestScope.build(configured: Boolean = true) {
        val client = OkHttpClient.Builder().connectTimeout(3, TimeUnit.SECONDS).readTimeout(3, TimeUnit.SECONDS).build()
        val factory = FetcharrApiFactory(client, FetcharrJson)
        settings = SettingsRepository(
            InMemoryPreferencesDataStore(),
            FetcharrJson,
            ioScope,
        )
        if (configured) {
            settings.setServer(server.url("/").toString().trimEnd('/'), "")
            settings.config.first { it != null }
        }
        val provider = FetcharrApiProvider(factory, settings)
        categories = CategoryRepository(factory, settings, ioScope)
        actions = JobActions(provider, store)
        sync = SyncManager(connector, settings, store, { }, FetcharrJson, backgroundScope, provider)
        vm = QueueViewModel(settings, store, categories, sync, actions)
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.state.collect { } }
        // `state` is built from DataStore, which emits from a real thread: wait for it, or `state.value` is still the
        // initial value (and a later runCurrent() cannot help).
        vm.state.first { it.loaded }
    }

    private fun json(code: Int, body: String) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    private fun job(name: String): JobDto = FetcharrJson.decodeFromString(Fixtures.text(name))

    private suspend fun nextEvent(): QueueEvent = vm.events.first()

    @Test
    fun pauseOnA409ShowsJobAlreadyChanged() = runTest {
        build()
        val downloading = job("job-downloading.json")
        store.upsert(downloading)
        server.enqueue(json(409, Fixtures.text("error-invalid_state.json")))
        server.enqueue(json(200, Fixtures.text("job-completed.json").replace("91b4c6e8-0d3f-4a25-8e7b-c5a2f1d60937", downloading.id)))
        vm.pause(downloading.id)
        assertEquals(QueueEvent.Snackbar("Job already changed"), nextEvent())
        assertEquals("/api/jobs/${downloading.id}/pause", server.takeRequest().path)
    }

    @Test
    fun unreachableServerShowsCantReachServer() = runTest {
        build()
        server.shutdown()
        vm.pause("any")
        assertEquals(QueueEvent.Snackbar("Can't reach server"), nextEvent())
    }

    @Test
    fun wrongTokenOffersTheSettingsAction() = runTest {
        build()
        server.enqueue(json(401, Fixtures.text("error-unauthorized.json")))
        vm.resume("any")
        assertEquals(QueueEvent.Snackbar("Wrong API token", openSettings = true), nextEvent())
    }

    @Test
    fun serverErrorShowsTryAgain() = runTest {
        build()
        server.enqueue(MockResponse().setResponseCode(503))
        vm.pauseAll("batch")
        assertEquals(QueueEvent.Snackbar("Server error — try again"), nextEvent())
    }

    @Test
    fun noServerSavedShowsNotSetUp() = runTest {
        build(configured = false)
        vm.pause("any")
        assertEquals(QueueEvent.Snackbar("Server not set up", openSettings = true), nextEvent())
    }

    @Test
    fun cancelSendsNothingUntilConfirmed() = runTest {
        build()
        val downloading = job("job-downloading.json").copy(batchId = null)
        store.upsert(downloading)
        val card = buildQueueSections(listOf(downloading), emptyList()).single().items.single().let { (it as JobItem).job }

        vm.requestCancel(card)
        runCurrent()
        assertEquals(CancelRequest.SingleJob(downloading.id, "Severance — S02E01"), vm.state.value.pendingCancel)
        assertEquals(0, server.requestCount)

        vm.dismissCancel()
        vm.confirmCancel() // nothing pending any more
        runCurrent()
        assertNull(vm.state.value.pendingCancel)
        assertEquals(0, server.requestCount)

        server.enqueue(json(200, Fixtures.text("job-cancelled.json")))
        vm.requestCancel(card)
        vm.confirmCancel()
        runCurrent()
        assertNull(vm.state.value.pendingCancel)
        assertEquals("/api/jobs/${downloading.id}/cancel", server.takeRequest().path)
    }

    @Test
    fun cancelAllAsksFirstAndThenCallsTheBatchEndpoint() = runTest {
        build()
        val a = job("job-downloading.json")
        val b = job("job-pending.json").copy(status = JobStatus.DOWNLOADING)
        val group = buildQueueSections(listOf(a, b), emptyList()).single().items.single() as BatchGroup

        vm.requestCancelAll(group)
        runCurrent()
        assertEquals(CancelRequest.WholeBatch(a.batchId!!, "Severance — Season 2", 2), vm.state.value.pendingCancel)
        assertEquals(0, server.requestCount)

        server.enqueue(json(200, """{"jobs": []}"""))
        vm.confirmCancel()
        runCurrent()
        assertEquals("/api/batches/${a.batchId}/cancel", server.takeRequest().path)
    }

    @Test
    fun stateCombinesJobsSettingsAndConnection() = runTest {
        build()
        store.upsert(job("job-downloading.json"))
        val state = vm.state.first { it.loaded && it.hasServer && it.sections.isNotEmpty() }
        assertEquals(listOf(QueueSectionKind.Active), state.sections.map { it.kind })
        assertEquals("Disconnected", state.banner?.text)
        assertFalse(state.hasSnapshot)
    }

    @Test
    fun noServerMeansNoServerState() = runTest {
        build(configured = false)
        val state = vm.state.first { it.loaded }
        assertFalse(state.hasServer)
        assertNull(state.banner)
    }

    @Test
    fun refreshReconnectsAndClearsWhenConnectedAgain() = runTest {
        build()
        server.enqueue(json(200, Fixtures.text("info.json")))
        sync.acquire("test")
        runCurrent()
        connector.attempts.single().open()
        runCurrent()
        assertEquals(ConnectionState.Connected, sync.state.value)

        vm.refresh()
        runCurrent()
        assertTrue(vm.state.value.isRefreshing)
        assertEquals(2, connector.attempts.size)
        assertEquals(ConnectionState.Connecting, sync.state.value)

        connector.attempts.last().open()
        runCurrent()
        assertFalse(vm.state.value.isRefreshing)
        sync.release("test")
    }

    @Test
    fun refreshStopsSpinningAfterATimeoutIfTheSocketNeverReconnects() = runTest {
        build()
        server.enqueue(json(200, Fixtures.text("info.json")))
        sync.acquire("test")
        runCurrent()
        connector.attempts.single().open()
        runCurrent()

        vm.refresh()
        runCurrent()
        assertTrue(vm.state.value.isRefreshing)
        advanceTimeBy(4_900)
        assertTrue(vm.state.value.isRefreshing)
        advanceTimeBy(200)
        assertFalse(vm.state.value.isRefreshing)
        sync.release("test")
    }
}
