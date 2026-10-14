package dev.istarvin.fetcharr.ui.incoming

import dev.istarvin.fetcharr.data.network.Fixtures
import dev.istarvin.fetcharr.incoming.IncomingRequest
import dev.istarvin.fetcharr.incoming.IncomingSubtitle
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

@OptIn(ExperimentalCoroutinesApi::class)
class IncomingViewModelTest {
    @get:Rule
    val tmp = TemporaryFolder()

    private lateinit var env: VmTestEnv
    private lateinit var vm: IncomingViewModel
    private val submitListener = dev.istarvin.fetcharr.notifications.SubmitListener { }

    @Before
    fun setUp() {
        kotlinx.coroutines.Dispatchers.setMain(UnconfinedTestDispatcher())
        env = VmTestEnv(tmp.root)
        vm = IncomingViewModel(env.hub, env.settings, env.categoryRepository, env.submit, submitListener)
    }


    @After
    fun tearDown() {
        vm.viewModelScope.cancel() // stop collectors that dispatch on Main before Main is reset
        env.close()
        kotlinx.coroutines.Dispatchers.resetMain()
    }

    private fun runVm(block: suspend () -> Unit) {
        env.await(block)
    }

    private fun movie(title: String? = "Dune", year: Int? = 2024, hint: String? = null) = IncomingRequest.Single(
        hint, "https://cdn.example/dune.m3u8", title, year, null, null, null,
        mapOf("Referer" to "https://example.com/"), listOf(IncomingSubtitle("https://cdn.example/dune.en.vtt", "en")),
    )

    private fun episode(n: Int, show: String = "Andor") = IncomingRequest.Single(
        null, "https://cdn.example/andor/$n.m3u8", "Ep $n", 2022, show, 1, n, emptyMap(), emptyList(),
    )

    private suspend fun open(request: IncomingRequest, categories: Boolean = true, defaults: Boolean = true): IncomingUiState {
        if (categories) env.settings.setCachedCategories(env.categories)
        if (defaults) {
            env.settings.setDefaultMovieCategoryId("misc")
            env.settings.setDefaultTvCategoryId("anime")
        }
        env.hub.coordinator.onRequest(request)
        return vm.uiState.first { it.sheet != null && it.settingsLoaded && (!categories || it.serverCategories.isNotEmpty()) }
    }

    @Test
    fun movieUsesDefaultMovieCategoryAndFillsFields() = runVm {
        val s = open(movie())
        assertEquals("misc", s.categoryId)
        assertEquals("Dune", s.form.title)
        assertEquals("2024", s.form.year)
        assertEquals("cdn.example · 1 subtitle: en", s.sourceLine)
        assertFalse(s.isSeasonMode)
    }

    @Test
    fun hintWinsOverDefaults() = runVm {
        assertEquals("movies", open(movie(hint = "movies")).categoryId)
    }

    @Test
    fun episodeUsesDefaultTvCategoryAndShowsSingleLayout() = runVm {
        val s = open(episode(3))
        assertEquals("anime", s.categoryId)
        assertFalse("one-episode season sheet uses the single layout", s.isSeasonMode)
        assertEquals("tv", s.layout)
        assertEquals("Andor", s.form.show)
        assertEquals("1", s.form.season)
        assertEquals("3", s.form.episode)
        assertEquals("Ep 3", s.form.episodeTitle)
        assertEquals(4, s.categories.size)
    }

    @Test
    fun seasonLayoutOffersOnlyTvCategoriesAndRepreselectsInvalidChoice() = runVm {
        open(episode(1))
        vm.selectCategory("movies") // allowed in the single layout
        assertEquals("movies", vm.uiState.value.categoryId)
        env.hub.coordinator.onRequest(episode(2))
        val s = vm.uiState.first { it.isSeasonMode }
        assertEquals(listOf("tv", "anime"), s.categories.map { it.id })
        assertEquals("anime", s.categoryId)
    }

    @Test
    fun preselectionRerunsWhenCategoriesArriveLateButOnlyIfNothingChosen() = runVm {
        val s0 = open(movie(), categories = false, defaults = false)
        assertNull(s0.categoryId)
        env.settings.setDefaultMovieCategoryId("movies")
        env.settings.setCachedCategories(env.categories)
        val s1 = vm.uiState.first { it.categoryId != null }
        assertEquals("movies", s1.categoryId)

        vm.selectCategory("misc")
        env.settings.setDefaultMovieCategoryId("misc2") // settings change must not override the user's choice
        env.settings.setCachedCategories(env.categories.take(3) + env.categories[3])
        assertEquals("misc", vm.uiState.value.categoryId)
    }

