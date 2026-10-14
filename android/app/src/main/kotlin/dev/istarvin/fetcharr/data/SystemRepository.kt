package dev.istarvin.fetcharr.data

import dev.istarvin.fetcharr.data.network.ApiError
import dev.istarvin.fetcharr.data.network.ApiResult
import dev.istarvin.fetcharr.data.network.FetcharrApiProvider
import dev.istarvin.fetcharr.data.network.dto.YtdlpUpdateResponse
import dev.istarvin.fetcharr.data.network.safeApiCall
import javax.inject.Inject
import javax.inject.Singleton

/** Server-wide actions that do not belong to a job (server spec section 6.3, the system endpoints). */
@Singleton
class SystemRepository @Inject constructor(
    private val apiProvider: FetcharrApiProvider,
) {
    /** `POST /api/system/ytdlp/update`. Fails with [ApiError.Unreachable] when no server is saved. */
    suspend fun updateYtdlp(): ApiResult<YtdlpUpdateResponse> {
        val api = try {
            apiProvider.current()
        } catch (e: IllegalArgumentException) {
            null
        } ?: return ApiResult.Failure(ApiError.Unreachable)
        return safeApiCall { api.updateYtdlp() }
    }
}
