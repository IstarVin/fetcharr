package com.istarvin.fetcharr.data.history

import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus

enum class HistoryFilter { All, Completed, Failed }

/**
 * What History is limited to. The server applies it (`status`, and `q` when it advertises
 * `history_search_supported`); [accepts] applies the same rules to jobs that finish while the list is open.
 */
data class HistoryQuery(val filter: HistoryFilter = HistoryFilter.All, val text: String = "") {
    val statuses: String
        get() = when (filter) {
            HistoryFilter.All -> HistoryRepository.STATUSES
            HistoryFilter.Completed -> "completed"
            HistoryFilter.Failed -> "failed,cancelled"
        }

    val isFiltered: Boolean get() = filter != HistoryFilter.All || text.isNotBlank()

    fun accepts(job: JobDto): Boolean {
        val statusOk = when (filter) {
            HistoryFilter.All -> job.status.isFinished
            HistoryFilter.Completed -> job.status == JobStatus.COMPLETED
            HistoryFilter.Failed -> job.status == JobStatus.FAILED || job.status == JobStatus.CANCELLED
        }
        val needle = text.trim()
        return statusOk && (needle.isEmpty() || listOfNotNull(job.title, job.showName).any { it.contains(needle, ignoreCase = true) })
    }
}
