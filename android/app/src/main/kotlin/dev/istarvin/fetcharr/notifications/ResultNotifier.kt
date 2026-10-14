package dev.istarvin.fetcharr.notifications

import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.network.ServerConfigProvider
import dev.istarvin.fetcharr.data.sync.ActiveJobsStore
import dev.istarvin.fetcharr.data.sync.SyncManager
import dev.istarvin.fetcharr.di.ApplicationScope
import dev.istarvin.fetcharr.notifications.logic.CatchUpPlanner
import dev.istarvin.fetcharr.notifications.logic.NotifierState
import dev.istarvin.fetcharr.notifications.logic.ResultDecision
import dev.istarvin.fetcharr.notifications.logic.ResultNotificationDecider
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
 * When a batch summary needs a fetch that fails, nothing is recorded and *last seen* does not advance past that job,
 * so the next reconnect retries it.
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
                store.finished.collect { job ->
                    ready.await()
                    onLiveJob(job)
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

    internal suspend fun onLiveJob(job: JobDto) = mutex.withLock {
        resetIfServerChanged()
        if (process(job)) advanceLastSeen(job.finishedAt)
    }

    internal suspend fun catchUp() = mutex.withLock {
        resetIfServerChanged()
        // The active list must be from this connection, or a batch could be judged by stale siblings: ones that
        // finished while we were away still look active, new ones are missing.
        withTimeoutOrNull(SNAPSHOT_WAIT_MS) { store.snapshotFresh.first { it } } ?: return@withLock
        val last = lastSeen.get() ?: return@withLock
        val finished = gateway.listFinishedAfter(last) ?: return@withLock
        val plan = CatchUpPlanner.plan(last, finished)
        var processedUpTo = last
        for (job in plan.jobsToProcess) {
            if (!process(job)) break
            processedUpTo = maxOf(processedUpTo, job.finishedAt ?: processedUpTo)
        }
        if (processedUpTo > last) lastSeen.set(processedUpTo)
    }

    /** True when [job] is dealt with (posted, skipped or already known); false when a fetch failed and it must be retried. */
    private suspend fun process(job: JobDto): Boolean {
        val state = loadState()
        when (val decision = ResultNotificationDecider.onJobFinished(job, store.jobs.value, state)) {
            is ResultDecision.Skip ->
                // Not recorded: when the last sibling finishes (or a catch-up runs) the batch can still be summarized.
                if (decision.reason != ResultNotificationDecider.SKIP_BATCH_NOT_FINISHED) {
                    saveState(ResultNotificationDecider.record(state, job, decision))
                }
            is ResultDecision.PostJob -> {
                poster.post(decision.notification)
                saveState(ResultNotificationDecider.record(state, job, decision))
            }
            is ResultDecision.SummarizeBatch -> {
                val batchJobs = gateway.listBatch(decision.batchId) ?: return false
                if (batchJobs.any { !it.status.isFinished }) {
                    // Raced with a retry or a late job: not done after all. Whoever finishes last summarizes.
                    saveState(state.withJobNotified(job))
                    return true
                }
                ResultNotificationDecider.summarize(decision.batchId, batchJobs)?.let(poster::post)
                // Every job in the summary is covered by it: mark them so their own events (queued behind this one,
                // or replayed by a catch-up) do not post again.
                var next = ResultNotificationDecider.record(state, job, decision)
                for (member in batchJobs) next = next.withJobNotified(member)
                saveState(next)
            }
        }
        return true
    }

    /**
     * Another server has other jobs and other times: forget the old state and start from now, or its whole history
     * would be announced. The same goes for the first connection to any server (the server was set up after the app
     * first ran, so the "last seen" from the first run says nothing about it).
     */
    private suspend fun resetIfServerChanged() {
        val current = serverKey() ?: return
        val stored = stateStore.loadServerKey()
        if (stored == current) return
        cachedState = NotifierState()
        stateStore.save(NotifierState())
        lastSeen.set(time.nowMs())
        stateStore.saveServerKey(current)
    }

    private suspend fun advanceLastSeen(finishedAt: Long?) {
        if (finishedAt == null) return
        val current = lastSeen.get() ?: return
        if (finishedAt > current) lastSeen.set(finishedAt)
    }

    private suspend fun loadState(): NotifierState =
        cachedState ?: stateStore.load().also { cachedState = it }

    private suspend fun saveState(state: NotifierState) {
        val capped = state.capped()
        if (capped == cachedState) return
        cachedState = capped
        stateStore.save(capped)
    }

    private fun NotifierState.capped(): NotifierState = copy(
        summarizedBatchIds = summarizedBatchIds.lastN(MAX_BATCHES),
        notifiedJobKeys = notifiedJobKeys.lastN(MAX_JOB_KEYS),
    )

    private fun Set<String>.lastN(n: Int): Set<String> =
        if (size <= n) this else LinkedHashSet(toList().takeLast(n))

    internal companion object {
        const val MAX_JOB_KEYS = 500
        const val MAX_BATCHES = 200
        const val SNAPSHOT_WAIT_MS = 10_000L
    }
}
