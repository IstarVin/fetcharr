package com.istarvin.fetcharr.notifications.logic

import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus

/** Content of the foreground-service notification. [progressPercent] null means indeterminate. */
data class OngoingContent(val text: String, val progressPercent: Int?)

object OngoingNotificationText {
    const val NOTHING_ACTIVE = "Waiting for downloads"

    fun content(jobs: List<JobDto>): OngoingContent = OngoingContent(text(jobs), progressPercent(jobs))

    /**
     * Parts joined with " · ", each omitted when zero: "Downloading N" (downloading), "Finishing N" (processing),
     * "N queued" (pending). Examples: "Downloading 2 · 3 queued", "Downloading 2", "3 queued", "Finishing 1".
     * Paused and finished jobs are ignored. With nothing to show: [NOTHING_ACTIVE].
     */
    fun text(jobs: List<JobDto>): String {
        val downloading = jobs.count { it.status == JobStatus.DOWNLOADING }
        val processing = jobs.count { it.status == JobStatus.PROCESSING }
        val queued = jobs.count { it.status == JobStatus.PENDING }
        val parts = buildList {
            if (downloading > 0) add("Downloading $downloading")
            if (processing > 0) add("Finishing $processing")
            if (queued > 0) add("$queued queued")
        }
        return if (parts.isEmpty()) NOTHING_ACTIVE else parts.joinToString(" · ")
    }

    /**
     * 0..100 from sum(downloaded) / sum(total) over downloading jobs whose downloaded and total bytes are both known
     * (total > 0). Null (indeterminate) when no downloading job qualifies.
     */
    fun progressPercent(jobs: List<JobDto>): Int? {
        var downloaded = 0L
        var total = 0L
        for (job in jobs) {
            if (job.status != JobStatus.DOWNLOADING) continue
            val p = job.progress ?: continue
            val d = p.downloadedBytes ?: continue
            val t = p.totalBytes ?: continue
            if (t <= 0) continue
            downloaded += d.coerceIn(0, t)
            total += t
        }
        if (total <= 0) return null
        return (downloaded * 100 / total).toInt().coerceIn(0, 100)
    }
}
