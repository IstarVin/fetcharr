package com.istarvin.fetcharr.data.network

import javax.inject.Inject
import kotlin.time.TimeSource

sealed interface TestConnectionResult {
    data class Connected(val version: String, val latencyMs: Long) : TestConnectionResult

    data object Unreachable : TestConnectionResult

    data object WrongToken : TestConnectionResult

    data object NotFetcharr : TestConnectionResult
}

/** "Test connection" (app spec §5.5): `GET /api/info`. */
class ConnectionTester @Inject constructor(
    private val factory: FetcharrApiFactory,
) {
    suspend fun test(config: ServerConfig): TestConnectionResult {
        val api = try {
            factory.create(config)
        } catch (e: IllegalArgumentException) {
            return TestConnectionResult.NotFetcharr
        }
        val start = TimeSource.Monotonic.markNow()
        return when (val result = safeApiCall { api.info() }) {
            is ApiResult.Success ->
                TestConnectionResult.Connected(result.value.version, start.elapsedNow().inWholeMilliseconds)
            is ApiResult.Failure -> when (result.error) {
                ApiError.Unreachable -> TestConnectionResult.Unreachable
                ApiError.Unauthorized -> TestConnectionResult.WrongToken
                else -> TestConnectionResult.NotFetcharr
            }
        }
    }
}
