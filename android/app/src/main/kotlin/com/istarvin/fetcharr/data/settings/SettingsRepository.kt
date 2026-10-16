package com.istarvin.fetcharr.data.settings

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.MutablePreferences
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.ServerConfigProvider
import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.di.ApplicationScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import javax.inject.Inject
import javax.inject.Singleton

/** DataStore-backed settings (app spec §3.1, §3.2). */
@Singleton
class SettingsRepository @Inject constructor(
    private val dataStore: DataStore<Preferences>,
    private val json: Json,
    @ApplicationScope scope: CoroutineScope,
) : ServerConfigProvider {

    val settings: Flow<AppSettings> = dataStore.data.map { it.toAppSettings() }

    /** Null while no URL is saved, and until the first DataStore read completes. */
    override val config: StateFlow<ServerConfig?> = settings
        .map { s -> s.serverUrl.takeIf { it.isNotBlank() }?.let { ServerConfig(it, s.token.takeIf { t -> t.isNotBlank() }) } }
        .distinctUntilChanged()
        .stateIn(scope, SharingStarted.Eagerly, null)

    /** Saves both server fields in one write. Blank [url] clears the server. */
    suspend fun setServer(url: String, token: String) {
        dataStore.edit {
            if (it[SERVER_URL].orEmpty() != url) {
                it.remove(CACHED_CATEGORIES)
                it.remove(DEFAULT_MOVIE)
                it.remove(DEFAULT_TV)
            }
            it[SERVER_URL] = url
            it[TOKEN] = token
        }
    }

    suspend fun setDefaultMovieCategoryId(id: String?) {
        dataStore.edit { it.putOrRemove(DEFAULT_MOVIE, id) }
    }

    suspend fun setDefaultTvCategoryId(id: String?) {
        dataStore.edit { it.putOrRemove(DEFAULT_TV, id) }
    }

    suspend fun setCachedCategories(categories: List<CategoryDto>) {
        val encoded = json.encodeToString(CATEGORIES_SERIALIZER, categories)
        dataStore.edit { it[CACHED_CATEGORIES] = encoded }
    }

    /** Cache and reconcile defaults atomically, only if this is still the saved server. */
    suspend fun reconcileServerCategories(connection: ServerConfig, categories: List<CategoryDto>) {
        val encoded = json.encodeToString(CATEGORIES_SERIALIZER, categories)
        dataStore.edit {
            if (it[SERVER_URL] != connection.baseUrl || it[TOKEN].orEmpty() != connection.token.orEmpty()) return@edit
            it[CACHED_CATEGORIES] = encoded
            val movie = it[DEFAULT_MOVIE]
            val tv = it[DEFAULT_TV]
            if (categories.none { c -> c.id == movie && c.layout in listOf(CategoryDto.LAYOUT_MOVIE, CategoryDto.LAYOUT_FLAT) }) {
                it.remove(DEFAULT_MOVIE)
            }
            if (categories.none { c -> c.id == tv && c.layout == CategoryDto.LAYOUT_TV }) it.remove(DEFAULT_TV)
        }
    }

    suspend fun setLastSeenFinishedAt(epochMs: Long) {
        dataStore.edit { it[LAST_SEEN] = epochMs }
    }

    /** Sets the last seen time to [nowMs] unless one is already stored. */
    suspend fun initLastSeenIfAbsent(nowMs: Long) {
        dataStore.edit { if (it[LAST_SEEN] == null) it[LAST_SEEN] = nowMs }
    }

    /** True once the POST_NOTIFICATIONS prompt has been shown (it is only asked once). */
    val notificationPermissionAsked: Flow<Boolean> = dataStore.data.map { it[NOTIFICATION_PERMISSION_ASKED] ?: false }

    suspend fun markNotificationPermissionAsked() {
        dataStore.edit { it[NOTIFICATION_PERMISSION_ASKED] = true }
    }

    private fun Preferences.toAppSettings() = AppSettings(
        serverUrl = this[SERVER_URL].orEmpty(),
        token = this[TOKEN].orEmpty(),
        defaultMovieCategoryId = this[DEFAULT_MOVIE],
        defaultTvCategoryId = this[DEFAULT_TV],
        cachedCategories = this[CACHED_CATEGORIES]?.let {
            try {
                json.decodeFromString(CATEGORIES_SERIALIZER, it)
            } catch (e: IllegalArgumentException) { // SerializationException is a subclass
                emptyList()
            }
        }.orEmpty(),
        lastSeenFinishedAt = this[LAST_SEEN],
    )

    private fun MutablePreferences.putOrRemove(
        key: Preferences.Key<String>,
        value: String?,
    ) {
        if (value == null) remove(key) else this[key] = value
    }

    private companion object {
        val SERVER_URL = stringPreferencesKey("server_url")
        val TOKEN = stringPreferencesKey("token")
        val DEFAULT_MOVIE = stringPreferencesKey("default_movie_category_id")
        val DEFAULT_TV = stringPreferencesKey("default_tv_category_id")
        val CACHED_CATEGORIES = stringPreferencesKey("cached_categories")
        val LAST_SEEN = longPreferencesKey("last_seen_finished_at")
        val NOTIFICATION_PERMISSION_ASKED = booleanPreferencesKey("notification_permission_asked")
        val CATEGORIES_SERIALIZER = ListSerializer(CategoryDto.serializer())
    }
}
