package com.istarvin.fetcharr.data

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.data.settings.SettingsRepository
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
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
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

class CategoryRepositoryTest {
    @get:Rule val tmp = TemporaryFolder()
    private val scope = CoroutineScope(Job() + Dispatchers.IO)
    private val server = MockWebServer()
    private lateinit var settings: SettingsRepository
    private lateinit var repository: CategoryRepository
    private val original = CategoryDto("original", "Original", "movie")
    private val reply = """{"version":"test","categories":[{"id":"replacement","label":"Replacement","layout":"tv"}]}"""

    @Before fun setup() {
        server.start()
        settings = SettingsRepository(
            PreferenceDataStoreFactory.create(scope = scope) { tmp.newFile("categories.preferences_pb") },
            FetcharrJson, scope,
        )
        repository = CategoryRepository(FetcharrApiFactory(OkHttpClient(), FetcharrJson), settings, scope)
    }

    @After fun cleanup() {
        server.shutdown()
        scope.cancel()
    }

    private fun <T> await(block: suspend () -> T): T = runBlocking { withTimeout(30_000) { block() } }

    private suspend fun save(url: String, token: String = "token") {
        settings.setServer(url, token)
        settings.config.first { it == ServerConfig(url, token) }
    }

    @Test fun testingAnotherServerDoesNotReplaceSavedCategories() = await {
        save("http://saved.example")
        settings.setCachedCategories(listOf(original))
        settings.setDefaultMovieCategoryId(original.id)
        server.enqueue(MockResponse().setBody(reply))

        assertTrue(repository.refresh(ServerConfig(server.url("/").toString().trimEnd('/'), "token")) is ApiResult.Success)
        assertEquals(listOf(original), settings.settings.first().cachedCategories)
        assertEquals(original.id, settings.settings.first().defaultMovieCategoryId)
        assertNull(repository.info.value)
    }

    @Test fun delayedReplyCannotReplaceTheNewServersCache() = await {
        val release = CountDownLatch(1)
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                check(release.await(5, TimeUnit.SECONDS))
                return MockResponse().setBody(reply)
            }
        }
        save(server.url("/").toString().trimEnd('/'))
        val pending = scope.async { repository.refresh() }
        try {
            assertTrue(server.takeRequest(3, TimeUnit.SECONDS) != null)
            save("http://new.example")
            settings.setCachedCategories(listOf(original))
        } finally {
            release.countDown()
        }
        assertTrue(pending.await() is ApiResult.Success)
        assertEquals(listOf(original), settings.settings.first().cachedCategories)
        assertNull(repository.info.value)
    }

    @Test fun savedReplyReconcilesDefaultsAndInfoIsHiddenAfterSwitching() = await {
        save(server.url("/").toString().trimEnd('/'))
        settings.setDefaultMovieCategoryId("removed")
        server.enqueue(MockResponse().setBody(reply))
        assertTrue(repository.refresh() is ApiResult.Success)
        assertEquals("test", repository.info.first { it != null }?.version)
        assertEquals("replacement", settings.settings.first().cachedCategories.single().id)
        assertNull(settings.settings.first().defaultMovieCategoryId)

        save("http://new.example")
        repository.info.first { it == null }
        assertNull(repository.info.value)
    }
}
