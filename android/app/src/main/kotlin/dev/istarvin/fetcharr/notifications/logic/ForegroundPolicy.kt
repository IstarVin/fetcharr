package dev.istarvin.fetcharr.notifications.logic

import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.network.dto.JobStatus

sealed interface ForegroundEvent {
    /** The full list of server jobs that are not finished changed (WebSocket or sync). Any statuses are accepted. */
    data class ActiveJobsChanged(val jobs: List<JobDto>) : ForegroundEvent
    data class ConnectionChanged(val connected: Boolean, val atMs: Long) : ForegroundEvent
    data class Tick(val nowMs: Long) : ForegroundEvent

    /** `Service.onTimeout` (Android 15 dataSync limit). */
    data object SystemTimeout : ForegroundEvent
    data object SubmitSucceeded : ForegroundEvent

    /** The user opened the app: allows a restart after a notice-stop. */
    data object AppForegrounded : ForegroundEvent
}

sealed interface ServiceCommand {
    /** Start the foreground service. */
    data object Start : ServiceCommand

    /** Service is running and should stay up. */
    data object KeepRunning : ServiceCommand

    /** Stop the service; post "Stopped tracking downloads — open Fetcharr to resume" when [postStoppedNotice]. */
    data class Stop(val postStoppedNotice: Boolean) : ServiceCommand

    /** Not running and nothing to do (not in the spec's three commands; avoids a redundant Stop). */
    data object Idle : ServiceCommand
}

data class ForegroundState(
    val running: Boolean = false,
    val hasActiveJobs: Boolean = false,
    /** Set by SubmitSucceeded, cleared by the next ActiveJobsChanged. Keeps the service up before the job shows. */
    val awaitingFirstSync: Boolean = false,
    val disconnectedSinceMs: Long? = null,
    /** After a notice-stop, only SubmitSucceeded or AppForegrounded can start the service again. */
    val suppressed: Boolean = false,
)

object ForegroundPolicy {
    const val UNREACHABLE_LIMIT_MS: Long = 15L * 60 * 1000
    const val STOPPED_NOTICE: String = "Stopped tracking downloads — open Fetcharr to resume"

    /** pending, downloading, processing; paused does not count. */
    fun isActive(job: JobDto): Boolean =
        job.status == JobStatus.PENDING || job.status == JobStatus.DOWNLOADING || job.status == JobStatus.PROCESSING

    /** Pure and deterministic: all time comes from the events. */
    fun reduce(state: ForegroundState, event: ForegroundEvent): Pair<ForegroundState, ServiceCommand> {
        var s = state
        when (event) {
            is ForegroundEvent.ActiveJobsChanged ->
                s = s.copy(hasActiveJobs = event.jobs.any(::isActive), awaitingFirstSync = false)
            is ForegroundEvent.ConnectionChanged ->
                s = s.copy(disconnectedSinceMs = if (event.connected) null else s.disconnectedSinceMs ?: event.atMs)
            is ForegroundEvent.Tick -> {
                val since = s.disconnectedSinceMs
                if (s.running && since != null && event.nowMs - since >= UNREACHABLE_LIMIT_MS) return noticeStop(s)
            }
            ForegroundEvent.SystemTimeout -> if (s.running) return noticeStop(s)
            ForegroundEvent.SubmitSucceeded ->
                // A successful submit proves the server is reachable.
                s = s.copy(awaitingFirstSync = true, suppressed = false, disconnectedSinceMs = null)
            ForegroundEvent.AppForegrounded -> s = s.copy(suppressed = false)
        }
        val wanted = !s.suppressed && (s.hasActiveJobs || s.awaitingFirstSync)
        return when {
            wanted && !s.running -> s.copy(running = true) to ServiceCommand.Start
            wanted -> s to ServiceCommand.KeepRunning
            s.running -> s.copy(running = false, awaitingFirstSync = false) to ServiceCommand.Stop(false)
            else -> s to ServiceCommand.Idle
        }
    }

    private fun noticeStop(s: ForegroundState) =
        s.copy(running = false, awaitingFirstSync = false, suppressed = true) to ServiceCommand.Stop(true)
}
