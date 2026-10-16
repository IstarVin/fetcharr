package com.istarvin.fetcharr.notifications

import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.data.network.dto.WsEvent
import com.istarvin.fetcharr.data.sync.ActiveJobsStore
import com.istarvin.fetcharr.notifications.logic.NotifierState
import com.istarvin.fetcharr.notifications.logic.ResultNotification
import com.istarvin.fetcharr.notifications.logic.ep
import com.istarvin.fetcharr.notifications.logic.job
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class ResultNotifierTest {
    private class FakeGateway : JobsGateway {
        /** Everything the "server" has finished. Filtered by `finished_after` like the real API. */
        var finished: List<JobDto> = emptyList()
        var ignoreFilter = false
        val batches = mutableMapOf<String, List<JobDto>>()
        var failBatch = false
        var generation = 0
        var connectionIdentity: Any = Any()
        var historyGate: CompletableDeferred<Unit>? = null
        var batchGate: CompletableDeferred<Unit>? = null

        override fun captureSession(): JobsSession {
            val captured = generation
            val identity = connectionIdentity
            return object : JobsSession {
                override val connectionIdentity = identity
                override val serverKey: String? = null
                override fun isCurrent() = captured == generation && identity === this@FakeGateway.connectionIdentity
                override suspend fun listFinishedAfter(afterMs: Long) = this@FakeGateway.listFinishedAfter(afterMs)
                override suspend fun listBatch(batchId: String) = this@FakeGateway.listBatch(batchId)
            }
        }
        val finishedAfterCalls = mutableListOf<Long>()
        val batchCalls = mutableListOf<String>()

        override suspend fun listFinishedAfter(afterMs: Long): List<JobDto>? {
            finishedAfterCalls += afterMs
            historyGate?.await()
            return finished.filter { ignoreFilter || (it.finishedAt ?: 0) > afterMs }
        }

        override suspend fun listBatch(batchId: String): List<JobDto>? {
            batchCalls += batchId
            batchGate?.await()
            return if (failBatch) null else batches[batchId]
        }
    }

    private class MemoryStateStore(var state: NotifierState = NotifierState()) : NotifierStateStore {
        var serverKey: String? = null

        override suspend fun loadServerKey() = serverKey

        override suspend fun saveServerKey(key: String) {
            serverKey = key
        }

        override suspend fun load() = state

        override suspend fun save(state: NotifierState) {
            this.state = state
        }
    }

    private class MemoryLastSeen(var value: Long? = null) : LastSeenStore {
        var initDelayMs = 0L

        override suspend fun get() = value

        override suspend fun set(epochMs: Long) {
            value = epochMs
        }

        override suspend fun initIfAbsent(nowMs: Long) {
            delay(initDelayMs)
            if (value == null) value = nowMs
        }
    }

    private class Env(scope: TestScope, lastSeenValue: Long? = null, state: NotifierState = NotifierState()) {
        val store = ActiveJobsStore().also { it.apply(WsEvent.Snapshot(emptyList())) }
        val connected = MutableSharedFlow<Unit>(extraBufferCapacity = 1)
        val gateway = FakeGateway()
        val posted = mutableListOf<ResultNotification>()
        val stateStore = MemoryStateStore(state)
        val lastSeen = MemoryLastSeen(lastSeenValue)
        var now = 5_000L
        var server: String? = null
        val notifier = ResultNotifier(
            store, connected, gateway, { posted += it }, stateStore, lastSeen, { now }, scope.backgroundScope,
            { server },
        )

        suspend fun TestScope.startAndSettle() {
            notifier.start()
            runCurrent()
        }

        suspend fun TestScope.reconnect() {
            connected.emit(Unit)
            runCurrent()
        }

        val combined get() = posted.map { it.combined }
    }

    @Test
    fun firstRunSetsLastSeenToNowAndPostsNothingForOlderJobs() = runTest {
        val env = Env(this)
        env.gateway.finished = listOf(job("old", JobStatus.COMPLETED, title = "Old", finishedAt = 1_000L))
        with(env) {
            startAndSettle()
            assertEquals(5_000L, lastSeen.value)
            reconnect()
        }
        assertEquals(listOf(5_000L), env.gateway.finishedAfterCalls)
        assertTrue(env.posted.isEmpty())
        assertEquals(5_000L, env.lastSeen.value)
    }

    @Test
    fun existingLastSeenIsNotOverwrittenAtStartup() = runTest {
        val env = Env(this, lastSeenValue = 2_000L)
        with(env) { startAndSettle() }
        assertEquals(2_000L, env.lastSeen.value)
    }

    @Test
    fun liveCompletedAndFailedJobsPostOneEach() = runTest {
        val env = Env(this, lastSeenValue = 100L)
        with(env) { startAndSettle() }
        env.store.upsert(job("a", JobStatus.COMPLETED, title = "Dune Part Two", year = 2024, finishedAt = 200L))
        env.store.upsert(
            job("b", JobStatus.FAILED, title = "Dune Part Two", year = 2024, errorCode = "download_failed", finishedAt = 300L),
        )
        runCurrent()
        assertEquals(
            listOf("Downloaded · Dune Part Two (2024)", "Failed · Dune Part Two (2024) — Download failed"),
            env.combined,
        )
        assertEquals("b", env.posted[1].retryJobId)
        assertEquals(100L, env.lastSeen.value)
    }

    @Test
    fun aUserCancelPostsNothingAndLeavesTheRecoveryCursorSafe() = runTest {
        val env = Env(this, lastSeenValue = 100L)
        with(env) { startAndSettle() }
        env.store.upsert(job("c", JobStatus.CANCELLED, title = "X", finishedAt = 400L))
        runCurrent()
        assertTrue(env.posted.isEmpty())
        assertEquals(100L, env.lastSeen.value)
    }

    @Test
    fun aFinishedBatchFetchesTheBatchAndPostsOneSummary() = runTest {
        val env = Env(this, lastSeenValue = 100L)
        val e1 = ep("e1", JobStatus.DOWNLOADING, n = 1)
        val e2 = ep("e2", JobStatus.DOWNLOADING, n = 2)
        env.store.apply(WsEvent.Snapshot(listOf(e1, e2)))
        with(env) { startAndSettle() }

        val done1 = ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 200L)
        env.store.upsert(done1)
        runCurrent()
        assertTrue(env.posted.isEmpty()) // e2 still running
        assertTrue(env.gateway.batchCalls.isEmpty())

        val failed2 = ep("e2", JobStatus.FAILED, n = 2, finishedAt = 300L, errorCode = "download_failed")
        env.gateway.batches["b1"] = listOf(done1, failed2)
        env.store.upsert(failed2)
        runCurrent()

        assertEquals(listOf("b1"), env.gateway.batchCalls)
        assertEquals(listOf("Severance S02 — 1 downloaded, 1 failed"), env.combined)
        assertEquals("b1", env.posted.single().retryBatchId)
        assertEquals(100L, env.lastSeen.value)
    }

    @Test
    fun aBatchWhereEveryEpisodeWasCancelledPostsNothing() = runTest {
        val env = Env(this, lastSeenValue = 100L)
        val e1 = ep("e1", JobStatus.CANCELLED, n = 1, finishedAt = 200L)
        env.gateway.batches["b1"] = listOf(e1)
        with(env) { startAndSettle() }
        env.store.upsert(e1)
        runCurrent()
        assertEquals(listOf("b1"), env.gateway.batchCalls)
        assertTrue(env.posted.isEmpty())
        assertTrue("b1" in env.stateStore.state.summarizedBatchIds)
    }

    @Test
    fun episodesQueuedBehindTheSummaryDoNotPostAgain() = runTest {
        // Two last episodes finish at the same moment: the first event summarizes, the second is covered by it.
        val env = Env(this, lastSeenValue = 100L)
        val e1 = ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 200L)
        val e2 = ep("e2", JobStatus.COMPLETED, n = 2, finishedAt = 201L)
        env.gateway.batches["b1"] = listOf(e1, e2)
        with(env) { startAndSettle() }
        env.store.upsert(e1)
        env.store.upsert(e2)
        runCurrent()
        assertEquals(listOf("Severance S02 — 2 downloaded"), env.combined)
    }

    @Test
    fun aRetriedEpisodeAfterTheSummaryGetsItsOwnNotification() = runTest {
        val env = Env(this, lastSeenValue = 100L)
        val e1 = ep("e1", JobStatus.FAILED, n = 1, finishedAt = 200L, errorCode = "download_failed")
        env.gateway.batches["b1"] = listOf(e1)
        with(env) { startAndSettle() }
        env.store.upsert(e1)
        runCurrent()
        assertEquals(1, env.posted.size)

        // Retried and finished again, later.
        env.store.upsert(ep("e1", JobStatus.PENDING, n = 1))
        env.store.upsert(ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 900L))
        runCurrent()
        assertEquals(listOf("Severance S02 — 1 failed", "Downloaded · Severance — S02E01"), env.combined)
    }

    @Test
    fun summarizedBatchesSurviveARestart() = runTest {
        val first = Env(this, lastSeenValue = 100L)
        val e1 = ep("e1", JobStatus.FAILED, n = 1, finishedAt = 200L, errorCode = "download_failed")
        first.gateway.batches["b1"] = listOf(e1)
        with(first) { startAndSettle() }
        first.store.upsert(e1)
        runCurrent()

        // New process: fresh notifier, same persisted state.
        val second = Env(this, lastSeenValue = first.lastSeen.value, state = first.stateStore.state)
        with(second) { startAndSettle() }
        second.store.upsert(ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 900L))
        runCurrent()
        assertEquals(listOf("Downloaded · Severance — S02E01"), second.combined)
        assertTrue(second.gateway.batchCalls.isEmpty())
    }

    @Test
    fun catchUpRequestsFromLastSeenPostsAndAdvances() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        env.gateway.finished = listOf(
            job("late", JobStatus.FAILED, title = "B", errorCode = "low_disk_space", finishedAt = 3_000L),
            job("early", JobStatus.COMPLETED, title = "A", finishedAt = 2_000L),
            job("gone", JobStatus.CANCELLED, title = "C", finishedAt = 2_500L),
            job("before", JobStatus.COMPLETED, title = "Z", finishedAt = 500L),
        )
        with(env) {
            startAndSettle()
            reconnect()
        }
        assertEquals(listOf(1_000L), env.gateway.finishedAfterCalls)
        assertEquals(listOf("Downloaded · A", "Failed · B — Not enough disk space on server"), env.combined)
        assertEquals(3_000L, env.lastSeen.value)

        // A second reconnect finds nothing new.
        with(env) { reconnect() }
        assertEquals(listOf(1_000L, 3_000L), env.gateway.finishedAfterCalls)
        assertEquals(2, env.posted.size)
    }

    @Test
    fun aJobSeenLiveAndAgainByCatchUpPostsOnce() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        val done = job("a", JobStatus.COMPLETED, title = "A", finishedAt = 2_000L)
        env.gateway.finished = listOf(done)
        env.gateway.ignoreFilter = true
        with(env) { startAndSettle() }
        env.store.upsert(done) // live
        runCurrent()
        with(env) { reconnect() } // the server still lists it (e.g. last seen was behind)
        env.lastSeen.value = 1_000L
        with(env) { reconnect() }
        assertEquals(listOf("Downloaded · A"), env.combined)
    }

    @Test
    fun aJobSeenByCatchUpAndThenLivePostsOnce() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        val done = job("a", JobStatus.COMPLETED, title = "A", finishedAt = 2_000L)
        env.gateway.finished = listOf(done)
        with(env) {
            startAndSettle()
            reconnect()
        }
        env.store.upsert(done)
        runCurrent()
        assertEquals(listOf("Downloaded · A"), env.combined)
    }

    @Test
    fun aMissedBatchIsSummarizedOnceByCatchUp() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        val e1 = ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 2_000L)
        val e2 = ep("e2", JobStatus.COMPLETED, n = 2, finishedAt = 2_100L)
        val e3 = ep("e3", JobStatus.FAILED, n = 3, finishedAt = 2_200L, errorCode = "download_failed")
        env.gateway.finished = listOf(e1, e2, e3)
        env.gateway.batches["b1"] = listOf(e1, e2, e3)
        with(env) {
            startAndSettle()
            reconnect()
        }
        assertEquals(listOf("Severance S02 — 2 downloaded, 1 failed"), env.combined)
        assertEquals(2_200L, env.lastSeen.value)
    }

    @Test
    fun aFailedBatchFetchIsRetriedOnTheNextReconnect() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        val e1 = ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 2_000L)
        env.gateway.finished = listOf(e1)
        env.gateway.batches["b1"] = listOf(e1)
        env.gateway.failBatch = true
        with(env) {
            startAndSettle()
            reconnect()
        }
        assertTrue(env.posted.isEmpty())
        assertEquals(1_000L, env.lastSeen.value)

        env.gateway.failBatch = false
        with(env) { reconnect() }
        assertEquals(listOf("Severance S02 — 1 downloaded"), env.combined)
        assertEquals(2_000L, env.lastSeen.value)
    }

    @Test
    fun failedBatchThenLaterLiveResultIsRetriedAfterProcessRestartIncludingTimestampTies() = runTest {
        val first = Env(this, lastSeenValue = 1_000L)
        val movie = job("a", JobStatus.COMPLETED, title = "Movie", finishedAt = 2_000L)
        val episode = ep("z", JobStatus.COMPLETED, n = 1, finishedAt = 2_000L)
        first.gateway.finished = listOf(movie, episode)
        first.gateway.failBatch = true
        with(first) { startAndSettle(); reconnect() }
        first.store.upsert(job("live", JobStatus.COMPLETED, title = "Later", finishedAt = 3_000L))
        runCurrent()
        assertEquals(1_000L, first.lastSeen.value)

        val second = Env(this, first.lastSeen.value, first.stateStore.state)
        second.gateway.finished = listOf(movie, episode)
        second.gateway.batches["b1"] = listOf(episode)
        with(second) { startAndSettle(); reconnect() }
        assertEquals(listOf("Severance S02 — 1 downloaded"), second.combined)
        assertEquals(2_000L, second.lastSeen.value)
    }

    @Test
    fun bufferedOldServerFinishIsDiscardedAfterClearWhileEarlierBatchFetchWasSuspended() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        env.server = "server-a"
        env.stateStore.serverKey = "server-a"
        val gate = CompletableDeferred<Unit>()
        val episode = ep("old-episode", JobStatus.COMPLETED, n = 1, finishedAt = 2_000L)
        env.gateway.batchGate = gate
        env.gateway.batches["b1"] = listOf(episode)
        with(env) { startAndSettle() }
        env.store.upsert(episode)
        runCurrent() // the collector is waiting for A's batch response
        assertEquals(listOf("b1"), env.gateway.batchCalls)
        env.store.upsert(job("old-buffered", JobStatus.COMPLETED, title = "Old", finishedAt = 2_100L))

        env.server = "server-b"
        env.gateway.generation++
        env.store.clear()
        gate.complete(Unit)
        runCurrent()
        assertTrue(env.posted.isEmpty())
        assertTrue(env.stateStore.state.notifiedJobKeys.isEmpty())

        env.store.apply(WsEvent.Snapshot(emptyList()))
        env.store.upsert(job("new", JobStatus.COMPLETED, title = "New", finishedAt = 6_000L))
        runCurrent()
        assertEquals(listOf("Downloaded · New"), env.combined)
        assertEquals(setOf("new@6000"), env.stateStore.state.notifiedJobKeys)
        assertEquals("server-b", env.stateStore.serverKey)
    }

    @Test
    fun bufferedFinishesFromOldConnectionAreDiscardedBeforeAnAsynchronousStoreClear() = runTest {
        // Cover a URL change and a token-only connection replacement; neither needs clear to have run yet.
        for (changeServer in listOf(true, false)) {
            val env = Env(this, lastSeenValue = 1_000L)
            env.server = "server-a"
            env.stateStore.serverKey = "server-a"
            val oldConnection = env.gateway.connectionIdentity
            val gate = CompletableDeferred<Unit>()
            val episode = ep("old-episode", JobStatus.COMPLETED, n = 1, finishedAt = 2_000L)
            env.gateway.batchGate = gate
            env.gateway.batches["b1"] = listOf(episode)
            with(env) { startAndSettle() }
            env.store.upsert(episode, oldConnection)
            runCurrent()
            assertEquals(listOf("b1"), env.gateway.batchCalls)
            env.store.apply(
                WsEvent.JobUpdated(job("old-buffered", JobStatus.COMPLETED, finishedAt = 2_100L)),
                oldConnection,
            )

            if (changeServer) env.server = "server-b"
            env.gateway.connectionIdentity = Any() // config collector has emitted a new API connection
            gate.complete(Unit) // SyncManager's asynchronous clear deliberately has not happened
            runCurrent()
            assertTrue(env.posted.isEmpty())
            assertTrue(env.stateStore.state.notifiedJobKeys.isEmpty())
            assertEquals(1_000L, env.lastSeen.value)
            assertEquals("server-a", env.stateStore.serverKey)
            env.store.clear()
        }
    }

    @Test
    fun changingConnectionWhileHistoryIsSuspendedDiscardsItsJobs() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        val gate = CompletableDeferred<Unit>()
        env.gateway.historyGate = gate
        env.gateway.finished = listOf(job("a", JobStatus.COMPLETED, finishedAt = 2_000L))
        with(env) { startAndSettle(); reconnect() }
        env.gateway.generation++
        gate.complete(Unit)
        runCurrent()
        assertTrue(env.posted.isEmpty())
        assertTrue(env.stateStore.state.notifiedJobKeys.isEmpty())
        assertEquals(1_000L, env.lastSeen.value)
    }

    @Test
    fun changingConnectionWhileBatchIsSuspendedDiscardsItsSummary() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        val gate = CompletableDeferred<Unit>()
        val episode = ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 2_000L)
        env.gateway.batchGate = gate
        env.gateway.finished = listOf(episode)
        env.gateway.batches["b1"] = listOf(episode)
        with(env) { startAndSettle(); reconnect() }
        assertEquals(listOf("b1"), env.gateway.batchCalls)
        env.gateway.generation++
        gate.complete(Unit)
        runCurrent()
        assertTrue(env.posted.isEmpty())
        assertTrue(env.stateStore.state.summarizedBatchIds.isEmpty())
        assertEquals(1_000L, env.lastSeen.value)
    }

    @Test
    fun changingConnectionWhileWaitingForSnapshotDoesNotFetchHistory() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        env.store.clear()
        with(env) { startAndSettle(); reconnect() }
        env.gateway.generation++
        env.store.apply(WsEvent.Snapshot(emptyList()))
        runCurrent()
        assertTrue(env.gateway.finishedAfterCalls.isEmpty())
        assertEquals(1_000L, env.lastSeen.value)
    }

    @Test
    fun catchUpWaitsForTheSnapshotSoRunningSiblingsBlockTheSummary() = runTest {
        val env = Env(this, lastSeenValue = 1_000L)
        env.store.clear() // no snapshot yet
        val e1 = ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 2_000L)
        env.gateway.finished = listOf(e1)
        env.gateway.batches["b1"] = listOf(e1, ep("e2", JobStatus.DOWNLOADING, n = 2))
        with(env) {
            startAndSettle()
            reconnect()
        }
        assertTrue(env.gateway.finishedAfterCalls.isEmpty())
        // The snapshot arrives and shows e2 running: e1 is skipped, no summary.
        env.store.apply(WsEvent.Snapshot(listOf(ep("e2", JobStatus.DOWNLOADING, n = 2))))
        runCurrent()
        with(env) { reconnect() }
        assertTrue(env.posted.isEmpty())
        assertNull(env.gateway.batchCalls.firstOrNull())
    }

    @Test
    fun catchUpAfterAReconnectWaitsForTheNewSnapshotNotTheOldOne() = runTest {
        // Both episodes were running when the connection dropped. While away they finished. The store still holds
        // the old list; judged by it, each would look blocked by the other and the summary would be lost.
        val env = Env(this, lastSeenValue = 1_000L)
        env.store.apply(
            WsEvent.Snapshot(listOf(ep("e1", JobStatus.DOWNLOADING, n = 1), ep("e2", JobStatus.DOWNLOADING, n = 2))),
        )
        val e1 = ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 2_000L)
        val e2 = ep("e2", JobStatus.COMPLETED, n = 2, finishedAt = 2_100L)
        env.gateway.finished = listOf(e1, e2)
        env.gateway.batches["b1"] = listOf(e1, e2)
        with(env) { startAndSettle() }

        env.store.onConnectionOpened() // what SyncManager does just before it announces the connection
        with(env) { reconnect() }
        assertTrue(env.gateway.finishedAfterCalls.isEmpty()) // still waiting for the new snapshot
        env.store.apply(WsEvent.Snapshot(emptyList()))
        runCurrent()

        assertEquals(listOf("Severance S02 — 2 downloaded"), env.combined)
    }

    @Test
    fun aSkippedBatchMemberIsNotRecordedSoALaterSummaryStillHappens() = runTest {
        val env = Env(this, lastSeenValue = 100L)
        val running = ep("e2", JobStatus.DOWNLOADING, n = 2)
        env.store.apply(WsEvent.Snapshot(listOf(running)))
        val e1 = ep("e1", JobStatus.COMPLETED, n = 1, finishedAt = 200L)
        with(env) { startAndSettle() }
        env.store.upsert(e1)
        runCurrent()
        assertTrue(env.posted.isEmpty())
        assertTrue(env.stateStore.state.notifiedJobKeys.isEmpty())

        val e2 = ep("e2", JobStatus.COMPLETED, n = 2, finishedAt = 300L)
        env.gateway.batches["b1"] = listOf(e1, e2)
        env.store.upsert(e2)
        runCurrent()
        assertEquals(listOf("Severance S02 — 2 downloaded"), env.combined)
    }

    @Test
    fun aConnectionThatOpensDuringFirstRunSetupIsNotDropped() = runTest {
        val env = Env(this)
        env.lastSeen.initDelayMs = 50
        env.gateway.finished = listOf(job("a", JobStatus.COMPLETED, title = "A", finishedAt = 6_000L))
        env.notifier.start()
        runCurrent()
        testScheduler.advanceTimeBy(10)
        env.connected.emit(Unit)
        runCurrent()
        assertTrue(env.posted.isEmpty()) // still setting up
        testScheduler.advanceTimeBy(100)
        runCurrent()
        assertEquals(5_000L, env.gateway.finishedAfterCalls.single()) // last seen was set to "now" first
        assertEquals(listOf("Downloaded · A"), env.combined)
    }

    @Test
    fun aJobFinishingDuringFirstRunSetupIsNotDropped() = runTest {
        val env = Env(this)
        env.lastSeen.initDelayMs = 50
        env.notifier.start()
        runCurrent()
        env.store.upsert(job("a", JobStatus.COMPLETED, title = "A", finishedAt = 6_000L))
        testScheduler.advanceTimeBy(100)
        runCurrent()
        assertEquals(listOf("Downloaded · A"), env.combined)
    }

    @Test
    fun changingTheServerForgetsStateAndStartsFromNow() = runTest {
        val env = Env(this, lastSeenValue = 1_000L, state = NotifierState(notifiedJobKeys = setOf("x@1")))
        env.server = "http://old:8080"
        env.gateway.finished = listOf(job("old", JobStatus.COMPLETED, title = "Old", finishedAt = 2_000L))
        with(env) {
            startAndSettle()
            reconnect()
        }
        // First connection to this server: starts from now, nothing from its history is announced.
        assertTrue(env.posted.isEmpty())
        assertEquals(5_000L, env.lastSeen.value)
        assertEquals(listOf(5_000L), env.gateway.finishedAfterCalls)

        env.now = 9_000L
        env.server = "http://old:8080"
        with(env) { reconnect() } // same server again: untouched
        assertEquals(5_000L, env.lastSeen.value)

        env.server = "http://new:8080"
        env.gateway.finished = listOf(job("n", JobStatus.COMPLETED, title = "New", finishedAt = 9_500L))
        with(env) { reconnect() }
        assertEquals(9_000L, env.gateway.finishedAfterCalls.last())
        assertEquals(listOf("Downloaded · New"), env.combined)
        assertTrue("x@1" !in env.stateStore.state.notifiedJobKeys)
        assertEquals("http://new:8080", env.stateStore.serverKey)
    }

    @Test
    fun liveResultsBeyondTheNormalDedupCapStayCoveredUntilRecoveryCheckpointsThem() = runTest {
        val env = Env(this, lastSeenValue = 100L)
        val jobs = (1..501).map { job("live-$it", JobStatus.COMPLETED, finishedAt = 200L) }
        with(env) { startAndSettle() }
        jobs.forEach { env.notifier.onLiveJob(it) }
        assertEquals(501, env.stateStore.state.notifiedJobKeys.size)
        env.gateway.finished = jobs
        with(env) { reconnect() }
        assertEquals(501, env.posted.size)
        assertEquals(200L, env.lastSeen.value)
        assertEquals(ResultNotifier.MAX_JOB_KEYS, env.stateStore.state.notifiedJobKeys.size)
    }

    @Test
    fun theStoredStateIsCapped() = runTest {
        val old = (1..600).map { "j$it@1" }.toCollection(LinkedHashSet())
        val env = Env(this, lastSeenValue = 100L, state = NotifierState(notifiedJobKeys = old))
        with(env) { startAndSettle() }
        env.store.upsert(job("new", JobStatus.COMPLETED, title = "N", finishedAt = 200L))
        runCurrent()
        val keys = env.stateStore.state.notifiedJobKeys
        assertEquals(ResultNotifier.MAX_JOB_KEYS, keys.size)
        assertTrue("new@200" in keys)
        assertTrue("j1@1" !in keys)
    }
}
