package dev.istarvin.fetcharr.ui.history

import dev.istarvin.fetcharr.data.network.FetcharrJson
import dev.istarvin.fetcharr.data.network.Fixtures
import dev.istarvin.fetcharr.data.network.dto.CategoryDto
import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.network.dto.JobListDto
import dev.istarvin.fetcharr.data.network.dto.JobStatus
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneId
import java.time.ZonedDateTime
import java.util.Locale

class HistoryModelsTest {
    private val zone = ZoneId.of("Asia/Manila") // UTC+8, no DST
    private val now = at(2026, 10, 14, 15, 30)

    private fun at(y: Int, m: Int, d: Int, h: Int, min: Int): Long =
        ZonedDateTime.of(y, m, d, h, min, 0, 0, zone).toInstant().toEpochMilli()

    private fun job(name: String): JobDto = FetcharrJson.decodeFromString(Fixtures.text(name))

    private fun row(id: String, finishedAt: Long) =
        job("job-completed.json").copy(id = id, finishedAt = finishedAt).toHistoryRow("Movies", "movie")

    private fun groups(vararg rows: HistoryRowModel) = groupByDay(rows.toList(), now, zone, Locale.ENGLISH)

    // --- grouping ---

    @Test
    fun groupsTodayYesterdayAndOlderDates() {
        val result = groups(
            row("a", at(2026, 10, 14, 12, 0)),
            row("b", at(2026, 10, 14, 0, 0)),
            row("c", at(2026, 10, 13, 23, 59)),
            row("d", at(2026, 10, 13, 0, 0)),
            row("e", at(2026, 10, 12, 23, 59)),
            row("f", at(2025, 12, 31, 8, 0)),
        )
        assertEquals(listOf("Today", "Yesterday", "Mon, 12 Oct", "Wed, 31 Dec 2025"), result.map { it.label })
        assertEquals(listOf(listOf("a", "b"), listOf("c", "d"), listOf("e"), listOf("f")), result.map { g -> g.items.map { it.id } })
    }

    @Test
    fun dayBoundaryIsLocalMidnightNotUtc() {
        // 2026-10-13 16:30 UTC is 2026-10-14 00:30 in Manila: today, although the UTC date is still the 13th.
        val justAfterLocalMidnight = at(2026, 10, 14, 0, 30)
        assertEquals(java.time.Instant.parse("2026-10-13T16:30:00Z").toEpochMilli(), justAfterLocalMidnight)
        assertEquals("Today", groups(row("a", justAfterLocalMidnight)).single().label)
        // One millisecond before local midnight is yesterday.
        assertEquals("Yesterday", groups(row("b", at(2026, 10, 14, 0, 0) - 1)).single().label)
    }

    @Test
    fun todayIsRelativeToNowEvenAroundMidnight() {
        val almostMidnight = at(2026, 10, 14, 23, 59)
        val result = groupByDay(listOf(row("a", at(2026, 10, 14, 23, 58)), row("b", at(2026, 10, 13, 1, 0))), almostMidnight, zone, Locale.ENGLISH)
        assertEquals(listOf("Today", "Yesterday"), result.map { it.label })
        val nextMorning = at(2026, 10, 15, 0, 1)
        val later = groupByDay(listOf(row("a", at(2026, 10, 14, 23, 58))), nextMorning, zone, Locale.ENGLISH)
        assertEquals(listOf("Yesterday"), later.map { it.label })
    }

    @Test
    fun yearIsShownOnlyForOtherYears() {
        val labels = groups(row("a", at(2026, 1, 2, 9, 0)), row("b", at(2025, 1, 2, 9, 0))).map { it.label }
        assertEquals(listOf("Fri, 2 Jan", "Thu, 2 Jan 2025"), labels)
    }

    @Test
    fun emptyInputHasNoGroups() {
        assertTrue(groups().isEmpty())
    }

    @Test
    fun groupKeysAreUniquePerDay() {
        val result = groups(row("a", at(2026, 10, 14, 12, 0)), row("b", at(2026, 10, 13, 12, 0)))
        assertEquals(listOf("2026-10-14", "2026-10-13"), result.map { it.key })
    }

