package com.istarvin.fetcharr.ui.add

import androidx.lifecycle.SavedStateHandle
import com.istarvin.fetcharr.data.network.Fixtures
import com.istarvin.fetcharr.ui.incoming.HubEvent
import com.istarvin.fetcharr.ui.incoming.VmTestEnv
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class AddViewModelTest {
    private lateinit var env: VmTestEnv

    @Before
    fun setUp() {
        kotlinx.coroutines.Dispatchers.setMain(UnconfinedTestDispatcher())
        env = VmTestEnv()
    }

    @After
    fun tearDown() {
        created.forEach { it.viewModelScope.cancel() } // stop collectors that dispatch on Main before Main is reset
        env.close()
        kotlinx.coroutines.Dispatchers.resetMain()
    }

    private val created = mutableListOf<AddViewModel>()
    private var submitsReported = 0
    private val submitListener = com.istarvin.fetcharr.notifications.SubmitListener { submitsReported++ }

    private fun vm(url: String? = null) = AddViewModel(
        SavedStateHandle(if (url == null) emptyMap() else mapOf("url" to url)),
        env.hub, env.settings, env.categoryRepository, env.serverSettings, env.submit, submitListener,
    ).also { created += it }

    private fun run(block: suspend () -> Unit) {
        env.await(block)
    }

    private suspend fun prepare() {
        env.configureServer()
        env.settings.setCachedCategories(env.categories)
        env.settings.setDefaultMovieCategoryId("movies")
    }

    @Test
    fun urlIsPrefilledFromTheRouteAndCategoryPreselected() = run {
        prepare()
        val vm = vm("https://example.com/v.m3u8")
        val s = vm.uiState.first { it.categoryId != null }
        assertEquals("https://example.com/v.m3u8", s.url)
        assertEquals("movies", s.categoryId)
        assertEquals("movie", s.layout)
    }

    @Test
    fun submitRulesRequireHttpUrlAndLayoutFields() = run {
        prepare()
        val vm = vm()
        vm.uiState.first { it.categoryId != null }
        assertFalse(vm.uiState.value.canSubmit)
        vm.onUrl("example.com/v")
        vm.onTitle("Dune")
        assertTrue(vm.uiState.value.urlInvalid)
        assertFalse(vm.uiState.value.canSubmit)
        vm.onUrl("https://example.com/v")
        assertTrue(vm.uiState.value.canSubmit)
        vm.addSubtitle()
        vm.updateSubtitle(vm.uiState.value.subtitles.single().id, url = "ftp://x")
        assertFalse(vm.uiState.value.canSubmit)
        vm.updateSubtitle(vm.uiState.value.subtitles.single().id, url = "")
        assertTrue("blank subtitle rows are ignored", vm.uiState.value.canSubmit)
        vm.selectCategory("tv")
        assertFalse(vm.uiState.value.canSubmit)
    }

    @Test
    fun successSendsHeadersAndSubtitlesThenEmitsDoneAndSnackbar() = run {
        prepare()
        env.server.enqueue(env.json(201, Fixtures.text("job-pending.json")))
        val vm = vm("https://cdn.example/dune2.m3u8")
        vm.uiState.first { it.categoryId != null }
        vm.onTitle("Dune Part Two")
        vm.onYear("2024")
        vm.addHeader()
        val h = vm.uiState.value.headers.single().id
        vm.updateHeader(h, name = "Referer", value = "https://example.com/")
        vm.addHeader() // blank row is dropped
        vm.addSubtitle()
        val sub = vm.uiState.value.subtitles.single().id
        vm.updateSubtitle(sub, url = "https://cdn.example/dune2.en.vtt", lang = "en")

        vm.submit()
        assertEquals(AddEvent.Done, vm.events.first())
        assertEquals(HubEvent.Snackbar("Added 1 download"), env.hub.events.first())
        assertEquals(1, submitsReported)

        val body = Json.parseToJsonElement(env.server.takeRequest().body.readUtf8()).jsonObject
        assertEquals("movies", body["category"]!!.jsonPrimitive.content)
        assertEquals("Dune Part Two", body["title"]!!.jsonPrimitive.content)
        assertEquals("2024", body["year"]!!.jsonPrimitive.content)
        assertEquals(setOf("Referer"), body["headers"]!!.jsonObject.keys)
        assertEquals("en", body["subtitles"]!!.jsonArray.single().jsonObject["lang"]!!.jsonPrimitive.content)
    }

    @Test
    fun aSecondShareReplacesTheUrlButKeepsOtherFields() = run {
        prepare()
        val vm = vm("https://cdn.example/first")
        vm.uiState.first { it.categoryId != null }
        vm.onTitle("Keep me")
        env.hub.replaceAddUrl("https://cdn.example/second")
        assertEquals("https://cdn.example/second", vm.uiState.value.url)
        assertEquals("Keep me", vm.uiState.value.form.title)
    }

    @Test
    fun errorsStayInlineAndKeepTheForm() = run {
        prepare()
        env.server.enqueue(env.json(400, Fixtures.text("error-invalid_request.json")))
        env.server.enqueue(env.json(409, Fixtures.text("error-duplicate.json")))
        val vm = vm("https://cdn.example/a")
        vm.uiState.first { it.categoryId != null }
        vm.onTitle("X")
        vm.submit()
        assertEquals("category 'foo' does not exist", vm.uiState.first { it.error != null }.error)
        vm.onTitle("Y") // editing clears the error
        vm.submit()
        assertEquals("Already in the queue", vm.uiState.first { it.error != null }.error)
        assertEquals("https://cdn.example/a", vm.uiState.value.url)
        assertFalse(vm.uiState.value.submitting)
        assertEquals(0, submitsReported)
    }

    @Test
    fun withoutServerSubmitIsDisabled() = run {
        env.settings.setCachedCategories(env.categories)
        val vm = vm("https://cdn.example/a")
        vm.uiState.first { it.settingsLoaded && it.categoryId != null }
        vm.onTitle("X")
        assertFalse(vm.uiState.value.serverConfigured)
        assertFalse(vm.uiState.value.canSubmit)
    }

    @Test
    fun categoryRefreshFailureCanBeRetriedWithoutLosingTheForm() = run {
        env.configureServer()
        env.server.enqueue(env.json(401, Fixtures.text("error-unauthorized.json")))
        val vm = vm("https://example.com/movie")
        vm.onTitle("Keep this title")
        val failed = vm.uiState.first { it.categoryError != null && !it.categoriesLoading }
        assertTrue(failed.categories.isEmpty())
        assertFalse(failed.canSubmit)

        env.server.enqueue(env.json(200, """{"version":"test","categories":[{"id":"movies","label":"Movies","layout":"movie"}]}"""))
        vm.refreshCategories()
        val ready = vm.uiState.first { it.categoryId == "movies" && !it.categoriesLoading }
        assertEquals(null, ready.categoryError)
        assertEquals("Keep this title", ready.form.title)
        assertEquals("https://example.com/movie", ready.url)
        assertTrue(ready.canSubmit)
        assertEquals(2, env.server.requestCount)
    }

    @Test
    fun emptyServerCategoriesFinishLoadingAndCanBeRefreshedAfterSetup() = run {
        env.configureServer()
        env.server.enqueue(env.json(200, """{"version":"test","categories":[]}"""))
        val vm = vm("https://example.com/movie")
        // Waiting for the request and repository info distinguishes the initial state from a successful empty response.
        env.categoryRepository.info.first { it != null }
        val empty = vm.uiState.first { !it.categoriesLoading }
        assertEquals(null, empty.categoryError)
        assertTrue(empty.categories.isEmpty())
        assertFalse(empty.canSubmit)

        env.server.enqueue(env.json(200, """{"version":"test","categories":[{"id":"misc","label":"Files","layout":"flat"}]}"""))
        vm.refreshCategories()
        val ready = vm.uiState.first { it.categoryId == "misc" && !it.categoriesLoading }
        assertEquals("flat", ready.layout)
    }

    @Test
    fun newCategoryIsAppendedToServerSettingsAndSelected() = run {
        prepare()
        val saved = mutableListOf<String>()
        env.server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when {
                request.method == "GET" && request.path == "/api/system/settings" -> env.json(200, Fixtures.text("server-settings.json"))
                request.method == "PUT" -> env.json(200, request.body.readUtf8().also { saved += it })
                else -> env.json(200, """{"version":"test","settings_supported":true,"categories":[]}""")
            }
        }
        val vm = vm("https://example.com/v")
        vm.uiState.first { it.categoryId != null }

        vm.openNewCategory()
        vm.saveNewCategory()
        val invalid = vm.uiState.value.newCategory!!
        assertTrue(invalid.showErrors && invalid.labelInvalid && invalid.pathInvalid)

        vm.editNewCategory { it.copy(label = " Anime ", path = "/media/anime", layout = "tv") }
        vm.saveNewCategory()
        val done = vm.uiState.first { it.newCategory == null && it.layout == "tv" }
        assertEquals("Anime", done.categories.single { it.id == done.categoryId }.label)

        val body = Json.parseToJsonElement(saved.single()).jsonObject
        val categories = body["categories"]!!.jsonArray.map { it.jsonObject }
        assertEquals(listOf("movies", done.categoryId), categories.map { it["id"]!!.jsonPrimitive.content })
        assertEquals("/media/anime", categories.last()["path"]!!.jsonPrimitive.content)
        assertEquals("1", body["revision"]!!.jsonPrimitive.content)
        assertEquals(1, body["media_servers"]!!.jsonArray.size)
    }
}