    @Test
    fun editsAreResetForTheNextSheet() = runVm {
        open(movie())
        vm.onTitle("Edited")
        assertEquals("Edited", vm.uiState.value.form.title)
        env.hub.coordinator.onRequest(episode(1)) // queued behind the movie
        assertEquals(1, vm.uiState.first { it.waitingCount == 1 }.waitingCount)
        vm.cancel()
        val s = vm.uiState.first { it.sheet is dev.istarvin.fetcharr.incoming.IncomingSheet.SeasonSheet }
        assertEquals("Andor", s.form.show)
    }

    @Test
    fun downloadEnabledRules() = runVm {
        env.configureServer()
        var s = open(movie())
        assertTrue(s.canDownload)
        vm.selectCategory("movies") // movie layout: Title + Year (the default category here is flat)

        vm.onTitle("  ")
        assertFalse("empty required title", vm.uiState.value.canDownload)
        vm.onTitle("Dune")
        vm.onYear("20") // not 4 digits
        assertFalse(vm.uiState.value.canDownload)
        vm.onYear("")
        assertTrue("year is optional", vm.uiState.value.canDownload)

        vm.selectCategory("tv") // tv layout needs show/season/episode
        assertFalse(vm.uiState.value.canDownload)
        vm.onShow("Dune")
        vm.onSeason("1")
        vm.onEpisode("1")
        assertTrue(vm.uiState.value.canDownload)

        vm.selectCategory("misc") // flat: title only
        assertTrue(vm.uiState.value.canDownload)
        s = vm.uiState.value
        assertEquals(1, s.selectedCount)
    }

    @Test
    fun downloadDisabledWithoutServer() = runVm {
        val s = open(movie())
        assertFalse(s.serverConfigured)
        assertFalse(s.canDownload)
    }

    @Test
    fun seasonSelectionRules() = runVm {
        env.configureServer()
        open(episode(1))
        env.hub.coordinator.onRequest(episode(2))
        env.hub.coordinator.onRequest(episode(3))
        vm.uiState.first { it.isSeasonMode && it.sheet != null && it.selectedCount == 3 }
        // Still within the 2 s receiving window.
        assertTrue(vm.uiState.value.receiving)
        assertFalse(vm.uiState.value.canDownload)
        vm.uiState.first { !it.receiving }
        assertTrue(vm.uiState.value.canDownload)
        vm.setAllSelected(false)
        assertEquals(0, vm.uiState.value.selectedCount)
        assertFalse(vm.uiState.value.canDownload)
        vm.toggleEpisode(2)
        assertTrue(vm.uiState.value.canDownload)
        vm.onShow("")
        assertFalse(vm.uiState.value.canDownload)
    }

    @Test
    fun successfulSubmitClosesSheetAndEmitsSnackbar() = runVm {
        env.configureServer()
        env.server.enqueue(env.json(201, Fixtures.text("job-pending.json")))
        open(movie())
        vm.download()
        val message = (vm.events.first() as HubEvent.Snackbar).message
        assertEquals("Added 1 download", message)
        assertNull(vm.uiState.first { it.sheet == null }.sheet)

        val req = env.server.takeRequest()
        val body = Json.parseToJsonElement(req.body.readUtf8()).jsonObject
        assertEquals("misc", body["category"]!!.jsonPrimitive.content) // default movie category is flat
        assertEquals("Dune", body["title"]!!.jsonPrimitive.content)
        assertEquals("Referer", body["headers"]!!.jsonObject.keys.single())
    }

    @Test
    fun duplicateClosesWithAlreadyInTheQueue() = runVm {
        env.configureServer()
        env.server.enqueue(env.json(409, Fixtures.text("error-duplicate.json")))
        open(movie())
        vm.download()
        assertEquals("Already in the queue", (vm.events.first() as HubEvent.Snackbar).message)
        vm.uiState.first { it.sheet == null }
    }

    @Test
    fun badRequestStaysOpenWithServerMessageThenCanRetry() = runVm {
        env.configureServer()
        env.server.enqueue(env.json(400, Fixtures.text("error-invalid_request.json")))
        open(movie())
        vm.download()
        val failed = vm.uiState.first { it.error != null }
        assertEquals("category 'foo' does not exist", failed.error)
        assertNotNull(failed.sheet)
        assertFalse(failed.submitting)
        assertTrue(failed.canDownload)
        vm.onTitle("Dune 2") // editing clears the error
        assertNull(vm.uiState.value.error)
    }

    @Test
    fun batchSubmitFromSeasonSheet() = runVm {
        env.configureServer()
        env.server.enqueue(env.json(201, Fixtures.text("batch-create-response.json")))
        open(episode(1))
        env.hub.coordinator.onRequest(episode(2))
        vm.uiState.first { it.isSeasonMode }
        vm.uiState.first { !it.receiving }
        vm.download()
        assertEquals("Added 2 · 1 already queued", (vm.events.first() as HubEvent.Snackbar).message)
        assertEquals("/api/jobs/batch", env.server.takeRequest().path)
    }
}
