package com.istarvin.fetcharr.notifications.logic

import com.istarvin.fetcharr.data.network.dto.JobStatus.DOWNLOADING
import com.istarvin.fetcharr.data.network.dto.JobStatus.PAUSED
import com.istarvin.fetcharr.data.network.dto.JobStatus.PENDING
import com.istarvin.fetcharr.data.network.dto.JobStatus.PROCESSING
import com.istarvin.fetcharr.data.network.dto.ProgressDto
import com.istarvin.fetcharr.data.sync.ConnectionState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class OngoingNotificationTextTest {
    private fun dl(
        id: String,
        done: Long?,
        total: Long?,
        speed: Long? = null,
        eta: Long? = null,
        title: String? = null,
        percent: Double? = null,
    ) = job(id, DOWNLOADING, title = title, progress = ProgressDto(
        downloadedBytes = done, totalBytes = total, speedBps = speed, etaSeconds = eta, percent = percent,
    ))

    private fun pend(id: String) = job(id, PENDING)

    @Test fun downloadingAndQueued() =
        assertEquals("Downloading 2 · 3 queued", OngoingNotificationText.text(listOf(dl("a", 1, 2), dl("b", 1, 2), pend("c"), pend("d"), pend("e"))))

    @Test fun onlyDownloading() = assertEquals("Downloading 1", OngoingNotificationText.text(listOf(dl("a", 1, 2))))

    @Test fun onlyQueued() = assertEquals("3 queued", OngoingNotificationText.text(listOf(pend("a"), pend("b"), pend("c"))))

    @Test fun processing() {
        assertEquals("Finishing 1", OngoingNotificationText.text(listOf(job("p", PROCESSING))))
        assertEquals("Downloading 1 · Finishing 1 · 2 queued",
            OngoingNotificationText.text(listOf(dl("a", 1, 2), job("p", PROCESSING), pend("q"), pend("r"))))
    }

    @Test fun pausedIgnoredAndEmptyFallback() {
        assertEquals("Waiting for downloads", OngoingNotificationText.text(listOf(job("x", PAUSED))))
        assertEquals("Waiting for downloads", OngoingNotificationText.text(emptyList()))
    }

    @Test fun combinedProgress() {
        // (50 + 300) / (100 + 400) = 70%
        assertEquals(70, OngoingNotificationText.progressPercent(listOf(dl("a", 50, 100), dl("b", 300, 400), pend("c"))))
    }

    @Test fun progressRoundsDown() = assertEquals(33, OngoingNotificationText.progressPercent(listOf(dl("a", 1, 3))))

    @Test fun unknownTotalsAreIndeterminate() {
        assertNull(OngoingNotificationText.progressPercent(listOf(dl("a", 50, null))))
        assertNull(OngoingNotificationText.progressPercent(listOf(dl("a", null, 100))))
        assertNull(OngoingNotificationText.progressPercent(listOf(dl("a", 0, 0))))
        assertNull(OngoingNotificationText.progressPercent(listOf(job("p", PROCESSING), pend("q"))))
        assertNull(OngoingNotificationText.progressPercent(emptyList()))
    }

    @Test fun jobsWithUnknownTotalAreExcludedFromSum() =
        assertEquals(50, OngoingNotificationText.progressPercent(listOf(dl("a", 50, 100), dl("b", 999, null))))

    @Test fun clampedTo0To100() =
        assertEquals(100, OngoingNotificationText.progressPercent(listOf(dl("a", 500, 100))))

    @Test fun singleDownloadShowsTitleAndLiveMetrics() {
        val content = OngoingNotificationText.content(listOf(
            dl("a", 512 * 1024, 1024 * 1024, speed = 1024, eta = 84, title = "Movie"),
            pend("q"),
        ))
        assertEquals("Movie", content.title)
        assertEquals("50% · 1.0 KiB/s · 1:24 left", content.text)
        assertEquals("Downloading · 1 queued", content.subtext)
        assertEquals(50, content.progressPercent)
        assertEquals(
            "512.0 KiB / 1.0 MiB transferred\nMovie\n50% · 1.0 KiB/s · 1:24 left · 512.0 KiB / 1.0 MiB\n1 queued",
            content.expandedText,
        )
    }

    @Test fun mediaHeadingsReuseMovieAndEpisodeNames() {
        assertEquals("Movie (2024)", OngoingNotificationText.content(listOf(
            job("a", DOWNLOADING, title = "Movie", year = 2024),
        )).title)
        assertEquals("Severance — S02E03", OngoingNotificationText.content(listOf(
            ep("a", DOWNLOADING, n = 3),
        )).title)
    }

    @Test fun concurrentDownloadsShowWeightedProgressAndCombinedSpeedWithoutCombinedEta() {
        val content = OngoingNotificationText.content(listOf(
            dl("a", 50, 100, speed = 1024, eta = 10, title = "First"),
            dl("b", 300, 400, speed = 2048, eta = 60, title = "Second"),
            pend("q"),
        ))
        assertEquals("Downloading 2 · 1 queued", content.title)
        assertEquals("70% · 3.0 KiB/s", content.text)
        assertNull(content.subtext)
        assertEquals(70, content.progressPercent)
        assertTrue(content.expandedText.startsWith("350 B / 500 B transferred\nFirst\n"))
        assertTrue(content.expandedText.contains("50% · 1.0 KiB/s · 0:10 left · 50 B / 100 B"))
        assertTrue(content.expandedText.contains("Second\n75% · 2.0 KiB/s · 1:00 left · 300 B / 400 B"))
    }

    @Test fun partialMetricsAreExplicitlyLabelled() {
        val content = OngoingNotificationText.content(listOf(
            dl("a", 50, 100, speed = 1024, title = "Known"),
            dl("b", 999, null, title = "Unknown"),
        ))
        assertEquals("50% of known sizes · 1.0 KiB/s reported", content.text)
        assertTrue(content.expandedText.startsWith("50 B / 100 B transferred · known sizes\n"))
        assertTrue(content.expandedText.contains("Unknown\n999 B downloaded"))
    }

    @Test fun unknownSizesRemainIndeterminateButStillShowSpeedAndEta() {
        val content = OngoingNotificationText.content(listOf(dl("a", 500, null, speed = 1024, eta = 10)))
        assertNull(content.progressPercent)
        assertEquals("1.0 KiB/s · 0:10 left", content.text)
        assertTrue(content.expandedText.endsWith("1.0 KiB/s · 0:10 left · 500 B downloaded"))
        assertFalse(content.expandedText.contains("transferred"))
    }

    @Test fun missingProgressDoesNotFabricateZeroMetrics() {
        val content = OngoingNotificationText.content(listOf(job("a", DOWNLOADING, title = "Movie")))
        assertEquals("Downloading", content.text)
        assertEquals("Movie\nDownloading", content.expandedText)
        assertNull(content.progressPercent)
    }

    @Test fun invalidMetricsAreHidden() {
        val content = OngoingNotificationText.content(listOf(
            dl("a", -1, -100, speed = -1, eta = -1, percent = Double.NaN),
        ))
        assertEquals("Downloading", content.text)
        assertFalse(content.expandedText.contains("%"))
        assertFalse(content.expandedText.contains(" B"))
        assertFalse(content.expandedText.contains("left"))
        assertNull(content.progressPercent)
        for (percent in listOf(Double.POSITIVE_INFINITY, Double.NEGATIVE_INFINITY, -1.0, 101.0)) {
            assertFalse(OngoingNotificationText.content(listOf(dl("a", null, null, percent = percent))).expandedText.contains("%"))
        }
    }

    @Test fun validReportedZeroValuesAreNotTreatedAsMissing() {
        val content = OngoingNotificationText.content(listOf(dl("a", 0, 100, speed = 0, eta = 0)))
        assertEquals("0% · 0 B/s · 0:00 left", content.text)
        assertEquals(0, content.progressPercent)
    }

    @Test fun perJobPercentageCanUseServerPercentWithoutInventingByteTotals() {
        val content = OngoingNotificationText.content(listOf(dl("a", null, null, percent = 42.9)))
        assertTrue(content.expandedText.endsWith("42%"))
        assertNull(content.progressPercent)
    }

    @Test fun largeByteCountsCannotOverflowWeightedProgress() {
        assertEquals(49, OngoingNotificationText.progressPercent(listOf(
            dl("a", Long.MAX_VALUE / 2, Long.MAX_VALUE),
            dl("b", Long.MAX_VALUE / 2, Long.MAX_VALUE),
        )))
    }

    @Test fun unrepresentableMetricSumsAreOmittedInsteadOfWrapping() {
        val content = OngoingNotificationText.content(listOf(
            dl("a", Long.MAX_VALUE / 2, Long.MAX_VALUE, speed = Long.MAX_VALUE),
            dl("b", Long.MAX_VALUE / 2, Long.MAX_VALUE, speed = Long.MAX_VALUE),
        ))
        assertEquals("49%", content.text)
        assertFalse(content.expandedText.contains("transferred"))
    }

    @Test fun expandedJobsPrioritizeDownloadingAndKeepServerOrder() {
        val content = OngoingNotificationText.content(listOf(
            job("p", PROCESSING, title = "Processing"),
            dl("b", 50, 100, title = "Second in server list"),
            dl("a", 50, 100, title = "Third in server list"),
            dl("c", 50, 100, title = "Hidden download"),
            pend("q1"), pend("q2"),
        ))
        assertEquals("Downloading 3 · Finishing 1 · 2 queued", content.title)
        assertTrue(content.expandedText.indexOf("Second in server list") < content.expandedText.indexOf("Third in server list"))
        assertFalse(content.expandedText.contains("Processing"))
        assertFalse(content.expandedText.contains("Hidden download"))
        assertTrue(content.expandedText.endsWith("+2 more active · 2 queued"))
    }

    @Test fun processingAndQueuedJobsNeverReuseOldTransferMetrics() {
        val stale = ProgressDto(downloadedBytes = 50, totalBytes = 100, speedBps = 1024, etaSeconds = 84)
        val processing = OngoingNotificationText.content(listOf(job("p", PROCESSING, title = "Movie", progress = stale)))
        assertEquals("Movie", processing.title)
        assertEquals("Finishing", processing.subtext)
        assertEquals("Finishing download", processing.text)
        assertEquals("Movie\nFinishing", processing.expandedText)
        assertNull(processing.progressPercent)
        val queued = OngoingNotificationText.content(listOf(job("q", PENDING, progress = stale)))
        assertEquals("1 queued", queued.title)
        assertEquals("Waiting to start", queued.text)
        assertEquals("1 queued", queued.expandedText)
        assertNull(queued.progressPercent)
    }

    @Test fun mixedDownloadAndProcessingIncludesFinishingDetailsWithoutEtaForTheWholeGroup() {
        val content = OngoingNotificationText.content(listOf(
            job("p", PROCESSING, title = "Finishing movie"),
            dl("a", 50, 100, speed = 1024, eta = 84, title = "Downloading movie"),
        ))
        assertEquals("Downloading 1 · Finishing 1", content.title)
        assertEquals("50% · 1.0 KiB/s", content.text)
        assertTrue(content.expandedText.endsWith("Finishing movie\nFinishing"))
    }

    @Test fun pausedAndFinishedJobsDoNotAffectPresentation() {
        val downloading = dl("a", 50, 100, speed = 1024)
        assertEquals(
            OngoingNotificationText.content(listOf(downloading)),
            OngoingNotificationText.content(listOf(downloading, job("p", PAUSED), job("c"))),
        )
        val empty = OngoingNotificationText.content(emptyList())
        assertEquals("Waiting for downloads", empty.title)
        assertEquals("Waiting for downloads", empty.text)
        assertEquals("Waiting for downloads", empty.expandedText)
        assertNull(empty.progressPercent)
    }

    @Test fun titlesCannotInjectExtraNotificationLines() {
        val content = OngoingNotificationText.content(listOf(
            dl("a", 50, 100, title = "  A\n\r\tlong\u00a0title\u2028with\u2029breaks  "),
        ))
        assertEquals("A long title with breaks", content.title)
        assertEquals(3, content.expandedText.lines().size)
    }

    @Test fun longExpandedTitlesAreBoundedWithoutSplittingUnicodeCharacters() {
        val title = "🎬".repeat(100)
        val content = OngoingNotificationText.content(listOf(dl("a", 50, 100, title = title)))
        assertEquals(title, content.title)
        assertEquals("🎬".repeat(59) + "…", content.expandedText.lines()[1])
        assertTrue(content.expandedText.endsWith("50% · 50 B / 100 B"))
    }

    @Test fun reconnectAndSnapshotRefreshImmediatelySuppressStaleMetrics() {
        val jobs = listOf(dl("a", 50, 100, speed = 1024, eta = 84, title = "Movie"))
        val live = OngoingNotificationText.content(jobs)
        for (connection in listOf(ConnectionState.Connecting, ConnectionState.Disconnected(5), ConnectionState.NotConfigured)) {
            val offline = OngoingNotificationText.content(jobs, connection, snapshotFresh = true)
            assertEquals("Reconnecting to server", offline.text)
            assertNull(offline.progressPercent)
            assertEquals("Reconnecting to server\nMovie\nDownloading · last known", offline.expandedText)
        }
        val refreshing = OngoingNotificationText.content(jobs, ConnectionState.Connected, snapshotFresh = false)
        assertEquals("Updating download status", refreshing.text)
        assertNull(refreshing.progressPercent)
        assertFalse(refreshing.expandedText.contains("KiB/s"))
        assertFalse(refreshing.expandedText.contains("left"))
        assertEquals(live, OngoingNotificationText.content(jobs, ConnectionState.Connected, snapshotFresh = true))
    }

    @Test fun changesThatDoNotAffectDisplayedContentRemainEqualForDeduplication() {
        val original = dl("a", 50, 100, speed = 1024)
        assertEquals(
            OngoingNotificationText.content(listOf(original), ConnectionState.Disconnected(5)),
            OngoingNotificationText.content(listOf(original), ConnectionState.Disconnected(4)),
        )
        assertEquals(
            OngoingNotificationText.content(listOf(original)),
            OngoingNotificationText.content(listOf(original.copy(warnings = listOf("Hidden warning")))),
        )
    }
}
