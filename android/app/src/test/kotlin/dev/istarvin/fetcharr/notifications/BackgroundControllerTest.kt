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
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class BackgroundControllerTest {
    private class FakeStarter : ServiceStarter {
        var accept = true
        var starts = 0
        var stops = 0
        override fun start(): Boolean {
            starts++
            return accept
        }

        override fun stop() {
            stops++
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
        assertEquals(0, env.starter.stops)
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
        assertEquals(0, env.starter.stops)
    }

    @Test
    fun noActiveJobsStopsQuietly() = runTest {
        val env = Env(this)
        env.controller.onSubmitSucceeded()
        runCurrent()
        env.snapshot(JobStatus.PAUSED) // only paused jobs
        runCurrent()
        assertEquals(1, env.starter.stops)
        assertTrue(env.notices.isEmpty())
    }

    @Test
    fun finishedSnapshotAfterSubmitStopsWithoutNotice() = runTest {
        val env = Env(this)
        env.snapshot(JobStatus.DOWNLOADING)
        runCurrent()
        env.snapshot() // all done
        runCurrent()
        assertEquals(1, env.starter.stops)
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
        assertEquals(0, env.starter.stops)
        assertTrue(env.notices.isEmpty())

        advanceTimeBy(minute + BackgroundController.TICK_MS)
        runCurrent()
        assertEquals(1, env.starter.stops)
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
        assertEquals(0, env.starter.stops)
        assertTrue(env.notices.isEmpty())
    }

    @Test
    fun systemTimeoutStopsWithNotice() = runTest {
        val env = Env(this)
        env.snapshot(JobStatus.DOWNLOADING)
        runCurrent()
        env.controller.onSystemTimeout()
        runCurrent()
        assertEquals(1, env.starter.stops)
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
}
