package com.istarvin.fetcharr.ui.settings

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import com.istarvin.fetcharr.data.CategoryRepository
import com.istarvin.fetcharr.data.ServerSettingsRepository
import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.dto.DownloadSettingsDto
import com.istarvin.fetcharr.data.network.dto.HistorySettingsDto
import com.istarvin.fetcharr.data.network.dto.MediaServerSettingsDto
import com.istarvin.fetcharr.data.network.dto.ServerCategoryDto
import com.istarvin.fetcharr.data.network.dto.ServerSettingsDto
import com.istarvin.fetcharr.data.network.dto.YtdlpSettingsDto
import com.istarvin.fetcharr.data.settings.SettingsRepository
import java.util.concurrent.TimeUnit
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
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

@OptIn(ExperimentalCoroutinesApi::class)
class ServerSettingsViewModelTest {
    @get:Rule val tmp = TemporaryFolder()
    private val scope = CoroutineScope(Job() + Dispatchers.IO)
    private lateinit var server: MockWebServer
    private lateinit var settings: SettingsRepository
    private lateinit var factory: FetcharrApiFactory
    private val document = ServerSettingsDto(
        revision = 1,
        downloads = DownloadSettingsDto(2, "mp4", 5_000_000_000),
        history = HistorySettingsDto(0), ytdlp = YtdlpSettingsDto(true),
        categories = listOf(ServerCategoryDto("movies", "Movies", "/media/movies", "movie")),
        mediaServers = listOf(MediaServerSettingsDto("jf", "jellyfin", "http://jellyfin:8096", emptyMap(), hasCredential = true)),
    )
    private val info = """{"version":"1.1.0","settings_supported":true,"ytdlp_managed":true,"categories":[{"id":"movies","label":"Movies","layout":"movie","path":"/media/movies"}]}"""

