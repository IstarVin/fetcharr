package com.istarvin.fetcharr.notifications.logic

import com.istarvin.fetcharr.data.network.dto.JobStatus.DOWNLOADING
import com.istarvin.fetcharr.data.network.dto.JobStatus.PAUSED
import com.istarvin.fetcharr.data.network.dto.JobStatus.PENDING
import com.istarvin.fetcharr.data.network.dto.JobStatus.PROCESSING
import com.istarvin.fetcharr.notifications.logic.ForegroundEvent.ActiveJobsChanged
import com.istarvin.fetcharr.notifications.logic.ForegroundEvent.ConnectionChanged
import com.istarvin.fetcharr.notifications.logic.ForegroundEvent.Tick
import org.junit.Assert.assertEquals
import org.junit.Test

class ForegroundPolicyTest {
    private val min = 60_000L
    private val limit = 15 * min

    private class Driver {
        var state = ForegroundState()
        fun send(e: ForegroundEvent): ServiceCommand {
            val (s, c) = ForegroundPolicy.reduce(state, e)
            state = s
            return c
        }
    }

    private fun active(vararg s: com.istarvin.fetcharr.data.network.dto.JobStatus) =
        ActiveJobsChanged(s.mapIndexed { i, st -> job("j$i", st, finishedAt = null) })

    @Test fun startsWhenSyncFindsActiveJobs() {
        val d = Driver()
        assertEquals(ServiceCommand.Start, d.send(active(PENDING)))
        assertEquals(ServiceCommand.KeepRunning, d.send(active(DOWNLOADING, PAUSED)))
    }

    @Test fun eachActiveStatusCounts() {
        for (s in listOf(PENDING, DOWNLOADING, PROCESSING)) {
            assertEquals(ServiceCommand.Start, Driver().send(active(s)))
        }
    }

    @Test fun pausedOnlyDoesNotStart() {
        assertEquals(ServiceCommand.Idle, Driver().send(active(PAUSED)))
    }

    @Test fun pausedOnlyStopsWithoutNotice() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        assertEquals(ServiceCommand.Stop(false), d.send(active(PAUSED)))
        assertEquals(ServiceCommand.Idle, d.send(Tick(1)))
    }

    @Test fun stopsAsSoonAsNothingActive() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        assertEquals(ServiceCommand.Stop(false), d.send(ActiveJobsChanged(emptyList())))
    }

    @Test fun submitStartsBeforeJobAppears() {
        val d = Driver()
        assertEquals(ServiceCommand.Start, d.send(ForegroundEvent.SubmitSucceeded(0)))
        assertEquals(ServiceCommand.KeepRunning, d.send(Tick(1000)))
        assertEquals(ServiceCommand.KeepRunning, d.send(active(PENDING)))
    }

    @Test fun submitGraceSurvivesASyncWithNothingActiveUntilTimeout() {
        val d = Driver()
        d.send(ForegroundEvent.SubmitSucceeded(1_000))
        assertEquals(ServiceCommand.KeepRunning, d.send(ActiveJobsChanged(emptyList())))
        assertEquals(ServiceCommand.KeepRunning, d.send(Tick(1_000 + ForegroundPolicy.SUBMIT_GRACE_MS - 1)))
        assertEquals(ServiceCommand.Stop(false), d.send(Tick(1_000 + ForegroundPolicy.SUBMIT_GRACE_MS)))
    }

    @Test fun submitGraceSurvivesAnUnrelatedPausedSync() {
        val d = Driver()
        d.send(ForegroundEvent.SubmitSucceeded(0))
        assertEquals(ServiceCommand.KeepRunning, d.send(active(PAUSED)))
    }

    @Test fun anActiveJobEndsTheGraceSoItsFinishStopsTheService() {
        val d = Driver()
        d.send(ForegroundEvent.SubmitSucceeded(0))
        d.send(active(PENDING))
        assertEquals(ServiceCommand.Stop(false), d.send(ActiveJobsChanged(emptyList())))
    }

    @Test fun foregroundingAfterANoticeStopStartsAFreshUnreachableClock() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        d.send(ConnectionChanged(false, 0))
        assertEquals(ServiceCommand.Stop(true), d.send(Tick(limit)))
        assertEquals(ServiceCommand.Start, d.send(ForegroundEvent.AppForegrounded))
        // Not a second notice on the first tick of the restarted service.
        assertEquals(ServiceCommand.KeepRunning, d.send(Tick(limit + 30_000)))
    }

    @Test fun noOneWantingAConnectionStopsTheUnreachableClock() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        d.send(ConnectionChanged(false, 0))
        d.send(ForegroundEvent.ConnectionNotWanted)
        assertEquals(ServiceCommand.KeepRunning, d.send(Tick(limit + 1)))
    }

    @Test fun unreachableBoundary() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        d.send(ConnectionChanged(false, 1_000))
        assertEquals(ServiceCommand.KeepRunning, d.send(Tick(1_000 + limit - 1)))
        assertEquals(ServiceCommand.Stop(true), d.send(Tick(1_000 + limit)))
    }

    @Test fun reconnectResetsTimer() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        d.send(ConnectionChanged(false, 0))
        assertEquals(ServiceCommand.KeepRunning, d.send(Tick(14 * min + 59_000)))
        d.send(ConnectionChanged(true, 14 * min + 59_500))
        d.send(ConnectionChanged(false, 15 * min))
        assertEquals(ServiceCommand.KeepRunning, d.send(Tick(15 * min + 14 * min + 59_000)))
        assertEquals(ServiceCommand.Stop(true), d.send(Tick(15 * min + limit)))
    }

    @Test fun repeatedDisconnectEventsKeepEarliestTime() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        d.send(ConnectionChanged(false, 0))
        d.send(ConnectionChanged(false, 10 * min))
        assertEquals(ServiceCommand.Stop(true), d.send(Tick(limit)))
    }

    @Test fun systemTimeoutStopsWithNotice() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        assertEquals(ServiceCommand.Stop(true), d.send(ForegroundEvent.SystemTimeout))
    }

    @Test fun systemTimeoutWhileNotRunningIsIdle() {
        assertEquals(ServiceCommand.Idle, Driver().send(ForegroundEvent.SystemTimeout))
    }

    @Test fun noticeStopIsNotUndoneByNextSyncButByForegroundOrSubmit() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        d.send(ForegroundEvent.SystemTimeout)
        assertEquals(ServiceCommand.Idle, d.send(active(DOWNLOADING)))
        assertEquals(ServiceCommand.Start, d.send(ForegroundEvent.AppForegrounded))
        val e = Driver()
        e.send(active(DOWNLOADING))
        e.send(ForegroundEvent.SystemTimeout)
        assertEquals(ServiceCommand.Start, e.send(ForegroundEvent.SubmitSucceeded(0)))
    }

    @Test fun submitAfterUnreachableStopRestartsWithFreshTimer() {
        val d = Driver()
        d.send(active(DOWNLOADING))
        d.send(ConnectionChanged(false, 0))
        d.send(Tick(limit))
        assertEquals(ServiceCommand.Start, d.send(ForegroundEvent.SubmitSucceeded(0)))
        assertEquals(ServiceCommand.KeepRunning, d.send(Tick(limit + 1)))
    }

    @Test fun deterministic() {
        val events = listOf(active(DOWNLOADING), ConnectionChanged(false, 5), Tick(5 + limit))
        fun run() = events.fold(ForegroundState() to ServiceCommand.Idle as ServiceCommand) { (s, _), e -> ForegroundPolicy.reduce(s, e) }
        assertEquals(run(), run())
    }
}
