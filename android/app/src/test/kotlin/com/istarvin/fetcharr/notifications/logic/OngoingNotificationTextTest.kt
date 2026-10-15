package com.istarvin.fetcharr.notifications.logic

import com.istarvin.fetcharr.data.network.dto.JobStatus.DOWNLOADING
import com.istarvin.fetcharr.data.network.dto.JobStatus.PAUSED
import com.istarvin.fetcharr.data.network.dto.JobStatus.PENDING
import com.istarvin.fetcharr.data.network.dto.JobStatus.PROCESSING
import com.istarvin.fetcharr.data.network.dto.ProgressDto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class OngoingNotificationTextTest {
    private fun dl(id: String, done: Long?, total: Long?) =
        job(id, DOWNLOADING, progress = ProgressDto(downloadedBytes = done, totalBytes = total))

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

    @Test fun contentBundlesBoth() =
        assertEquals(OngoingContent("Downloading 1", 50), OngoingNotificationText.content(listOf(dl("a", 50, 100))))
}
