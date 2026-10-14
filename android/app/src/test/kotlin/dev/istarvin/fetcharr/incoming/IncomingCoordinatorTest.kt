package dev.istarvin.fetcharr.incoming

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class IncomingCoordinatorTest {

    private fun ep(show: String, season: Int, episode: Int, title: String? = "T$episode", headers: Map<String, String> = emptyMap(), year: Int? = null) =
        IncomingRequest.Single(
            categoryHint = "tv", url = "https://cdn.example/$episode.mp4", title = title, year = year,
            showName = show, season = season, episode = episode, headers = headers,
            subtitles = listOf(IncomingSubtitle("https://cdn.example/$episode.vtt", "en")),
        )

    private fun movie(title: String = "Movie") =
        IncomingRequest.Single(null, "https://cdn.example/m.mp4", title, 2026, null, null, null, emptyMap(), emptyList())

    private fun batch(show: String, season: Int, vararg episodes: Int, headers: Map<String, String> = emptyMap()) =
        IncomingRequest.Batch(
            "tv", show, season, null, headers,
            episodes.map { BatchEpisode(it, "B$it", "https://cdn.example/b$it.mp4", emptyMap(), emptyList()) },
        )

    private fun TestScope.coordinator() = IncomingCoordinator(this)

    private fun IncomingCoordinator.season() = state.value.sheet as IncomingSheet.SeasonSheet

    @Test
    fun noSheetOpensImmediately() = runTest {
        val c = coordinator()
        assertNull(c.state.value.sheet)
        val m = movie()
        c.onRequest(m)
        assertEquals(m, (c.state.value.sheet as IncomingSheet.SingleSheet).request)
        assertEquals(0, c.state.value.waitingCount)
        assertFalse(c.state.value.receiving)
    }

    @Test
    fun tenSinglesAcrossCaseVariantsMergeIntoOneSeasonSheet() = runTest {
        val c = coordinator()
        for (i in 1..10) {
            val name = if (i % 2 == 0) "  EXAMPLE show " else "Example Show"
            c.onRequest(ep(name, 1, i, headers = mapOf("X-Ep" to "$i")))
            assertTrue("receiving after arrival $i", c.state.value.receiving)
            if (i < 10) advanceTimeBy(100)
        }
        assertEquals(900L, currentTime)
        val sheet = c.season()
        assertEquals("Example Show", sheet.showName)
        assertEquals(1, sheet.season)
        assertEquals((1..10).toList(), sheet.episodes.map { it.episode })
        assertTrue(sheet.episodes.all { it.selected })
        assertEquals(10, sheet.selectedCount)
        assertEquals("5", sheet.episodes[4].headers["X-Ep"])
        assertEquals(0, c.state.value.waitingCount)

        // Last arrival at t=900; window ends exactly 2000 ms later.
        advanceTimeBy(1999)
        runCurrent()
        assertEquals(2899L, currentTime)
        assertTrue(c.state.value.receiving)
        advanceTimeBy(1)
        runCurrent()
        assertEquals(2900L, currentTime)
        assertFalse(c.state.value.receiving)
    }

    @Test
    fun eachArrivalRestartsTheWindow() = runTest {
        val c = coordinator()
        c.onRequest(ep("S", 1, 1))
        advanceTimeBy(1500)
        c.onRequest(ep("S", 1, 2))
        advanceTimeBy(1999) // 1999 ms after the second arrival
        runCurrent()
        assertTrue(c.state.value.receiving)
        advanceTimeBy(1)
        runCurrent()
        assertFalse(c.state.value.receiving)
    }

    @Test
    fun duplicateEpisodeNumberReplacesAndReselects() = runTest {
        val c = coordinator()
        c.onRequest(ep("S", 1, 1, title = "Old", headers = mapOf("k" to "old")))
        c.onRequest(ep("S", 1, 2))
        c.toggleEpisode(1)
        assertFalse(c.season().episodes[0].selected)
        c.onRequest(ep("s", 1, 1, title = "New", headers = mapOf("k" to "new")))
        val sheet = c.season()
        assertEquals(2, sheet.episodes.size)
        val e1 = sheet.episodes.first { it.episode == 1 }
        assertEquals("New", e1.title)
        assertEquals(mapOf("k" to "new"), e1.headers)
        assertTrue(e1.selected)
        assertEquals("S", sheet.showName)
    }

    @Test
    fun episodesStaySortedWhenArrivingOutOfOrder() = runTest {
        val c = coordinator()
        listOf(3, 1, 2).forEach { c.onRequest(ep("S", 1, it)) }
        assertEquals(listOf(1, 2, 3), c.season().episodes.map { it.episode })
    }

    @Test
    fun differentShowOrSeasonIsQueuedAndOpensOnClose() = runTest {
        val c = coordinator()
        c.onRequest(ep("Show A", 1, 1))
        c.onRequest(ep("Show B", 1, 1))
        assertEquals(1, c.state.value.waitingCount)
        assertEquals("Show A", c.season().showName)
        c.onRequest(ep("Show A", 2, 1))
        assertEquals(2, c.state.value.waitingCount)

        c.close()
        assertEquals("Show B", c.season().showName)
        assertEquals(1, c.state.value.waitingCount)
        assertTrue(c.state.value.receiving) // a promoted lone episode starts its window
        advanceTimeBy(2000)
        runCurrent()
        assertFalse(c.state.value.receiving)

        c.close()
        assertEquals(2, c.season().season)
        assertEquals(0, c.state.value.waitingCount)
        c.close()
        assertEquals(IncomingState(), c.state.value)
    }

    @Test
    fun movieWhileSeasonOpenIsQueuedThenOpens() = runTest {
        val c = coordinator()
        c.onRequest(ep("S", 1, 1))
        c.onRequest(movie("Film"))
        assertEquals(1, c.state.value.waitingCount)
        assertEquals(1, c.season().episodes.size)
        c.close()
        assertEquals("Film", (c.state.value.sheet as IncomingSheet.SingleSheet).request.title)
        assertFalse(c.state.value.receiving)
        assertEquals(0, c.state.value.waitingCount)
    }

    @Test
    fun episodeWhileMovieOpenIsQueued() = runTest {
        val c = coordinator()
        c.onRequest(movie())
        c.onRequest(ep("S", 1, 1))
        assertEquals(1, c.state.value.waitingCount)
        assertTrue(c.state.value.sheet is IncomingSheet.SingleSheet)
        assertFalse(c.state.value.receiving)
    }

    @Test
    fun batchMergesIntoOpenSheetFromSingles() = runTest {
        val c = coordinator()
        c.onRequest(ep("Example Show", 1, 1, title = "Single 1"))
        c.onRequest(ep("Example Show", 1, 2))
        advanceTimeBy(2000)
        runCurrent()
        assertFalse(c.state.value.receiving)

        c.onRequest(batch("EXAMPLE SHOW", 1, 2, 3, 4, headers = mapOf("Referer" to "r")))
        assertTrue(c.state.value.receiving)
        val sheet = c.season()
        assertEquals(listOf(1, 2, 3, 4), sheet.episodes.map { it.episode })
        assertEquals("Single 1", sheet.episodes[0].title)
        assertEquals("B2", sheet.episodes[1].title) // replaced by the batch
        assertEquals(mapOf("Referer" to "r"), sheet.episodes[2].headers)
        assertTrue(sheet.episodes.all { it.selected })
        assertEquals(0, c.state.value.waitingCount)
    }

    @Test
    fun batchOpeningFreshSheetDoesNotStartReceivingWindow() = runTest {
        val c = coordinator()
        c.onRequest(batch("S", 1, 1, 2, 3))
        assertFalse(c.state.value.receiving)
        assertEquals(3, c.season().episodes.size)
        c.onRequest(ep("S", 1, 4))
        assertTrue(c.state.value.receiving)
        assertEquals(4, c.season().episodes.size)
    }

    @Test
    fun batchWithDifferentSeasonIsQueued() = runTest {
        val c = coordinator()
        c.onRequest(batch("S", 1, 1, 2))
        c.onRequest(batch("S", 2, 1))
        assertEquals(1, c.state.value.waitingCount)
    }

    @Test
    fun queuedSeasonAbsorbsMatchingRequests() = runTest {
        val c = coordinator()
        c.onRequest(movie())
        c.onRequest(ep("Show", 1, 1))
        c.onRequest(ep("show", 1, 2))
        c.onRequest(batch("SHOW", 1, 3, 4))
        c.onRequest(ep("Other", 1, 1))
        assertEquals(2, c.state.value.waitingCount)
        c.close()
        assertEquals((1..4).toList(), c.season().episodes.map { it.episode })
        assertEquals(1, c.state.value.waitingCount)
        assertTrue(c.state.value.receiving)
    }

    @Test
    fun selectionControls() = runTest {
        val c = coordinator()
        (1..4).forEach { c.onRequest(ep("S", 1, it)) }
        c.toggleEpisode(2)
        c.toggleEpisode(3)
        assertEquals(listOf(true, false, false, true), c.season().episodes.map { it.selected })
        assertEquals(2, c.season().selectedCount)
        c.toggleEpisode(2)
        assertEquals(listOf(true, true, false, true), c.season().episodes.map { it.selected })
        c.setEpisodeSelected(1, false)
        assertFalse(c.season().episodes[0].selected)
        c.setAllSelected(false)
        assertEquals(0, c.season().selectedCount)
        c.setAllSelected(true)
        assertEquals(4, c.season().selectedCount)
        c.toggleEpisode(99) // unknown episode: no-op
        assertEquals(4, c.season().selectedCount)
    }

    @Test
    fun selectionOnSingleSheetOrNoSheetIsNoOp() = runTest {
        val c = coordinator()
        c.toggleEpisode(1)
        c.setAllSelected(true)
        assertEquals(IncomingState(), c.state.value)
        c.onRequest(movie())
        val before = c.state.value
        c.toggleEpisode(1)
        assertEquals(before, c.state.value)
    }

    @Test
    fun closeCancelsReceivingAndStaleTimerDoesNotAffectNextSheet() = runTest {
        val c = coordinator()
        c.onRequest(ep("A", 1, 1))
        c.onRequest(movie())
        advanceTimeBy(1500)
        c.close() // opens the movie, receiving false
        assertFalse(c.state.value.receiving)
        c.onRequest(ep("B", 1, 1)) // queued behind the movie
        c.close() // opens B at t=1500, window until 3500
        assertTrue(c.state.value.receiving)
        advanceTimeBy(1999)
        runCurrent()
        assertTrue(c.state.value.receiving)
        advanceTimeBy(1)
        runCurrent()
        assertFalse(c.state.value.receiving)
    }

    @Test
    fun sheetIdsAreUniqueAndPreservedAcrossMerges() = runTest {
        val c = coordinator()
        c.onRequest(ep("S", 1, 1))
        val id = c.season().id
        c.onRequest(ep("S", 1, 2))
        assertEquals(id, c.season().id)
        c.onRequest(movie())
        c.close()
        assertTrue(c.state.value.sheet!!.id != id)
    }

    @Test
    fun yearAndHintAreTakenFromFirstReceivedButFilledIfMissing() = runTest {
        val c = coordinator()
        c.onRequest(ep("S", 1, 1, year = null))
        c.onRequest(ep("S", 1, 2, year = 2020))
        c.onRequest(ep("S", 1, 3, year = 2021))
        assertEquals(2020, c.season().year)
        assertEquals("tv", c.season().categoryHint)
    }
}
