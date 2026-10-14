package dev.istarvin.fetcharr.notifications

import dev.istarvin.fetcharr.data.network.dto.JobStatus
import dev.istarvin.fetcharr.data.network.dto.WsEvent
import dev.istarvin.fetcharr.data.sync.ActiveJobsStore
import dev.istarvin.fetcharr.data.sync.ConnectionState
import dev.istarvin.fetcharr.notifications.logic.ForegroundPolicy
import dev.istarvin.fetcharr.notifications.logic.job
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class BackgroundControllerTest {
    private class FakeStarter : ServiceStarter {
        var accept = true
        var starts = 0
        override fun start(): Boolean {
            starts++
            return accept
        }
    }

    private class Env(scope: TestScope) {
        val store = ActiveJobsStore()
        val connection = MutableStateFlow<ConnectionState>(ConnectionState.Disconnected(null))
        val starter = FakeStarter()
        val notices = mutableListOf<String>()
        val controller = BackgroundController(
            store, connection, starter, { notices += it }, { scope.currentTime }, scope.backgroundScope,
        ).also { it.start() }

        val shouldRun get() = controller.shouldRun.value

        fun snapshot(vararg statuses: JobStatus) {
            store.apply(WsEvent.Snapshot(statuses.mapIndexed { i, s -> job("j$i", s) }))
        }
    }

    private val minute = 60_000L

    @Test
    fun submitStartsTheService() = runTest {
        val env = Env(this)
        env.controller.onSubmitSucceeded()
        runCurrent()
        assertEquals(1, env.starter.starts)
        assertTrue(env.shouldRun)
    }

    @Test
    fun aSyncThatFindsActiveJobsStartsTheService() = runTest {
        val env = Env(this)
        runCurrent()
        assertEquals(0, env.starter.starts) // no snapshot yet: nothing is known
        env.snapshot(JobStatus.DOWNLOADING, JobStatus.PENDING)
        runCurrent()
        assertEquals(1, env.starter.starts)
        env.snapshot(JobStatus.DOWNLOADING) // still active: no second start
        runCurrent()
        assertEquals(1, env.starter.starts)
        assertTrue(env.shouldRun)
    }

    @Test
    fun noActiveJobsStopsQuietly() = runTest {
        val env = Env(this)
        env.controller.onSubmitSucceeded()
        runCurrent()
        env.snapshot(JobStatus.PAUSED) // only paused jobs: the submit grace still holds
        runCurrent()
        assertTrue(env.shouldRun)
        advanceTimeBy(ForegroundPolicy.SUBMIT_GRACE_MS + BackgroundController.TICK_MS)
        runCurrent()
        assertFalse(env.shouldRun)
        assertTrue(env.notices.isEmpty())
    }

    @Test
    fun finishedSnapshotAfterSubmitStopsWithoutNotice() = runTest {
        val env = Env(this)
        env.snapshot(JobStatus.DOWNLOADING)
        runCurrent()
        env.snapshot() // all done
        runCurrent()
        assertFalse(env.shouldRun)
        assertTrue(env.notices.isEmpty())
    }

    @Test
    fun unreachableForFifteenMinutesStopsWithNotice() = runTest {
        val env = Env(this)
        env.snapshot(JobStatus.DOWNLOADING)
        env.connection.value = ConnectionState.Connected
        runCurrent()
        assertEquals(1, env.starter.starts)

        env.connection.value = ConnectionState.Disconnected(5)
        runCurrent()
        advanceTimeBy(14 * minute)
        assertTrue(env.shouldRun)
        assertTrue(env.notices.isEmpty())

        advanceTimeBy(minute + BackgroundController.TICK_MS)
        runCurrent()
        assertFalse(env.shouldRun)
        assertEquals(listOf(ForegroundPolicy.STOPPED_NOTICE), env.notices)

        // Tracking stays off for further job updates until the user comes back.
        env.snapshot(JobStatus.DOWNLOADING, JobStatus.PENDING)
        runCurrent()
        assertEquals(1, env.starter.starts)
        env.controller.onAppForegrounded()
        runCurrent()
        assertEquals(2, env.starter.starts)
    }

    @Test
    fun reconnectingResetsTheUnreachableTimer() = runTest {
        val env = Env(this)
        env.snapshot(JobStatus.DOWNLOADING)
        env.connection.value = ConnectionState.Disconnected(3)
        runCurrent()
        advanceTimeBy(10 * minute)
        env.connection.value = ConnectionState.Connected
        runCurrent()
        env.connection.value = ConnectionState.Disconnected(3)
        runCurrent()
        advanceTimeBy(10 * minute)
        runCurrent()
        assertTrue(env.shouldRun)
        assertTrue(env.notices.isEmpty())
    }

    @Test
    fun systemTimeoutStopsWithNotice() = runTest {
        val env = Env(this)
        env.snapshot(JobStatus.DOWNLOADING)
        runCurrent()
        env.controller.onSystemTimeout()
        runCurrent()
        assertFalse(env.shouldRun)
        assertEquals(listOf(ForegroundPolicy.STOPPED_NOTICE), env.notices)
    }

    @Test
    fun aSubmitRestartsAfterANoticeStop() = runTest {
        val env = Env(this)
        env.snapshot(JobStatus.DOWNLOADING)
        runCurrent()
        env.controller.onSystemTimeout()
        runCurrent()
        env.controller.onSubmitSucceeded()
        runCurrent()
        assertEquals(2, env.starter.starts)
    }

    @Test
    fun aRefusedStartIsRetriedOnTheNextEvent() = runTest {
        val env = Env(this)
        env.starter.accept = false
        runCurrent() // let the initial connection event through first
        env.controller.onSubmitSucceeded()
        runCurrent()
        assertEquals(1, env.starter.starts)
        env.starter.accept = true
        env.snapshot(JobStatus.DOWNLOADING)
        runCurrent()
        assertEquals(2, env.starter.starts)
    }

    @Test
    fun submitSucceededIsAlsoAnnouncedForThePermissionPrompt() = runTest {
        val env = Env(this)
        var count = 0
        val collector = backgroundScope.let {
            it.launch { env.controller.submitSucceeded.collect { count++ } }
        }
        runCurrent()
        env.controller.onSubmitSucceeded()
        runCurrent()
        assertEquals(1, count)
        collector.cancel()
    }

    @Test
    fun aJobThatFailsRightAfterItIsAddedStartsTheServiceOnceAndNeverStopsItDirectly() = runTest {
        // job_added (pending) and job_updated (failed) milliseconds apart: Start and Stop arrive back to back.
        val env = Env(this)
        env.snapshot()
        runCurrent()
        env.store.upsert(job("x", JobStatus.PENDING, finishedAt = null))
        runCurrent()
        env.store.upsert(job("x", JobStatus.FAILED, errorCode = "download_failed"))
        runCurrent()
        assertEquals(1, env.starter.starts) // the controller has no way to stop the service: it only flips shouldRun
        assertFalse(env.shouldRun) // the service (already foreground by then) stops itself on this
        assertTrue(env.notices.isEmpty())
    }

    @Test
    fun shouldRunFollowsTheServiceLifecycle() = runTest {
        val env = Env(this)
        assertFalse(env.shouldRun)
        env.snapshot(JobStatus.DOWNLOADING)
        runCurrent()
        assertTrue(env.shouldRun)
        env.controller.onSystemTimeout()
        runCurrent()
        assertFalse(env.shouldRun)
        env.controller.onSubmitSucceeded()
        runCurrent()
        assertTrue(env.shouldRun)
    }

    @Test
    fun aRestartAfterTheUnreachableStopDoesNotPostASecondNotice() = runTest {
        val env = Env(this)
        env.snapshot(JobStatus.DOWNLOADING)
        env.connection.value = ConnectionState.Disconnected(5)
        runCurrent()
        advanceTimeBy(15 * minute + BackgroundController.TICK_MS)
        runCurrent()
        assertEquals(1, env.notices.size)

        // The service released its connection, the user opens the app, the connection is still down.
        env.connection.value = ConnectionState.Disconnected(null)
        env.controller.onAppForegrounded()
        env.connection.value = ConnectionState.Connecting
        runCurrent()
        assertEquals(2, env.starter.starts)
        advanceTimeBy(minute)
        runCurrent()
        assertEquals(1, env.notices.size)
        assertTrue(env.shouldRun)
    }

    @Test
    fun anEmptySyncRightAfterASubmitDoesNotStopTheService() = runTest {
        val env = Env(this)
        env.snapshot(JobStatus.PAUSED) // e.g. the connection is just up and the new job is not in yet
        runCurrent()
        env.controller.onSubmitSucceeded()
        runCurrent()
        env.snapshot(JobStatus.PAUSED)
        runCurrent()
        assertTrue(env.shouldRun)
        env.snapshot(JobStatus.DOWNLOADING)
        runCurrent()
        assertTrue(env.shouldRun)
    }
}