    @Test
    fun buildHistoryGroupsUsesCategoryLabelsAndLayouts() {
        val flat = job("job-completed.json").copy(finishedAt = now - 1000)
        val categories = listOf(CategoryDto(id = "movies", label = "Films", layout = "flat", path = "/m", freeBytes = null))
        val row = buildHistoryGroups(listOf(flat), categories, now, zone, Locale.ENGLISH).single().items.single()
        assertEquals("Films", row.categoryLabel)
        assertEquals("Oppenheimer (2023)", row.heading) // the year shows for every layout
        val unknown = buildHistoryGroups(listOf(flat), emptyList(), now, zone, Locale.ENGLISH).single().items.single()
        assertEquals("movies", unknown.categoryLabel)
        assertEquals("Oppenheimer (2023)", unknown.heading)
    }

    // --- rows ---

    @Test
    fun completedRowShowsThePathWarningAndActions() {
        val row = job("job-completed.json").toHistoryRow("Movies", "movie")
        assertEquals("Oppenheimer (2023)", row.heading)
        assertEquals("/media/movies/Oppenheimer (2023)/Oppenheimer (2023).mp4", row.statusLine)
        assertEquals(row.statusLine, row.filePath)
        assertTrue(row.hasWarnings)
        assertEquals(listOf(HistoryAction.CopyPath, HistoryAction.Details, HistoryAction.Delete), row.actions)
        assertEquals("subtitle es (https://cdn.example/oppenheimer.es.vtt): download failed: HTTP 404", row.detailsText)
    }

    @Test
    fun completedWithoutWarningsHasCopyPathAndDeleteOnly() {
        val row = job("job-completed.json").copy(warnings = emptyList()).toHistoryRow("Movies", "movie")
        assertFalse(row.hasWarnings)
        assertEquals(listOf(HistoryAction.CopyPath, HistoryAction.Delete), row.actions)
        assertNull(row.detailsText)
    }

    @Test
    fun failedEpisodeRowShowsAShortErrorAndKeepsTheMessageForDetails() {
        val row = job("job-failed.json").toHistoryRow("TV Shows", "tv")
        assertEquals("Andor — S01E03", row.heading)
        assertEquals("Download failed", row.statusLine)
        assertEquals("ERROR: [generic] Unable to download webpage: HTTP Error 403: Forbidden", row.errorMessage)
        assertEquals(row.errorMessage, row.detailsText)
        assertNull(row.filePath)
        assertFalse(row.hasWarnings)
        assertEquals(listOf(HistoryAction.Retry, HistoryAction.Details, HistoryAction.Delete), row.actions)
    }

    @Test
    fun failedWithoutAMessageHasNoDetails() {
        val row = job("job-failed.json").copy(errorMessage = null).toHistoryRow("TV", "tv")
        assertEquals(listOf(HistoryAction.Retry, HistoryAction.Delete), row.actions)
        assertNull(row.detailsText)
    }

    @Test
    fun cancelledRowSaysCancelledAndOffersRetryAndDelete() {
        val row = job("job-cancelled.json").toHistoryRow("Misc", "flat")
        assertEquals("Cancelled", row.statusLine)
        assertEquals("Podcast Episode 12", row.heading)
        assertEquals(listOf(HistoryAction.Retry, HistoryAction.Delete), row.actions)
    }

    @Test
    fun errorTextByCode() {
        assertEquals("Not enough disk space on server", errorText("low_disk_space"))
        assertEquals("File already exists in library", errorText("already_exists"))
        assertEquals("Download failed", errorText("download_failed"))
        assertEquals("Couldn't save file", errorText("processing_failed"))
        assertEquals("Download failed", errorText("something_new"))
        assertEquals("Download failed", errorText(null))
        val failed = job("job-failed.json")
        assertEquals("Not enough disk space on server", failed.copy(errorCode = "low_disk_space").toHistoryRow("x", null).statusLine)
        assertEquals("Couldn't save file", failed.copy(errorCode = "processing_failed").toHistoryRow("x", null).statusLine)
    }

    @Test
    fun rowsFromTheListFixtureAreOrderedAndLabelled() {
        val list: JobListDto = FetcharrJson.decodeFromString(Fixtures.text("jobs-list.json"))
        val finished = list.jobs.filter { it.status.isFinished }
        assertTrue(finished.isNotEmpty())
        for (job in finished) {
            val row = job.toHistoryRow("c", null)
            assertTrue(row.heading.isNotBlank())
            assertTrue(row.statusLine.isNotBlank())
            assertTrue(HistoryAction.Delete in row.actions)
        }
    }

    @Test
    fun finishedAtFallsBackToCreatedAt() {
        val job = job("job-cancelled.json").copy(finishedAt = null, status = JobStatus.CANCELLED)
        assertEquals(job.createdAt, job.toHistoryRow("c", null).finishedAt)
    }
}
