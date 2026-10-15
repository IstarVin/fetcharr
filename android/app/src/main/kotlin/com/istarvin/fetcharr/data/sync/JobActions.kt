package com.istarvin.fetcharr.data.sync

import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.FetcharrApi
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.RetryRequest
import com.istarvin.fetcharr.data.network.dto.WsEvent
import com.istarvin.fetcharr.data.network.safeApiCall
import javax.inject.Inject
import javax.inject.Singleton

sealed interface ActionOutcome {
    data object Ok : ActionOutcome

    /** The action failed. [message] is ready to show in a snackbar. */
    data class Failed(val message: String) : ActionOutcome

    /**
     * 409 `invalid_state`: the job moved on before the action arrived. The store has been refreshed silently;
     * show [message] ("Job already changed") in a snackbar.
     */
    data class Changed(val message: String) : ActionOutcome

    /** No usable server is saved. */
    data object NotConfigured : ActionOutcome
}

/**
 * Pause, resume, cancel, retry and delete for jobs and batches (server §6.3).
 *
 * On success the returned job(s) are upserted into [ActiveJobsStore] for a snappy UI; the matching WebSocket event
 * repeats the same state, which is harmless. Deleting touches nothing here: only finished jobs can be deleted
 * and the store holds active ones.
 */
@Singleton
class JobActions @Inject constructor(
    private val apiProvider: FetcharrApiProvider,
    private val store: ActiveJobsStore,
) {
    suspend fun pause(jobId: String): ActionOutcome = job(jobId) { it.pauseJob(jobId) }

    suspend fun resume(jobId: String): ActionOutcome = job(jobId) { it.resumeJob(jobId) }

    suspend fun cancel(jobId: String): ActionOutcome = job(jobId) { it.cancelJob(jobId) }

    suspend fun retry(jobId: String): ActionOutcome = job(jobId) { it.retryJob(jobId) }

    /**
     * Retry with an explicit [overwrite] flag. `true` sends `{"overwrite":true}` and replaces an existing library
     * file (`already_exists`); `false` is the same as [retry] (no body).
     */
    suspend fun retry(jobId: String, overwrite: Boolean): ActionOutcome =
        if (overwrite) job(jobId) { it.retryJobWithBody(jobId, RetryRequest(overwrite = true)) } else retry(jobId)

    /**
     * `DELETE /api/jobs`: removes every finished job record (files are kept). The caller refreshes history.
     */
    suspend fun clearHistory(): ActionOutcome {
        val api = api() ?: return ActionOutcome.NotConfigured
        return when (val result = safeApiCall { api.clearHistory() }) {
            is ApiResult.Success -> ActionOutcome.Ok
            is ApiResult.Failure -> ActionOutcome.Failed(result.error.userMessage)
        }
    }

    suspend fun pauseAll(batchId: String): ActionOutcome = batch(batchId) { it.pauseBatch(batchId).jobs }

    suspend fun resumeAll(batchId: String): ActionOutcome = batch(batchId) { it.resumeBatch(batchId).jobs }

    suspend fun cancelAll(batchId: String): ActionOutcome = batch(batchId) { it.cancelBatch(batchId).jobs }

    suspend fun retryAll(batchId: String): ActionOutcome = batch(batchId) { it.retryBatch(batchId).jobs }

    /** [deleteFile] also removes the downloaded file (completed jobs only). */
    suspend fun delete(jobId: String, deleteFile: Boolean): ActionOutcome {
        val api = api() ?: return ActionOutcome.NotConfigured
        return when (val result = safeApiCall { api.deleteJob(jobId, if (deleteFile) true else null) }) {
            is ApiResult.Success -> ActionOutcome.Ok
            is ApiResult.Failure -> {
                val error = result.error
                if (error is ApiError.InvalidState) ActionOutcome.Changed(error.userMessage) else ActionOutcome.Failed(error.userMessage)
            }
        }
    }

    private suspend fun job(jobId: String, call: suspend (FetcharrApi) -> JobDto): ActionOutcome {
        val api = api() ?: return ActionOutcome.NotConfigured
        return when (val result = safeApiCall { call(api) }) {
            is ApiResult.Success -> {
                store.upsert(result.value)
                ActionOutcome.Ok
            }
            is ApiResult.Failure -> {
                val error = result.error
                if (error is ApiError.InvalidState) {
                    refreshJob(api, jobId)
                    ActionOutcome.Changed(error.userMessage)
                } else {
                    ActionOutcome.Failed(error.userMessage)
                }
            }
        }
    }

    private suspend fun batch(batchId: String, call: suspend (FetcharrApi) -> List<JobDto>): ActionOutcome {
        val api = api() ?: return ActionOutcome.NotConfigured
        return when (val result = safeApiCall { call(api) }) {
            is ApiResult.Success -> {
                result.value.forEach(store::upsert)
                ActionOutcome.Ok
            }
            is ApiResult.Failure -> {
                val error = result.error
                if (error is ApiError.InvalidState) {
                    refreshBatch(api, batchId)
                    ActionOutcome.Changed(error.userMessage)
                } else {
                    ActionOutcome.Failed(error.userMessage)
                }
            }
        }
    }

    /** Silent: a failure here is not worth a second message; the WebSocket will catch up. A 404 means the job is gone. */
    private suspend fun refreshJob(api: FetcharrApi, jobId: String) {
        when (val result = safeApiCall { api.getJob(jobId) }) {
            is ApiResult.Success -> store.upsert(result.value)
            is ApiResult.Failure ->
                if (result.error is ApiError.NotFound) store.apply(WsEvent.JobRemoved(listOf(jobId)))
        }
    }

    private suspend fun refreshBatch(api: FetcharrApi, batchId: String) {
        val result = safeApiCall { api.listJobs(batchId = batchId, limit = BATCH_REFRESH_LIMIT) }
        if (result is ApiResult.Success) result.value.jobs.forEach(store::upsert)
    }

    private fun api(): FetcharrApi? =
        try {
            apiProvider.current()
        } catch (e: IllegalArgumentException) {
            null // saved URL is not a valid http(s) URL
        }

    private companion object {
        const val BATCH_REFRESH_LIMIT = 500
    }
}
