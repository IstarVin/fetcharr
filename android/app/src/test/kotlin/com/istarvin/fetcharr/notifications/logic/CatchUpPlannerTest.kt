package com.istarvin.fetcharr.notifications.logic

import com.istarvin.fetcharr.data.network.dto.JobStatus.COMPLETED
import org.junit.Assert.assertEquals
import org.junit.Test

class CatchUpPlannerTest {
    private fun j(id: String, at: Long?) = job(id, COMPLETED, finishedAt = at)

    @Test fun ordersAscendingAndAdvancesLastSeen() {
        val plan = CatchUpPlanner.plan(100, listOf(j("c", 300), j("a", 150), j("b", 200)))
        assertEquals(listOf("a", "b", "c"), plan.jobsToProcess.map { it.id })
        assertEquals(300L, plan.newLastSeen)
    }

    @Test fun excludesAtOrBeforeLastSeen() {
        val plan = CatchUpPlanner.plan(200, listOf(j("old", 100), j("eq", 200), j("new", 201)))
        assertEquals(listOf("new"), plan.jobsToProcess.map { it.id })
        assertEquals(201L, plan.newLastSeen)
    }

    @Test fun nothingNewKeepsLastSeen() {
        val plan = CatchUpPlanner.plan(500, listOf(j("old", 100)))
        assertEquals(emptyList<String>(), plan.jobsToProcess.map { it.id })
        assertEquals(500L, plan.newLastSeen)
        assertEquals(500L, CatchUpPlanner.plan(500, emptyList()).newLastSeen)
    }

    @Test fun tiesAreOrderedById() {
        val plan = CatchUpPlanner.plan(0, listOf(j("b", 10), j("a", 10)))
        assertEquals(listOf("a", "b"), plan.jobsToProcess.map { it.id })
    }

    @Test fun missingFinishedAtIgnored() {
        assertEquals(emptyList<String>(), CatchUpPlanner.plan(0, listOf(j("x", null))).jobsToProcess.map { it.id })
    }

    @Test fun firstRunUsesNow() {
        assertEquals(12345L, CatchUpPlanner.initialLastSeen(null, 12345))
        assertEquals(10L, CatchUpPlanner.initialLastSeen(10, 12345))
    }
}
