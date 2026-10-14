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
    data class SubmitSucceeded(val atMs: Long) : ForegroundEvent

    /** Nobody wants the WebSocket (no holder, or no server). Says nothing about reachability, so the clock stops. */
    data object ConnectionNotWanted : ForegroundEvent

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
    /**
     * Set by SubmitSucceeded; cleared by an ActiveJobsChanged that has an active job, or [ForegroundPolicy.SUBMIT_GRACE_MS]
     * after the submit. Keeps the service up before the new job shows.
     */
    val awaitingFirstSync: Boolean = false,
    val awaitingSinceMs: Long = 0,
    val disconnectedSinceMs: Long? = null,
    /** After a notice-stop, only SubmitSucceeded or AppForegrounded can start the service again. */
    val suppressed: Boolean = false,
)

object ForegroundPolicy {
    const val UNREACHABLE_LIMIT_MS: Long = 15L * 60 * 1000

    /** How long a successful submit keeps the service up while no active job has shown up yet. */
    const val SUBMIT_GRACE_MS: Long = 30_000
    const val STOPPED_NOTICE: String = "Stopped tracking downloads — open Fetcharr to resume"

    /** pending, downloading, processing; paused does not count. */
    fun isActive(job: JobDto): Boolean =
        job.status == JobStatus.PENDING || job.status == JobStatus.DOWNLOADING || job.status == JobStatus.PROCESSING

    /** Pure and deterministic: all time comes from the events. */
    fun reduce(state: ForegroundState, event: ForegroundEvent): Pair<ForegroundState, ServiceCommand> {
        var s = state
        when (event) {
            is ForegroundEvent.ActiveJobsChanged -> {
                val any = event.jobs.any(::isActive)
                // A sync without an active job (the list from before the submit, or the job already failed) does
                // not end the grace; the timeout does.
                s = s.copy(hasActiveJobs = any, awaitingFirstSync = s.awaitingFirstSync && !any)
            }
            is ForegroundEvent.ConnectionChanged ->
                s = s.copy(disconnectedSinceMs = if (event.connected) null else s.disconnectedSinceMs ?: event.atMs)
            ForegroundEvent.ConnectionNotWanted -> s = s.copy(disconnectedSinceMs = null)
            is ForegroundEvent.Tick -> {
                if (s.awaitingFirstSync && event.nowMs - s.awaitingSinceMs >= SUBMIT_GRACE_MS) {
                    s = s.copy(awaitingFirstSync = false)
                }
                val since = s.disconnectedSinceMs
                if (s.running && since != null && event.nowMs - since >= UNREACHABLE_LIMIT_MS) return noticeStop(s)
            }
            ForegroundEvent.SystemTimeout -> if (s.running) return noticeStop(s)
            is ForegroundEvent.SubmitSucceeded ->
                // A successful submit proves the server is reachable.
                s = s.copy(awaitingFirstSync = true, awaitingSinceMs = event.atMs, suppressed = false, disconnectedSinceMs = null)
            // The user is back: a fresh start also gets a fresh unreachable clock.
            ForegroundEvent.AppForegrounded -> s = s.copy(suppressed = false, disconnectedSinceMs = null)
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
        s.copy(running = false, awaitingFirstSync = false, suppressed = true, disconnectedSinceMs = null) to
            ServiceCommand.Stop(true)
}
