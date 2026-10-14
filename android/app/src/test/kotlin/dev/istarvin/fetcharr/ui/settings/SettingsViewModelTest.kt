package dev.istarvin.fetcharr.ui.settings

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import dev.istarvin.fetcharr.data.CategoryRepository
import dev.istarvin.fetcharr.data.SystemRepository
import dev.istarvin.fetcharr.data.network.ApiError
import dev.istarvin.fetcharr.data.network.Fixtures
import dev.istarvin.fetcharr.data.network.ConnectionTester
import dev.istarvin.fetcharr.data.network.FetcharrApiFactory
import dev.istarvin.fetcharr.data.network.FetcharrApiProvider
import dev.istarvin.fetcharr.data.network.FetcharrJson
import dev.istarvin.fetcharr.data.network.TestConnectionResult
import dev.istarvin.fetcharr.data.settings.SettingsRepository
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withTimeout
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.util.concurrent.TimeUnit

@OptIn(ExperimentalCoroutinesApi::class)
class SettingsViewModelTest {
    @get:Rule
    val tmp = TemporaryFolder()

    private val scope = CoroutineScope(Job() + Dispatchers.IO)
    private lateinit var server: MockWebServer
    private lateinit var settings: SettingsRepository
    private lateinit var vm: SettingsViewModel
    private lateinit var factory: FetcharrApiFactory

