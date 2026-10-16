package com.istarvin.fetcharr.notifications

import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.ServerConfigProvider
import com.istarvin.fetcharr.data.sync.ActiveJobsStore
import com.istarvin.fetcharr.data.sync.SyncManager
import com.istarvin.fetcharr.di.ApplicationScope
import com.istarvin.fetcharr.notifications.logic.CatchUpPlanner
import com.istarvin.fetcharr.notifications.logic.NotifierState
import com.istarvin.fetcharr.notifications.logic.ResultDecision
import com.istarvin.fetcharr.notifications.logic.ResultNotificationDecider
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeoutOrNull
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Posts "Downloaded", "Failed" and batch-summary notifications (app spec §6).
 *
 * Two sources feed the same path: jobs that finish while connected ([ActiveJobsStore.finished]) and a catch-up on
 * every (re)connect through `finished_after=<last seen>`. Both run under one [Mutex], so they never interleave. The
 * decisions come from [ResultNotificationDecider]; this class only does the I/O around them.
 *
 * State: [NotifierState] (summarized batches, notified job keys) is persisted in a [NotifierStateStore] and capped.
 * A retried episode after a process restart therefore still gets its own notification. *Last seen* lives in a
 * [LastSeenStore]; on first run it is set to "now" before the first catch-up.
 *
 * Only completed catch-up timestamp groups advance *last seen*. Live results therefore cannot skip over a failed
 * batch fetch, and the next reconnect retries it even after process death.
 */
