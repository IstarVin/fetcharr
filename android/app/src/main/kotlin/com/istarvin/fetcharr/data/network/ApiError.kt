package com.istarvin.fetcharr.data.network

import com.istarvin.fetcharr.data.network.dto.ErrorEnvelope
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import retrofit2.HttpException
import java.io.IOException

/** Everything that can go wrong with an API call (app spec §7). */
sealed interface ApiError {
    /** The text to show the user (snackbar or inline). */
    val userMessage: String

    data class SettingsConflict(override val userMessage: String) : ApiError

    data class CategoryBusy(override val userMessage: String) : ApiError

    /** I/O error, DNS failure or timeout (10 s connect, 30 s read). */
    data object Unreachable : ApiError {
        override val userMessage = "Can't reach server"
    }

    /** 401. */
    data object Unauthorized : ApiError {
        override val userMessage = "Wrong API token"
    }

    /** 400, including `invalid_request`. The server's message is meant to be shown. */
    data class BadRequest(val code: String, val message: String) : ApiError {
        override val userMessage: String get() = message.ifBlank { "Invalid request" }
    }

    data class NotFound(val message: String) : ApiError {
        override val userMessage: String get() = message.ifBlank { "Not found" }
    }

    /** 409 `duplicate`. [details] is the envelope's optional `details`, if any. */
    data class Duplicate(val message: String, val details: JsonElement? = null) : ApiError {
        override val userMessage: String get() = "Already in the queue"
    }

    /** 409 `invalid_state`: the job changed under us. Refresh it silently. */
    data class InvalidState(val message: String) : ApiError {
        override val userMessage: String get() = "Job already changed"
    }

    /** 409 `ytdlp_busy`. */
    data object YtdlpBusy : ApiError {
        override val userMessage = "Downloads are running — try again when they finish"
    }

    /** 409 `ytdlp_unmanaged`. */
    data object YtdlpUnmanaged : ApiError {
        override val userMessage = "yt-dlp is not managed by Fetcharr on this server"
    }

    /** 5xx. */
    data class Server(val status: Int) : ApiError {
        override val userMessage: String get() = "Server error — try again"
    }

    /** A 2xx body that didn't parse, or a reply that isn't a Fetcharr error envelope: not a Fetcharr server. */
    data object InvalidResponse : ApiError {
        override val userMessage = "Unexpected response from server"
    }

    /** An envelope or status nothing above covers. [status] is 0 for unexpected local exceptions. */
    data class Unknown(val status: Int, val code: String?, val message: String?) : ApiError {
        override val userMessage: String get() = message?.takeIf { it.isNotBlank() } ?: "Something went wrong"
    }
}

/** Maps HTTP replies and exceptions to [ApiError]. */
object ApiErrorMapper {
    /** Parses [body] as an error envelope, or null when it is not one. */
    fun parseEnvelope(json: Json, body: String?): ErrorEnvelope? {
        if (body.isNullOrBlank()) return null
        return try {
            json.decodeFromString<ErrorEnvelope>(body)
        } catch (e: SerializationException) {
            null
        } catch (e: IllegalArgumentException) {
            null
        }
    }

    /** Maps a non-2xx reply. By `error.code` first, HTTP status second. */
    fun fromHttp(json: Json, status: Int, body: String?): ApiError {
        val envelope = parseEnvelope(json, body)?.error
        val message = envelope?.message
        when (envelope?.code) {
            "settings_conflict" -> return ApiError.SettingsConflict(message.orEmpty())
            "category_busy" -> return ApiError.CategoryBusy(message.orEmpty())
            "unauthorized" -> return ApiError.Unauthorized
            "invalid_request" -> return ApiError.BadRequest("invalid_request", message.orEmpty())
            "not_found" -> return ApiError.NotFound(message.orEmpty())
            "invalid_state" -> return ApiError.InvalidState(message.orEmpty())
            "duplicate" -> return ApiError.Duplicate(message.orEmpty(), envelope.details)
            "ytdlp_busy" -> return ApiError.YtdlpBusy
            "ytdlp_unmanaged" -> return ApiError.YtdlpUnmanaged
            "internal" -> return ApiError.Server(status)
        }
        return when {
            status == 401 -> ApiError.Unauthorized
            status in 500..599 -> ApiError.Server(status)
            envelope == null -> ApiError.InvalidResponse
            status == 400 -> ApiError.BadRequest(envelope.code, envelope.message)
            status == 404 -> ApiError.NotFound(envelope.message)
            else -> ApiError.Unknown(status, envelope.code, envelope.message)
        }
    }

    /** Maps an exception thrown by a Retrofit call. Rethrows [CancellationException]. */
    fun fromThrowable(json: Json, t: Throwable): ApiError =
        when (t) {
            is CancellationException -> throw t
            is HttpException -> {
                val body = try {
                    t.response()?.errorBody()?.string()
                } catch (e: IOException) {
                    null
                }
                fromHttp(json, t.code(), body)
            }
            is IOException -> ApiError.Unreachable
            is SerializationException -> ApiError.InvalidResponse
            is IllegalArgumentException -> ApiError.InvalidResponse
            else -> ApiError.Unknown(0, null, t.message)
        }
}

sealed interface ApiResult<out T> {
    data class Success<T>(val value: T) : ApiResult<T>

    data class Failure(val error: ApiError) : ApiResult<Nothing>
}

fun <T> ApiResult<T>.getOrNull(): T? = (this as? ApiResult.Success)?.value

fun <T> ApiResult<T>.errorOrNull(): ApiError? = (this as? ApiResult.Failure)?.error

/**
 * Runs a Retrofit call and turns every failure into [ApiResult.Failure]. Coroutine cancellation is rethrown.
 * [json] is only used to parse error bodies.
 */
suspend fun <T> safeApiCall(json: Json = FetcharrJson, block: suspend () -> T): ApiResult<T> =
    try {
        ApiResult.Success(block())
    } catch (t: Throwable) {
        ApiResult.Failure(ApiErrorMapper.fromThrowable(json, t))
    }
