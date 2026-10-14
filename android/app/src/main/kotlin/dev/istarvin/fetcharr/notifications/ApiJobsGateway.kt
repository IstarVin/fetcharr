package dev.istarvin.fetcharr.notifications

import dev.istarvin.fetcharr.data.network.ApiResult
import dev.istarvin.fetcharr.data.network.FetcharrApi
import dev.istarvin.fetcharr.data.network.FetcharrApiProvider
import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.network.dto.Rfc3339
import dev.istarvin.fetcharr.data.network.safeApiCall
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class ApiJobsGateway @Inject constructor(
    private val apiProvider: FetcharrApiProvider,
) : JobsGateway {

    override suspend fun listFinishedAfter(afterMs: Long): List<JobDto>? {
        val api = api() ?: return null
        val after = Rfc3339.format(afterMs)
        val all = mutableListOf<JobDto>()
        var offset = 0
        repeat(MAX_PAGES) {
            val result = safeApiCall {
                api.listJobs(status = FINISHED_STATUSES, finishedAfter = after, limit = PAGE_SIZE, offset = offset)
            }
            val page = (result as? ApiResult.Success)?.value ?: return null
            all += page.jobs
            offset += page.jobs.size
            if (page.jobs.isEmpty() || offset >= page.total) return all
        }
        return all
    }

    override suspend fun listBatch(batchId: String): List<JobDto>? {
        val api = api() ?: return null
        val result = safeApiCall { api.listJobs(batchId = batchId, limit = BATCH_LIMIT) }
        return (result as? ApiResult.Success)?.value?.jobs
    }

    private fun api(): FetcharrApi? =
        try {
            apiProvider.current()
        } catch (e: IllegalArgumentException) {
            null // saved URL is not a valid http(s) URL
        }

    internal companion object {
        const val FINISHED_STATUSES = "completed,failed,cancelled"
        const val PAGE_SIZE = 200
        const val MAX_PAGES = 10
        const val BATCH_LIMIT = 500
    }
}