@Singleton
class ResultNotifier internal constructor(
    private val store: ActiveJobsStore,
    private val connected: Flow<Unit>,
    private val gateway: JobsGateway,
    private val poster: ResultPoster,
    private val stateStore: NotifierStateStore,
    private val lastSeen: LastSeenStore,
    private val time: TimeSource,
    private val scope: CoroutineScope,
    private val serverKey: () -> String? = { null },
) {
    @Inject
    constructor(
        store: ActiveJobsStore,
        syncManager: SyncManager,
        gateway: JobsGateway,
        poster: ResultPoster,
        stateStore: NotifierStateStore,
        lastSeen: LastSeenStore,
        time: TimeSource,
        config: ServerConfigProvider,
        @ApplicationScope scope: CoroutineScope,
    ) : this(
        store, syncManager.connected, gateway, poster, stateStore, lastSeen, time, scope,
        { config.config.value?.baseUrl },
    )

    private val mutex = Mutex()
    private var started = false

    /** Only touched under [mutex]. */
    private var cachedState: NotifierState? = null

    /** Call once from `Application.onCreate`. */
    fun start() {
        if (started) return
        started = true
        // Subscribe first (UNDISPATCHED runs up to the subscription), then wait for the first-run setup inside the
        // collectors: a connection that opens or a job that finishes during the setup must not be dropped.
        scope.launch(start = CoroutineStart.UNDISPATCHED) {
            val ready = CompletableDeferred<Unit>()
            launch(start = CoroutineStart.UNDISPATCHED) {
                store.finishedEvents.collect { event ->
                    ready.await()
                    onLiveJob(event.job, event.generation, event.connectionIdentity)
                }
            }
            launch(start = CoroutineStart.UNDISPATCHED) {
                connected.collect {
                    ready.await()
                    catchUp()
                }
            }
            // First run: "last seen" is now, so old history never notifies. Must happen before any catch-up.
            lastSeen.initIfAbsent(time.nowMs())
            ready.complete(Unit)
        }
    }

    private inner class Pass(
        val session: JobsSession,
        val key: String?,
        val generation: Long,
        val currentKey: () -> String?,
    ) {
        fun isCurrent() = session.isCurrent() && currentKey() == key && store.isCurrentGeneration(generation)
        fun runIfCurrent(effect: () -> Unit): Boolean {
            if (!isCurrent()) return false
            var applied = false
            session.runIfCurrent {
                if (currentKey() == key) applied = store.runIfCurrentGeneration(generation, effect)
            }
            return applied
        }
    }

    private fun capturePass(
        generation: Long = store.currentGeneration(),
        connectionIdentity: Any? = null,
    ): Pass? {
        if (!store.isCurrentGeneration(generation)) return null
        val session = gateway.captureSession() ?: return null
        if (connectionIdentity != null && connectionIdentity !== session.connectionIdentity) return null
        val key = session.serverKey ?: serverKey()
        return Pass(session, key, generation) { session.serverKey ?: serverKey() }
    }

    internal suspend fun onLiveJob(
        job: JobDto,
        generation: Long = store.currentGeneration(),
        connectionIdentity: Any? = null,
    ) {
        val pass = capturePass(generation, connectionIdentity) ?: return
        mutex.withLock {
            if (!resetIfServerChanged(pass)) return@withLock
            process(job, pass)
            // Only a complete history pass proves there are no missing finishes before a timestamp. A later live
            // result must never jump over a failed batch fetch (including after a process restart).
        }
    }

    internal suspend fun catchUp() {
        val pass = capturePass() ?: return
        mutex.withLock {
            if (!resetIfServerChanged(pass)) return@withLock
            // A connection may change during any suspension, including waiting for its first snapshot.
            withTimeoutOrNull(SNAPSHOT_WAIT_MS) { store.snapshotFresh.first { it } } ?: return@withLock
            if (!pass.isCurrent()) return@withLock
            val last = lastSeen.get() ?: return@withLock
            if (!pass.isCurrent()) return@withLock
            val finished = pass.session.listFinishedAfter(last) ?: return@withLock
            if (!pass.isCurrent()) return@withLock
            val plan = CatchUpPlanner.plan(last, finished)
            // Commit only complete timestamp groups: an exclusive server filter must not skip an unprocessed tie.
            for ((finishedAt, jobs) in plan.jobsToProcess.groupBy { it.finishedAt!! }) {
                for (job in jobs) if (!process(job, pass)) return@withLock
                if (!pass.isCurrent()) return@withLock
                lastSeen.set(finishedAt)
                if (!pass.isCurrent()) return@withLock
                if (!saveState(loadState(), pass)) return@withLock
            }
        }
    }

    /** True when [job] is dealt with (posted, skipped or already known); false when a fetch failed and it must be retried. */
    private suspend fun process(job: JobDto, pass: Pass): Boolean {
        if (!pass.isCurrent()) return false
        val state = loadState()
        if (!pass.isCurrent()) return false
        when (val decision = ResultNotificationDecider.onJobFinished(job, store.jobs.value, state)) {
            is ResultDecision.Skip ->
                // Not recorded: when the last sibling finishes (or a catch-up runs) the batch can still be summarized.
                if (decision.reason != ResultNotificationDecider.SKIP_BATCH_NOT_FINISHED) {
                    saveState(ResultNotificationDecider.record(state, job, decision), pass)
                }
            is ResultDecision.PostJob -> {
                if (!pass.runIfCurrent { poster.post(decision.notification) }) return false
                saveState(ResultNotificationDecider.record(state, job, decision), pass)
            }
            is ResultDecision.SummarizeBatch -> {
                val batchJobs = pass.session.listBatch(decision.batchId) ?: return false
                if (!pass.isCurrent()) return false
                if (batchJobs.any { !it.status.isFinished }) {
                    // Raced with a retry or a late job: not done after all. Whoever finishes last summarizes.
                    return saveState(state.withJobNotified(job), pass)
                }
                val notification = ResultNotificationDecider.summarize(decision.batchId, batchJobs)
                if (!pass.runIfCurrent { notification?.let(poster::post) }) return false
                // Every job in the summary is covered by it: mark them so their own events (queued behind this one,
                // or replayed by a catch-up) do not post again.
                var next = ResultNotificationDecider.record(state, job, decision)
                for (member in batchJobs) next = next.withJobNotified(member)
                saveState(next, pass)
            }
        }
        return pass.isCurrent()
    }

    /**
     * Another server has other jobs and other times: forget the old state and start from now, or its whole history
     * would be announced. The same goes for the first connection to any server (the server was set up after the app
     * first ran, so the "last seen" from the first run says nothing about it).
     */
    private suspend fun resetIfServerChanged(pass: Pass): Boolean {
        if (!pass.isCurrent()) return false
        val current = pass.key ?: return true
        val stored = stateStore.loadServerKey()
        if (!pass.isCurrent()) return false
        if (stored == current) return true
        // Save the owner last. If settings change during persistence, the next serialized pass must reset again.
        cachedState = null
        stateStore.save(NotifierState())
        if (!pass.isCurrent()) return false
        lastSeen.set(time.nowMs())
        if (!pass.isCurrent()) return false
        stateStore.saveServerKey(current)
        return pass.isCurrent()
    }

    private suspend fun loadState(): NotifierState =
        cachedState ?: stateStore.load().also { cachedState = it }

    private suspend fun saveState(state: NotifierState, pass: Pass): Boolean {
        if (!pass.isCurrent()) return false
        val cursor = lastSeen.get()
        if (!pass.isCurrent()) return false
        val capped = state.capped(cursor)
        if (capped == cachedState) return true
        stateStore.save(capped)
        cachedState = if (pass.isCurrent()) capped else null
        return pass.isCurrent()
    }

    private fun NotifierState.capped(cursor: Long?): NotifierState {
        // Live results have not yet moved the recovery cursor. Keep their keys until a complete history pass
        // covers them, even above the normal cap, or reconnecting after many live finishes would post them again.
        val pending = notifiedJobKeys.filterTo(LinkedHashSet()) {
            cursor == null || (it.substringAfterLast('@').toLongOrNull() ?: Long.MIN_VALUE) > cursor
        }
        val checkpointed = (notifiedJobKeys - pending).lastN((MAX_JOB_KEYS - pending.size).coerceAtLeast(0))
        return copy(
            summarizedBatchIds = summarizedBatchIds.lastN(MAX_BATCHES),
            notifiedJobKeys = notifiedJobKeys.filterTo(LinkedHashSet()) { it in pending || it in checkpointed },
        )
    }

    private fun Set<String>.lastN(n: Int): Set<String> =
        if (size <= n) this else LinkedHashSet(toList().takeLast(n))

    internal companion object {
        const val MAX_JOB_KEYS = 500
        const val MAX_BATCHES = 200
        const val SNAPSHOT_WAIT_MS = 10_000L
    }
}
