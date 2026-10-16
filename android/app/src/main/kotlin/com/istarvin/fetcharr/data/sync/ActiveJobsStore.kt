package com.istarvin.fetcharr.data.sync

import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.data.network.dto.WsEvent
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The live list of active jobs (pending, downloading, processing, paused), fed by WebSocket events and by REST
 * refreshes after an action. Order is the server's; new jobs are appended. The UI sorts.
 */
@Singleton
class ActiveJobsStore @Inject constructor() {
    private val _jobs = MutableStateFlow<List<JobDto>>(emptyList())
    val jobs: StateFlow<List<JobDto>> = _jobs.asStateFlow()

    private val _hasSnapshot = MutableStateFlow(false)

    /** False until the first snapshot (or [clear]ed again), so the UI can tell "loading" from "empty". */
    val hasSnapshot: StateFlow<Boolean> = _hasSnapshot.asStateFlow()

    private val _snapshotFresh = MutableStateFlow(false)

    /**
     * True once a snapshot has arrived on the current connection: false from [onConnectionOpened] (or [clear]) until
     * the next snapshot. Unlike [hasSnapshot] it is not true just because an older connection had one, so a caller
     * that needs the list to be current (the catch-up) can wait for it.
     */
    val snapshotFresh: StateFlow<Boolean> = _snapshotFresh.asStateFlow()

    private val _finished = MutableSharedFlow<JobDto>(extraBufferCapacity = 64, onBufferOverflow = BufferOverflow.DROP_OLDEST)

    /**
     * A job reached completed/failed/cancelled. Emitted once per finish: a REST refresh and the matching
     * WebSocket event do not both emit.
     */
    val finished: SharedFlow<JobDto> = _finished.asSharedFlow()

    /** Tags queued finishes with their originating store contents, so a later server cannot consume them. */
    data class FinishedEvent(val job: JobDto, val generation: Long, val connectionIdentity: Any? = null)

    private val _finishedEvents = MutableSharedFlow<FinishedEvent>(
        extraBufferCapacity = 64, onBufferOverflow = BufferOverflow.DROP_OLDEST,
    )
    val finishedEvents: SharedFlow<FinishedEvent> = _finishedEvents.asSharedFlow()

    private val lock = Any()
    private var generation = 0L
    private val recentlyFinished = LinkedHashSet<String>()

    fun apply(event: WsEvent, connectionIdentity: Any? = null) {
        when (event) {
            is WsEvent.Snapshot -> synchronized(lock) {
                _jobs.value = event.jobs.filter { it.status.isActive }
                _hasSnapshot.value = true
                _snapshotFresh.value = true
                event.jobs.forEach { if (it.status.isActive) recentlyFinished.remove(it.id) }
            }
            is WsEvent.JobAdded -> upsert(event.job, connectionIdentity)
            is WsEvent.JobUpdated -> upsert(event.job, connectionIdentity)
            is WsEvent.JobRemoved -> synchronized(lock) {
                val ids = event.jobIds.toSet()
                _jobs.update { list -> list.filterNot { it.id in ids } }
            }
            is WsEvent.Unknown -> Unit
        }
    }

    /** Applies a job fetched over REST (or any updated job) with the same rules as `job_updated`. */
    fun upsert(job: JobDto, connectionIdentity: Any? = null) {
        val finishedEvent = synchronized(lock) {
            if (job.status.isActive) {
                recentlyFinished.remove(job.id)
                _jobs.update { list ->
                    val index = list.indexOfFirst { it.id == job.id }
                    if (index < 0) list + job else list.toMutableList().also { it[index] = job }
                }
                null
            } else {
                _jobs.update { list -> if (list.any { it.id == job.id }) list.filterNot { it.id == job.id } else list }
                if (markFinished(job.id)) FinishedEvent(job, generation, connectionIdentity) else null
            }
        }
        // Keep flow consumers outside the lock. A concurrent clear invalidates the captured generation even
        // when it happens between tagging and emission, and consumers can safely acquire their API lock.
        if (finishedEvent != null) {
            _finishedEvents.tryEmit(finishedEvent)
            _finished.tryEmit(job)
        }
    }

    fun currentGeneration(): Long = synchronized(lock) { generation }

    fun isCurrentGeneration(expected: Long): Boolean = synchronized(lock) { generation == expected }

    /** Call inside an API session guard to serialize posting with connection-driven clears. */
    fun runIfCurrentGeneration(expected: Long, effect: () -> Unit): Boolean = synchronized(lock) {
        if (generation != expected) return@synchronized false
        effect()
        true
    }

    /** The WebSocket opened: the server's snapshot is on its way and the list is stale until it arrives. */
    fun onConnectionOpened() = synchronized(lock) {
        _snapshotFresh.value = false
    }

    /** Forgets everything, e.g. when the server changes. */
    fun clear() = synchronized(lock) {
        generation++
        _jobs.value = emptyList()
        _hasSnapshot.value = false
        _snapshotFresh.value = false
        recentlyFinished.clear()
    }

    /** True the first time [id] is marked since it was last active. */
    private fun markFinished(id: String): Boolean {
        val added = recentlyFinished.add(id)
        if (recentlyFinished.size > MAX_REMEMBERED) recentlyFinished.remove(recentlyFinished.first())
        return added
    }

    private companion object {
        const val MAX_REMEMBERED = 200
    }
}

private val JobStatus.isActive: Boolean get() = !isFinished
