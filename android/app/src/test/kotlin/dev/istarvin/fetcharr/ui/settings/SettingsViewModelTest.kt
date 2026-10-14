package dev.istarvin.fetcharr.ui.settings

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import dev.istarvin.fetcharr.data.CategoryRepository
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

    @Before
    fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        server = MockWebServer().also { it.start() }
        val client = OkHttpClient.Builder()
            .connectTimeout(4, TimeUnit.SECONDS)
            .readTimeout(4, TimeUnit.SECONDS)
            .build()
        val factory = FetcharrApiFactory(client, FetcharrJson)
        val dataStore = PreferenceDataStoreFactory.create(scope = scope) { tmp.newFile("settings.preferences_pb") }
        settings = SettingsRepository(dataStore, FetcharrJson, scope)
        val categories = CategoryRepository(FetcharrApiProvider(factory, settings), factory, settings)
        vm = SettingsViewModel(settings, categories, ConnectionTester(factory))
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
