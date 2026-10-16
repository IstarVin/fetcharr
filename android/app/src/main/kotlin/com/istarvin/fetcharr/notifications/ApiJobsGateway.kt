package com.istarvin.fetcharr.notifications

import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.FetcharrApiSession
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.Rfc3339
import com.istarvin.fetcharr.data.network.safeApiCall
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class ApiJobsGateway @Inject constructor(
    private val apiProvider: FetcharrApiProvider,
) : JobsGateway {
    override fun captureSession(): JobsSession? = try {
        apiProvider.captureSession()?.let { BoundSession(it) }
    } catch (_: IllegalArgumentException) {
        null // saved URL is not a valid http(s) URL
    }

    override suspend fun listFinishedAfter(afterMs: Long): List<JobDto>? =
        captureSession()?.listFinishedAfter(afterMs)

    override suspend fun listBatch(batchId: String): List<JobDto>? = captureSession()?.listBatch(batchId)

    private inner class BoundSession(private val session: FetcharrApiSession) : JobsSession {
        override val connectionIdentity: Any = session
        override val serverKey = session.config.baseUrl
        override fun isCurrent() = apiProvider.isCurrent(session)
        override fun runIfCurrent(effect: () -> Unit): Boolean =
            apiProvider.withCurrentSession(session) { effect(); true } ?: false

        override suspend fun listFinishedAfter(afterMs: Long): List<JobDto>? =
            listAll(after = Rfc3339.format(afterMs), limit = PAGE_SIZE)

        override suspend fun listBatch(batchId: String): List<JobDto>? =
            listAll(batchId = batchId, limit = BATCH_LIMIT)

        // The server orders by creation time, not finish time. Every page must be read before the notifier
        // may move its finish-time cursor; a page cap or a partial response can hide older-created jobs.
        private suspend fun listAll(after: String? = null, batchId: String? = null, limit: Int): List<JobDto>? {
            val all = mutableListOf<JobDto>()
            val ids = mutableSetOf<String>()
            var expectedTotal: Int? = null
            while (true) {
                if (!isCurrent()) return null
                val result = safeApiCall {
                    session.api.listJobs(
                        status = if (after != null) FINISHED_STATUSES else null,
                        finishedAfter = after, batchId = batchId, limit = limit, offset = all.size,
                    )
                }
                if (!isCurrent()) return null
                val page = (result as? ApiResult.Success)?.value ?: return null
                if (expectedTotal == null) expectedTotal = page.total
                // A changing page set cannot safely establish a complete cursor boundary. Retry on reconnect.
                if (page.total != expectedTotal || page.total < 0 ||
                    all.size + page.jobs.size > page.total || page.jobs.any { !ids.add(it.id) }) return null
                all += page.jobs
                if (all.size == page.total) return all
                if (page.jobs.isEmpty()) return null
            }
        }
    }

    internal companion object {
        const val FINISHED_STATUSES = "completed,failed,cancelled"
        const val PAGE_SIZE = 200
        const val BATCH_LIMIT = 500
    }
}
