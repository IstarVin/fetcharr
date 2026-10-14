package dev.istarvin.fetcharr.notifications.logic

import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.network.dto.JobStatus

/**
 * A result notification to post on the `results` channel.
 *
 * @property key stable per notification: the job id, or "batch:<id>" for a summary. Re-posting with the same key
 *   replaces the earlier notification.
 * @property title first part, e.g. "Downloaded", "Failed" or "Severance S02".
 * @property text second part, e.g. "Dune Part Two (2024)" or "8 downloaded, 1 failed".
 * @property separator what joins title and text in the spec string: " · " for per-job results, " — " for summaries.
 */
data class ResultNotification(
    val key: String,
    val title: String,
    val text: String,
    val retryJobId: String? = null,
    val retryBatchId: String? = null,
    val separator: String = " · ",
) {
    /** The full spec string, e.g. "Downloaded · Dune Part Two (2024)" or "Severance S02 — 8 downloaded, 1 failed". */
    val combined: String get() = title + separator + text
}

/**
 * What the notifier remembers between events. Persist it (e.g. DataStore) so dedupe survives process death.
 * Prune [notifiedJobKeys] if it grows large; only recent keys matter.
 */
data class NotifierState(
    val summarizedBatchIds: Set<String> = emptySet(),
    val notifiedJobKeys: Set<String> = emptySet(),
) {
    fun hasNotified(job: JobDto): Boolean = jobKey(job) in notifiedJobKeys
    fun withJobNotified(job: JobDto): NotifierState = copy(notifiedJobKeys = notifiedJobKeys + jobKey(job))
    fun withBatchSummarized(batchId: String): NotifierState = copy(summarizedBatchIds = summarizedBatchIds + batchId)

    companion object {
        /** Job id plus finishedAt, so a retry that finishes again is a new event but a duplicate delivery is not. */
        fun jobKey(job: JobDto): String = "${job.id}@${job.finishedAt}"
    }
}

sealed interface ResultDecision {
    data class Skip(val reason: String) : ResultDecision
    data class PostJob(val notification: ResultNotification) : ResultDecision

    /** The caller fetches every job of [batchId] and calls [ResultNotificationDecider.summarize]. */
    data class SummarizeBatch(val batchId: String) : ResultDecision
}

object ResultNotificationDecider {
    /** Texts per §5.4. Unknown or missing codes map to [DEFAULT_ERROR_TEXT]. */
    val ERROR_TEXTS: Map<String, String> = mapOf(
        "low_disk_space" to "Not enough disk space on server",
        "already_exists" to "File already exists in library",
        "download_failed" to "Download failed",
        "processing_failed" to "Couldn't save file",
    )
    const val DEFAULT_ERROR_TEXT = "Download failed"

    fun errorText(code: String?): String = code?.let { ERROR_TEXTS[it] } ?: DEFAULT_ERROR_TEXT

    /**
     * Decides what to do when [job] finished (live WebSocket event or catch-up).
     *
     * Rules, in order:
     *  1. Not finished -> Skip.
     *  2. Already notified for this id + finishedAt (duplicate delivery) -> Skip.
     *  3. No batch: cancelled -> Skip; otherwise PostJob.
     *  4. Batch already summarized: cancelled -> Skip; otherwise PostJob (D9: a retried episode gets its own notification).
     *  5. Another job of the batch in [activeJobs] is pending, downloading, processing or PAUSED -> Skip("batch not finished").
     *     A paused sibling blocks the summary because the batch can still be resumed and finish.
     *  6. Otherwise SummarizeBatch. This includes a cancelled last job: the cancel itself is silent but the rest of the
     *     batch may still deserve a summary ([summarize] returns null when every episode was cancelled).
     *
     * The caller should, after acting: `state.withJobNotified(job)` for every non-duplicate event (also on Skip, so
     * a later duplicate cannot slip through), and `state.withBatchSummarized(batchId)` on every SummarizeBatch
     * (even if [summarize] returned null) so a later retry gets a per-job notification. See [record].
     * When replaying several catch-up jobs, thread the updated state through each call.
     */
    fun onJobFinished(job: JobDto, activeJobs: List<JobDto>, state: NotifierState): ResultDecision {
        if (!job.status.isFinished) return ResultDecision.Skip("job not finished")
        if (state.hasNotified(job)) return ResultDecision.Skip("duplicate event")
        val cancelled = job.status == JobStatus.CANCELLED
        val batchId = job.batchId
        if (batchId == null) {
            return if (cancelled) ResultDecision.Skip("cancelled") else ResultDecision.PostJob(jobNotification(job))
        }
        if (batchId in state.summarizedBatchIds) {
            return if (cancelled) ResultDecision.Skip("cancelled") else ResultDecision.PostJob(jobNotification(job))
        }
        val blocked = activeJobs.any { it.batchId == batchId && it.id != job.id && !it.status.isFinished }
        if (blocked) return ResultDecision.Skip("batch not finished")
        return ResultDecision.SummarizeBatch(batchId)
    }

    /** Convenience: applies the bookkeeping the caller must do after [onJobFinished] (see its doc). */
    fun record(state: NotifierState, job: JobDto, decision: ResultDecision): NotifierState {
        var s = state
        if (!(decision is ResultDecision.Skip && decision.reason == "duplicate event")) s = s.withJobNotified(job)
        if (decision is ResultDecision.SummarizeBatch) s = s.withBatchSummarized(decision.batchId)
        return s
    }

    /**
     * Builds the batch summary from every job of the batch. Counts only completed and failed; cancelled episodes are
     * ignored (D9). Zero parts are omitted ("8 downloaded", "1 failed"); order is downloaded, failed.
     * Returns null for an empty list, when any job is not finished, or when nothing completed or failed.
     */
    fun summarize(batchId: String, batchJobs: List<JobDto>): ResultNotification? {
        if (batchJobs.isEmpty() || batchJobs.any { !it.status.isFinished }) return null
        val downloaded = batchJobs.count { it.status == JobStatus.COMPLETED }
        val failed = batchJobs.count { it.status == JobStatus.FAILED }
        if (downloaded + failed == 0) return null
        val text = buildList {
            if (downloaded > 0) add("$downloaded downloaded")
            if (failed > 0) add("$failed failed")
        }.joinToString(", ")
        val named = batchJobs.firstOrNull { !it.showName.isNullOrBlank() } ?: batchJobs.first()
        return ResultNotification(
            key = "batch:$batchId",
            title = JobTitles.batchHeading(named.showName ?: named.title, named.season),
            text = text,
            retryBatchId = if (failed > 0) batchId else null,
            separator = " — ",
        )
    }

    private fun jobNotification(job: JobDto): ResultNotification {
        val heading = JobTitles.heading(job)
        return if (job.status == JobStatus.COMPLETED) {
            ResultNotification(key = job.id, title = "Downloaded", text = heading)
        } else {
            ResultNotification(
                key = job.id,
                title = "Failed",
                text = "$heading — ${errorText(job.errorCode)}",
                retryJobId = job.id,
            )
        }
    }
}
