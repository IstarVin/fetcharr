package dev.istarvin.fetcharr.ui.incoming

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import dev.istarvin.fetcharr.data.CategoryRepository
import dev.istarvin.fetcharr.data.network.FetcharrApiFactory
import dev.istarvin.fetcharr.data.network.FetcharrApiProvider
import dev.istarvin.fetcharr.data.network.FetcharrJson
import dev.istarvin.fetcharr.data.network.dto.CategoryDto
import dev.istarvin.fetcharr.data.settings.SettingsRepository
import dev.istarvin.fetcharr.incoming.IncomingCoordinator
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import java.io.File
import java.util.concurrent.TimeUnit

/** Real settings (temp DataStore), real network layer against MockWebServer, hub on a real-time scope. */
internal class VmTestEnv(tmpDir: File) {
    val scope = CoroutineScope(Job() + Dispatchers.IO)
    val server = MockWebServer().also { it.start() }
    private val client = OkHttpClient.Builder()
        .connectTimeout(3, TimeUnit.SECONDS)
        .readTimeout(3, TimeUnit.SECONDS)
        .build()
    private val factory = FetcharrApiFactory(client, FetcharrJson)
    val settings = SettingsRepository(
        PreferenceDataStoreFactory.create(scope = scope) { File(tmpDir, "settings.preferences_pb") },
        FetcharrJson,
        scope,
    )
    val categoryRepository = CategoryRepository(FetcharrApiProvider(factory, settings), factory, settings)
    val submit = SubmitUseCase(FetcharrApiProvider(factory, settings))
    val hub = IncomingHub(IncomingCoordinator(scope), IntentRouter())

    val categories = listOf(
        CategoryDto("movies", "Movies", "movie"),
        CategoryDto("tv", "TV Shows", "tv"),
        CategoryDto("anime", "Anime", "tv"),
        CategoryDto("misc", "Misc", "flat"),
    )

    fun <T> await(block: suspend () -> T): T = runBlocking { withTimeout(5_000) { block() } }

    suspend fun configureServer() {
        settings.setServer(server.url("/").toString().trimEnd('/'), "")
        settings.config.first { it != null }
    }

    fun json(code: Int, body: String) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    fun close() {
        runCatching { server.shutdown() }
        scope.cancel()
    }
}