    @Before fun setup() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        server = MockWebServer().also { it.start() }
        factory = FetcharrApiFactory(OkHttpClient.Builder().readTimeout(3, TimeUnit.SECONDS).build(), FetcharrJson)
        settings = SettingsRepository(PreferenceDataStoreFactory.create(scope = scope) { tmp.newFile("settings.preferences_pb") }, FetcharrJson, scope)
    }

    private fun newVm() = ServerSettingsViewModel(
        settings, ServerSettingsRepository(factory), CategoryRepository(FetcharrApiProvider(factory, settings), factory, settings),
    )

    private fun response(body: String, code: Int = 200) = MockResponse().setResponseCode(code).setBody(body)

    private fun open(): ServerSettingsViewModel {
        server.enqueue(response(info))
        server.enqueue(response(FetcharrJson.encodeToString(document)))
        await {
            settings.setServer(server.url("/").toString().trimEnd('/'), "token")
            settings.config.first { it != null }
        }
        return newVm().also { vm -> await { vm.uiState.first { it.draft != null } } }
    }

    @Test fun saveAppliesServerResponseClearsSecretsAndReconcilesDefaults() {
        val vm = open()
        await { settings.setDefaultTvCategoryId("removed") }
        vm.edit { it.copy(concurrency = "4", integrations = it.integrations.map { s -> s.copy(credential = "replacement-secret") }) }
        assertTrue(vm.uiState.value.dirty)
        server.enqueue(response(FetcharrJson.encodeToString(document.copy(revision = 2, downloads = document.downloads.copy(concurrency = 4)))))
        server.enqueue(response(info))
        vm.save()
        val saved = await { vm.uiState.first { it.saved } }
        assertFalse(saved.dirty)
        assertEquals("", saved.draft!!.integrations.single().credential)
        assertEquals(2L, saved.draft.revision)
        await { settings.settings.first { it.defaultTvCategoryId == null && it.cachedCategories.isNotEmpty() } }
        server.takeRequest()
        server.takeRequest()
        val request = server.takeRequest()
        assertEquals("PUT", request.method)
        assertEquals("/api/system/settings", request.path)
        assertEquals("Bearer token", request.getHeader("Authorization"))
        val body = request.body.readUtf8()
        assertTrue(body.contains("replacement-secret"))
        assertFalse(body.contains("auth_token"))
        assertFalse(body.contains("extra_args"))
        assertFalse(tmp.root.walkTopDown().filter { it.isFile }.any { it.readBytes().toString(Charsets.UTF_8).contains("replacement-secret") })
    }

    @Test fun invalidFieldsPreventNetworkSave() {
        val vm = open()
        vm.edit { it.copy(concurrency = "0", minFreeGb = "-1", retentionDays = "-2",
            categories = it.categories.map { c -> c.copy(path = "relative") }) }
        vm.save()
        assertTrue(vm.uiState.value.fieldErrors.keys.containsAll(listOf("concurrency", "minFreeGb", "retentionDays", "category.0.path")))
        assertEquals(2, server.requestCount)
    }

    @Test fun failedSavePreservesDraftAndConflictsRequireReload() {
        val vm = open()
        vm.edit { it.copy(concurrency = "3") }
        server.enqueue(response("""{"error":{"code":"category_busy","message":"Category has queued jobs"}}""", 409))
        vm.save()
        assertEquals("3", await { vm.uiState.first { it.error != null && !it.saving } }.draft!!.concurrency)
        assertFalse(vm.uiState.value.conflict)
        server.enqueue(response("""{"error":{"code":"settings_conflict","message":"Reload before saving"}}""", 409))
        vm.save()
        await { vm.uiState.first { it.conflict } }
        vm.edit { it.copy(concurrency = "4") }
        assertEquals("Reload before saving", vm.uiState.value.error)
        val requests = server.requestCount
        vm.save()
        assertEquals(requests, server.requestCount)
        server.enqueue(response(info))
        server.enqueue(response(FetcharrJson.encodeToString(document.copy(revision = 5))))
        vm.reload()
        assertEquals(5L, await { vm.uiState.first { it.draft?.revision == 5L } }.draft!!.revision)
        assertFalse(vm.uiState.value.dirty)
    }

    @Test fun olderServerDoesNotRequestUnsupportedEndpoint() {
        server.enqueue(response("""{"version":"1.0.0"}"""))
        await {
            settings.setServer(server.url("/").toString().trimEnd('/'), "")
            settings.config.first { it != null }
        }
        val vm = newVm()
        await { vm.uiState.first { it.unsupported } }
        assertNull(vm.uiState.value.draft)
        assertEquals(1, server.requestCount)
    }

    @Test fun changingConnectionDiscardsDraftAndIgnoresLateSave() {
        val vm = open()
        vm.edit { it.copy(concurrency = "9", integrations = it.integrations.map { s -> s.copy(credential = "old-server-secret") }) }
        server.enqueue(response(FetcharrJson.encodeToString(document.copy(revision = 2))).setBodyDelay(500, TimeUnit.MILLISECONDS))
        vm.save()
        server.takeRequest()
        server.takeRequest()
        server.takeRequest() // The old server received the save; its response is still delayed.
        server.enqueue(response(info))
        server.enqueue(response(FetcharrJson.encodeToString(document.copy(revision = 7))))
        await { settings.setServer(server.url("/different").toString(), "different-token") }
        val state = await { vm.uiState.first { it.draft?.revision == 7L } }
        assertEquals("2", state.draft!!.concurrency)
        assertEquals("", state.draft.integrations.single().credential)
        assertFalse(state.saved)
        assertFalse(state.dirty)
    }

    @Test fun settingsContractFixturesKeepCredentialsWriteOnlyAndEncodeZeroValues() {
        fun fixture(name: String) = checkNotNull(javaClass.classLoader!!.getResourceAsStream(name)).use {
            it.readBytes().toString(Charsets.UTF_8)
        }
        val response = FetcharrJson.decodeFromString<ServerSettingsDto>(fixture("server-settings.json"))
        assertNull(response.mediaServers.single().credential)
        assertTrue(response.mediaServers.single().hasCredential)
        val update = FetcharrJson.decodeFromString<ServerSettingsDto>(fixture("update-server-settings.json"))
        assertEquals("example-replacement-key", update.mediaServers.single().credential)
        val zero = update.copy(history = HistorySettingsDto(0), ytdlp = YtdlpSettingsDto(false))
        val encoded = FetcharrJson.encodeToString(zero)
        assertTrue(encoded.contains("\"retention_days\":0"))
        assertTrue(encoded.contains("\"auto_update\":false"))
        val windows = ServerSettingsDraft.from(response).copy(integrations = listOf(
            IntegrationDraft(url = "http://plex:32400", credential = "key", paths = listOf(PathMappingDraft(from = "/media", to = "D:\\Media"))),
        ))
        assertTrue(windows.validationErrors().isEmpty())
    }

    @Test fun draftPreservesExactBytesAndValidatesCategoriesAndMappings() {
        val draft = ServerSettingsDraft.from(document.copy(downloads = document.downloads.copy(minFreeSpace = 5_123_456_789)))
        assertEquals(5_123_456_789L, draft.document().downloads.minFreeSpace)
        assertNull(draft.document().mediaServers.single().credential)
        val invalid = draft.copy(
            categories = listOf(CategoryDraft(id = "duplicate", path = "/a"), CategoryDraft(id = "duplicate", path = "/b")),
            integrations = listOf(IntegrationDraft(url = "ftp://wrong", paths = listOf(PathMappingDraft(from = "relative", to = "/data")))),
        ).validationErrors()
        assertTrue(invalid.keys.containsAll(listOf("category.0.id", "category.1.id", "integration.0.url", "integration.0.credential", "integration.0.path.0")))
    }

    @After fun teardown() {
        scope.cancel()
        runCatching { server.shutdown() }
        Dispatchers.resetMain()
    }

    private fun <T> await(block: suspend () -> T): T = runBlocking { withTimeout(6_000) { block() } }
}
