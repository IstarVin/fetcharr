package dev.istarvin.fetcharr.data.settings

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import dev.istarvin.fetcharr.data.network.FetcharrJson
import dev.istarvin.fetcharr.data.network.ServerConfig
import dev.istarvin.fetcharr.data.network.dto.CategoryDto
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class SettingsRepositoryTest {
    @get:Rule
    val tmp = TemporaryFolder()

    private val scope = CoroutineScope(Job() + Dispatchers.IO)
    private lateinit var dataStore: DataStore<Preferences>
    private lateinit var repo: SettingsRepository

    @Before
    fun setUp() {
        dataStore = PreferenceDataStoreFactory.create(scope = scope) { tmp.newFile("settings.preferences_pb") }
        repo = SettingsRepository(dataStore, FetcharrJson, scope)
    }

    @After
    fun tearDown() {
        scope.cancel()
    }

    private fun <T> await(block: suspend () -> T): T = runBlocking { withTimeout(5_000) { block() } }

    @Test
    fun defaultsWhenEmpty() = await {
        assertEquals(AppSettings(), repo.settings.first())
        assertNull(repo.config.value)
    }

    @Test
    fun roundTrip() = await {
        val categories = listOf(
            CategoryDto("movies", "Movies", "movie", "/media/movies", 123L),
            CategoryDto("tv", "TV", "tv"),
        )
        repo.setServer("http://host:8080", "secret")
        repo.setDefaultMovieCategoryId("movies")
        repo.setDefaultTvCategoryId("tv")
        repo.setCachedCategories(categories)
        repo.setLastSeenFinishedAt(1_700_000_000_000)

        assertEquals(
            AppSettings("http://host:8080", "secret", "movies", "tv", categories, 1_700_000_000_000),
            repo.settings.first(),
        )
    }

    @Test
    fun defaultsCanBeCleared() = await {
        repo.setDefaultMovieCategoryId("movies")
        repo.setDefaultMovieCategoryId(null)
        assertNull(repo.settings.first().defaultMovieCategoryId)
    }

    @Test
    fun configFollowsUrlAndBlankUrlIsNull() = await {
        repo.setServer("http://host:8080", "tok")
        assertEquals(ServerConfig("http://host:8080", "tok"), repo.config.first { it != null })

        repo.setServer("http://host:8080", "")
        assertEquals(ServerConfig("http://host:8080", null), repo.config.first { it?.token == null })

        repo.setServer("", "tok")
        assertNull(repo.config.first { it == null })
    }

    @Test
    fun initLastSeenOnlyWhenAbsent() = await {
        repo.initLastSeenIfAbsent(100)
        assertEquals(100L, repo.settings.first().lastSeenFinishedAt)
        repo.initLastSeenIfAbsent(200)
        assertEquals(100L, repo.settings.first().lastSeenFinishedAt)
        repo.setLastSeenFinishedAt(300)
        assertEquals(300L, repo.settings.first().lastSeenFinishedAt)
    }

    @Test
    fun corruptCachedCategoriesDecodeAsEmpty() = await {
        dataStore.updateData { prefs ->
            prefs.toMutablePreferences().apply {
                this[androidx.datastore.preferences.core.stringPreferencesKey("cached_categories")] = "not json"
            }
        }
        assertEquals(emptyList<CategoryDto>(), repo.settings.first().cachedCategories)
    }
}
