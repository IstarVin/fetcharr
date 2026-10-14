package com.istarvin.fetcharr.incoming

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class IncomingCoordinatorSubmitLockTest {
    private fun ep(n: Int, show: String = "Show") = IncomingRequest.Single(
        "tv", "https://cdn.example/$n.mp4", "T$n", null, show, 1, n, emptyMap(), emptyList(),
    )

    private fun TestScope.coordinator() = IncomingCoordinator(this)

    private fun IncomingCoordinator.season() = state.value.sheet as IncomingSheet.SeasonSheet

    @Test
    fun episodesArrivingDuringSubmitAreQueuedNotMergedAndSurviveClose() = runTest {
        val c = coordinator()
        c.onRequest(ep(1))
        c.onRequest(ep(2))
        advanceTimeBy(2_000)
        runCurrent()
        val id = c.season().id
        assertFalse(c.state.value.receiving)

        val snapshot = c.beginSubmit(id) as IncomingSheet.SeasonSheet
        assertEquals(listOf(1, 2), snapshot.episodes.map { it.episode })

        c.onRequest(ep(3))
        c.onRequest(ep(4)) // absorbed by the queued sheet of the same season
        assertEquals("open sheet unchanged", listOf(1, 2), c.season().episodes.map { it.episode })
        assertEquals(1, c.state.value.waitingCount)
        assertFalse("no receiving window on the locked sheet", c.state.value.receiving)

        c.close() // submit succeeded
        val next = c.season()
        assertEquals(listOf(3, 4), next.episodes.map { it.episode })
        assertTrue(next.id != id)
        assertEquals(0, c.state.value.waitingCount)
        assertTrue("queued episodes open into a receiving window", c.state.value.receiving)
    }

    @Test
    fun selectionEditsAreIgnoredWhileLockedAndMergingResumesAfterEndSubmit() = runTest {
        val c = coordinator()
        c.onRequest(ep(1))
        c.onRequest(ep(2))
        advanceTimeBy(2_000)
        runCurrent()
        val id = c.season().id
        c.beginSubmit(id)
        c.setAllSelected(false)
        assertEquals(2, c.season().selectedCount)

        c.endSubmit(id) // submit failed
        c.setAllSelected(false)
        assertEquals(0, c.season().selectedCount)
        c.onRequest(ep(3))
        assertEquals(listOf(1, 2, 3), c.season().episodes.map { it.episode })
        assertEquals(0, c.state.value.waitingCount)
    }

    @Test
    fun beginSubmitRejectsAStaleSheetIdAndCloseReleasesTheLock() = runTest {
        val c = coordinator()
        assertNull(c.beginSubmit(1))
        c.onRequest(ep(1))
        val id = c.season().id
        assertNull(c.beginSubmit(id + 99))
        c.beginSubmit(id)
        c.close()
        c.onRequest(ep(2))
        advanceTimeBy(2_000)
        runCurrent()
        c.onRequest(ep(3)) // the new sheet is not locked
        assertEquals(listOf(2, 3), c.season().episodes.map { it.episode })
    }
}
