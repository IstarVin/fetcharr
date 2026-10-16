package com.istarvin.fetcharr.data

import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.FetcharrApiFactory
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.data.network.dto.InfoDto
import com.istarvin.fetcharr.data.network.safeApiCall
import com.istarvin.fetcharr.data.settings.SettingsRepository
import com.istarvin.fetcharr.di.ApplicationScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import javax.inject.Inject
import javax.inject.Singleton

/** Categories from `GET /api/info`, cached in DataStore so pickers work offline (app spec §3.2). */
@Singleton
class CategoryRepository @Inject constructor(
    private val apiFactory: FetcharrApiFactory,
    private val settings: SettingsRepository,
    @ApplicationScope scope: CoroutineScope,
) {
    /** The cached categories. Empty until the first successful refresh. */
    val categories: Flow<List<CategoryDto>> =
        settings.settings.map { it.cachedCategories }.distinctUntilChanged()

    private val fetchedInfo = MutableStateFlow<Pair<ServerConfig, InfoDto>?>(null)

    /** The last `/api/info` fetched from the saved server in this process (version, yt-dlp), or null. */
    val info: StateFlow<InfoDto?> = combine(settings.config, fetchedInfo) { connection, cached ->
        cached?.takeIf { it.first == connection }?.second
    }.stateIn(scope, SharingStarted.Eagerly, null)

    /**
     * Fetches `/api/info` from the saved server and caches its categories.
     * Fails with [ApiError.Unreachable] when no server is saved.
     */
    suspend fun refresh(): ApiResult<InfoDto> {
        val connection = settings.config.value ?: return ApiResult.Failure(ApiError.Unreachable)
        return refresh(connection)
    }

    /** Like [refresh], but against [config], for example values entered but not saved yet. */
    suspend fun refresh(config: ServerConfig): ApiResult<InfoDto> {
        val result = safeApiCall { apiFactory.create(config).info() }
        if (result is ApiResult.Success) acceptServerInfo(config, result.value)
        return result
    }

    /** Accept a response only for the current connection, never a stale editor request. */
    suspend fun acceptServerInfo(connection: ServerConfig, info: InfoDto) {
        if (settings.config.value != connection) return
        settings.reconcileServerCategories(connection, info.categories)
        if (settings.config.value == connection) fetchedInfo.value = connection to info
    }
}