    @Before
    fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        server = MockWebServer().also { it.start() }
        val client = OkHttpClient.Builder()
            .connectTimeout(4, TimeUnit.SECONDS)
            .readTimeout(4, TimeUnit.SECONDS)
            .build()
        factory = FetcharrApiFactory(client, FetcharrJson)
        val dataStore = PreferenceDataStoreFactory.create(scope = scope) { tmp.newFile("settings.preferences_pb") }
        settings = SettingsRepository(dataStore, FetcharrJson, scope)
        vm = newVm()
    }

    private fun newVm(): SettingsViewModel {
        val provider = FetcharrApiProvider(factory, settings)
        val categories = CategoryRepository(provider, factory, settings)
        return SettingsViewModel(settings, categories, SystemRepository(provider), ConnectionTester(factory))
    }

    /** Saves the mock server as the configured one and builds a VM that has loaded `/api/info` (managed yt-dlp). */
    private fun vmWithSavedServer(infoBody: String = infoJson()): SettingsViewModel {
        await { settings.setServer("http://$hostPort", "tok") }
        server.enqueue(MockResponse().setResponseCode(200).setBody(infoBody))
        val v = newVm()
        await { v.uiState.first { it.serverVersion != null } }
        return v
    }

    private fun ytdlpResult(v: SettingsViewModel): YtdlpUpdateStatus =
        await { v.uiState.first { it.ytdlpUpdate !is YtdlpUpdateStatus.Idle && it.ytdlpUpdate !is YtdlpUpdateStatus.Running } }.ytdlpUpdate

    @Test
    fun aboutAndYtdlpShowInfo() {
        val v = vmWithSavedServer()
        val state = v.uiState.value
        assertEquals("1.0.0", state.serverVersion)
        assertEquals("2026.09.30", state.ytdlpVersion)
        assertTrue(state.ytdlpManaged)
    }

    @Test
    fun unmanagedInfoHidesUpdate() {
        val v = vmWithSavedServer(infoJson().replace("\"ytdlp_managed\": true", "\"ytdlp_managed\": false"))
        assertFalse(v.uiState.value.ytdlpManaged)
        assertEquals("2026.09.30", v.uiState.value.ytdlpVersion)
    }

    @Test
    fun ytdlpUpdateSuccessShowsNewVersionAndRefreshesInfo() {
        val v = vmWithSavedServer()
        server.enqueue(MockResponse().setResponseCode(200).setBody(Fixtures.text("ytdlp-update.json")))
        server.enqueue(
            MockResponse().setResponseCode(200)
                .setBody(infoJson().replace("2026.09.30", "2026.10.01")),
        )
        v.updateYtdlp()
        assertEquals(YtdlpUpdateStatus.Updated("2026.09.30"), ytdlpResult(v))
        assertEquals("2026.10.01", await { v.uiState.first { it.ytdlpVersion == "2026.10.01" } }.ytdlpVersion)
        server.takeRequest() // /api/info
        val post = server.takeRequest()
        assertEquals("POST", post.method)
        assertEquals("/api/system/ytdlp/update", post.path)
        assertEquals("Bearer tok", post.getHeader("Authorization"))
    }

    @Test
    fun ytdlpBusyShowsTryLater() {
        val v = vmWithSavedServer()
        server.enqueue(MockResponse().setResponseCode(409).setBody(Fixtures.text("error-ytdlp_busy.json")))
        v.updateYtdlp()
        val status = ytdlpResult(v) as YtdlpUpdateStatus.Failed
        assertEquals(ApiError.YtdlpBusy, status.error)
        assertEquals("Downloads are running — try again when they finish", status.error.userMessage)
        assertTrue(v.uiState.value.ytdlpManaged) // can retry
    }

    @Test
    fun ytdlpUnmanagedHidesButtonAndShowsMessage() {
        val v = vmWithSavedServer()
        server.enqueue(MockResponse().setResponseCode(409).setBody(Fixtures.text("error-ytdlp_unmanaged.json")))
        v.updateYtdlp()
        val status = ytdlpResult(v) as YtdlpUpdateStatus.Failed
        assertEquals(ApiError.YtdlpUnmanaged, status.error)
        assertFalse(v.uiState.value.ytdlpManaged)
    }

    @Test
    fun ytdlpOtherErrorsUseUserMessage() {
        val v = vmWithSavedServer()
        server.enqueue(MockResponse().setResponseCode(401).setBody(Fixtures.text("error-unauthorized.json")))
        v.updateYtdlp()
        assertEquals("Wrong API token", (ytdlpResult(v) as YtdlpUpdateStatus.Failed).error.userMessage)
        server.enqueue(MockResponse().setResponseCode(500).setBody("boom"))
        v.updateYtdlp()
        assertEquals(
            "Server error — try again",
            (await { v.uiState.first { (it.ytdlpUpdate as? YtdlpUpdateStatus.Failed)?.error is ApiError.Server } }
                .ytdlpUpdate as YtdlpUpdateStatus.Failed).error.userMessage,
        )
    }

    @After
    fun tearDown() {
        runCatching { server.shutdown() }
        scope.cancel()
        Dispatchers.resetMain()
    }

    private fun <T> await(block: suspend () -> T): T = runBlocking { withTimeout(5_000) { block() } }

    private val hostPort get() = "${server.hostName}:${server.port}"

    /** Types the server address (no scheme) and runs the test; returns the final result. */
    private fun runTest(token: String = ""): TestConnectionResult = await {
        vm.uiState.first { it.loaded }
        vm.onUrlChange("$hostPort/")
        vm.onTokenChange(token)
        vm.testConnection()
        val done = vm.uiState.first { it.testStatus is TestStatus.Done }.testStatus as TestStatus.Done
        done.result
    }

    @Test
    fun connectedUsesEnteredValuesAndCachesCategories() {
        server.enqueue(MockResponse().setResponseCode(200).setBody(infoJson()))
        server.enqueue(MockResponse().setResponseCode(200).setBody(infoJson()))

        val result = runTest(token = "tok") as TestConnectionResult.Connected
        assertEquals("1.0.0", result.version)

        val first = server.takeRequest()
        assertEquals("/api/info", first.path)
        assertEquals("Bearer tok", first.getHeader("Authorization"))

        // Entered values were tested, not saved, and the form shows the normalized URL.
        await {
            assertEquals("http://$hostPort", vm.uiState.value.url)
            assertEquals("", settings.settings.first().serverUrl)
            val cached = settings.settings.first { it.cachedCategories.isNotEmpty() }.cachedCategories
            assertEquals(listOf("movies", "tv", "misc"), cached.map { it.id })
            // Movie dropdown: movie + flat; TV dropdown: tv.
            val state = vm.uiState.first { it.movieCategories.isNotEmpty() }
            assertEquals(listOf("movies", "misc"), state.movieCategories.map { it.id })
            assertEquals(listOf("tv"), state.tvCategories.map { it.id })
        }
    }

    @Test
    fun wrongToken() {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":{"code":"unauthorized","message":"nope"}}"""))
        assertEquals(TestConnectionResult.WrongToken, runTest("bad"))
    }

    @Test
    fun notFetcharr() {
        server.enqueue(MockResponse().setResponseCode(200).setBody("<html>hello</html>"))
        assertEquals(TestConnectionResult.NotFetcharr, runTest())
    }

    @Test
    fun unreachable() {
        server.shutdown()
        assertEquals(TestConnectionResult.Unreachable, runTest())
    }

    @Test
    fun invalidUrlIsFlaggedWithoutTesting() = await {
        vm.uiState.first { it.loaded }
        vm.onUrlChange("http://exa mple")
        vm.testConnection()
        assertTrue(vm.uiState.value.urlInvalid)
        assertEquals(TestStatus.Idle, vm.uiState.value.testStatus)
        assertEquals(0, server.requestCount)
    }

    @Test
    fun saveNormalizesAndPersists() = await {
        vm.uiState.first { it.loaded }
        vm.onUrlChange("  $hostPort// ")
        vm.onTokenChange(" tok ")
        assertTrue(vm.save())
        assertEquals("http://$hostPort", vm.uiState.value.url)
        val saved = settings.settings.first { it.serverUrl.isNotEmpty() }
        assertEquals("http://$hostPort", saved.serverUrl)
        assertEquals("tok", saved.token)
        assertEquals("http://$hostPort", settings.config.first { it != null }!!.baseUrl)
    }

    @Test
    fun saveRejectsInvalidUrl() = await {
        vm.uiState.first { it.loaded }
        vm.onUrlChange("ftp://host")
        assertFalse(vm.save())
        assertTrue(vm.uiState.value.urlInvalid)
        assertEquals("", settings.settings.first().serverUrl)
    }

    @Test
    fun editingResetsTestStatus() {
        server.enqueue(MockResponse().setResponseCode(200).setBody("<html/>"))
        runTest()
        vm.onTokenChange("x")
        assertEquals(TestStatus.Idle, vm.uiState.value.testStatus)
    }

    @Test
    fun uiStringsMatchSpec() {
        val xml = listOf("src/main/res/values/strings.xml", "app/src/main/res/values/strings.xml")
            .map(::File).first { it.exists() }.readText()
        fun str(name: String) = Regex("""<string name="$name">(.*?)</string>""").find(xml)!!.groupValues[1].replace("\\'", "'")
        assertEquals("Connected · v%1\$s · %2\$d ms", str("settings_test_connected"))
        assertEquals("Can't reach server", str("settings_test_unreachable"))
        assertEquals("Wrong API token", str("settings_test_wrong_token"))
        assertEquals("Not a Fetcharr server", str("settings_test_not_fetcharr"))
    }

    private fun infoJson() = checkNotNull(javaClass.classLoader!!.getResourceAsStream("info.json")).use {
        it.readBytes().toString(Charsets.UTF_8)
    }
}
