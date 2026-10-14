package dev.istarvin.fetcharr.data

import dev.istarvin.fetcharr.data.network.ApiError
import dev.istarvin.fetcharr.data.network.ApiResult
import dev.istarvin.fetcharr.data.network.FetcharrApi
import dev.istarvin.fetcharr.data.network.FetcharrApiFactory
import dev.istarvin.fetcharr.data.network.FetcharrApiProvider
import dev.istarvin.fetcharr.data.network.ServerConfig
import dev.istarvin.fetcharr.data.network.dto.CategoryDto
import dev.istarvin.fetcharr.data.network.dto.InfoDto
import dev.istarvin.fetcharr.data.network.safeApiCall
import dev.istarvin.fetcharr.data.settings.SettingsRepository
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import javax.inject.Inject
import javax.inject.Singleton

/** Categories from `GET /api/info`, cached in DataStore so pickers work offline (app spec §3.2). */
@Singleton
class CategoryRepository @Inject constructor(
    private val apiProvider: FetcharrApiProvider,
    private val apiFactory: FetcharrApiFactory,
    private val settings: SettingsRepository,
) {
    /** The cached categories. Empty until the first successful refresh. */
    val categories: Flow<List<CategoryDto>> =
        settings.settings.map { it.cachedCategories }.distinctUntilChanged()

    private val _info = MutableStateFlow<InfoDto?>(null)

    /** The last `/api/info` fetched from the saved server in this process (version, yt-dlp), or null. */
    val info: StateFlow<InfoDto?> = _info

    /**
     * Fetches `/api/info` from the saved server and caches its categories.
     * Fails with [ApiError.Unreachable] when no server is saved.
     */
    suspend fun refresh(): ApiResult<InfoDto> =
        fetchAndCache { apiProvider.current() }.also { if (it is ApiResult.Success) _info.value = it.value }

    /** Like [refresh], but against [config], for example values entered but not saved yet. */
    suspend fun refresh(config: ServerConfig): ApiResult<InfoDto> = fetchAndCache { apiFactory.create(config) }

    private suspend fun fetchAndCache(api: () -> FetcharrApi?): ApiResult<InfoDto> {
        val resolved = try {
            api()
        } catch (e: IllegalArgumentException) {
            null
        } ?: return ApiResult.Failure(ApiError.Unreachable)
        val result = safeApiCall { resolved.info() }
        if (result is ApiResult.Success) settings.setCachedCategories(result.value.categories)
        return result
    }
}
