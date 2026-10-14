package com.istarvin.fetcharr.notifications.logic

import com.istarvin.fetcharr.data.network.dto.JobStatus.CANCELLED
import com.istarvin.fetcharr.data.network.dto.JobStatus.COMPLETED
import com.istarvin.fetcharr.data.network.dto.JobStatus.DOWNLOADING
import com.istarvin.fetcharr.data.network.dto.JobStatus.FAILED
import com.istarvin.fetcharr.data.network.dto.JobStatus.PAUSED
import com.istarvin.fetcharr.data.network.dto.JobStatus.PENDING
import com.istarvin.fetcharr.data.network.dto.JobStatus.PROCESSING
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ResultNotificationDeciderTest {
    private val state = NotifierState()
    private val movie = job("m1", COMPLETED, title = "Dune Part Two", year = 2024)

    private fun post(d: ResultDecision): ResultNotification {
        assertTrue("expected PostJob but was $d", d is ResultDecision.PostJob)
        return (d as ResultDecision.PostJob).notification
    }

    @Test fun completedJobWithoutBatch() {
        val n = post(ResultNotificationDecider.onJobFinished(movie, emptyList(), state))
        assertEquals("Downloaded · Dune Part Two (2024)", n.combined)
        assertEquals("m1", n.key)
        assertNull(n.retryJobId)
    }

    @Test fun failedJobWithoutBatch() {
        val j = job("m2", FAILED, title = "Dune Part Two", year = 2024, errorCode = "download_failed")
        val n = post(ResultNotificationDecider.onJobFinished(j, emptyList(), state))
        assertEquals("Failed · Dune Part Two (2024) — Download failed", n.combined)
        assertEquals("m2", n.retryJobId)
        assertNull(n.retryBatchId)
    }

    @Test fun errorTextTable() {
        assertEquals("Not enough disk space on server", ResultNotificationDecider.errorText("low_disk_space"))
        assertEquals("File already exists in library", ResultNotificationDecider.errorText("already_exists"))
        assertEquals("Download failed", ResultNotificationDecider.errorText("download_failed"))
        assertEquals("Couldn't save file", ResultNotificationDecider.errorText("processing_failed"))
        assertEquals("Download failed", ResultNotificationDecider.errorText("weird"))
        assertEquals("Download failed", ResultNotificationDecider.errorText(null))
    }

    @Test fun failedUsesCodeText() {
        val j = job("m3", FAILED, title = "X", errorCode = "processing_failed")
        assertEquals("Failed · X — Couldn't save file", post(ResultNotificationDecider.onJobFinished(j, emptyList(), state)).combined)
        val unknown = job("m4", FAILED, title = "X")
        assertEquals("Failed · X — Download failed", post(ResultNotificationDecider.onJobFinished(unknown, emptyList(), state)).combined)
    }

    @Test fun cancelledSkipsWithoutBatch() {
        val d = ResultNotificationDecider.onJobFinished(job("c", CANCELLED), emptyList(), state)
        assertTrue(d is ResultDecision.Skip)
    }

    @Test fun unfinishedJobSkips() {
        val d = ResultNotificationDecider.onJobFinished(job("c", DOWNLOADING), emptyList(), state)
        assertTrue(d is ResultDecision.Skip)
    }

    @Test fun batchWaitsWhileSiblingsActive() {
        val last = ep("e1", COMPLETED, n = 1)
        for (s in listOf(PENDING, DOWNLOADING, PROCESSING, PAUSED)) {
            val d = ResultNotificationDecider.onJobFinished(last, listOf(ep("e2", s, n = 2)), state)
            assertEquals("sibling $s", ResultDecision.Skip("batch not finished"), d)
        }
    }

    @Test fun otherBatchActiveDoesNotBlock() {
        val d = ResultNotificationDecider.onJobFinished(ep("e1", COMPLETED), listOf(ep("x", DOWNLOADING, batchId = "other")), state)
        assertEquals(ResultDecision.SummarizeBatch("b1"), d)
    }

    @Test fun lastJobTriggersSummary() {
        val d = ResultNotificationDecider.onJobFinished(ep("e1", COMPLETED), emptyList(), state)
        assertEquals(ResultDecision.SummarizeBatch("b1"), d)
    }

    @Test fun cancelledLastJobInBatchStillSummarizes() {
        val d = ResultNotificationDecider.onJobFinished(ep("e1", CANCELLED), emptyList(), state)
        assertEquals(ResultDecision.SummarizeBatch("b1"), d)
    }

    @Test fun summary8Completed1Failed1Cancelled() {
        val jobs = (1..8).map { ep("e$it", COMPLETED, n = it) } + ep("e9", FAILED, n = 9) + ep("e10", CANCELLED, n = 10)
        val n = ResultNotificationDecider.summarize("b1", jobs)!!
        assertEquals("Severance S02 — 8 downloaded, 1 failed", n.combined)
        assertEquals("batch:b1", n.key)
        assertEquals("b1", n.retryBatchId)
        assertNull(n.retryJobId)
    }

    @Test fun summaryOmitsZeroParts() {
        val ok = ResultNotificationDecider.summarize("b1", listOf(ep("a", COMPLETED), ep("b", COMPLETED, n = 2)))!!
        assertEquals("Severance S02 — 2 downloaded", ok.combined)
        assertNull(ok.retryBatchId)
        val bad = ResultNotificationDecider.summarize("b1", listOf(ep("a", FAILED)))!!
        assertEquals("Severance S02 — 1 failed", bad.combined)
        assertEquals("b1", bad.retryBatchId)
    }

    @Test fun allCancelledNoSummary() {
        assertNull(ResultNotificationDecider.summarize("b1", listOf(ep("a", CANCELLED), ep("b", CANCELLED, n = 2))))
    }

    @Test fun emptyOrUnfinishedNoSummary() {
        assertNull(ResultNotificationDecider.summarize("b1", emptyList()))
        assertNull(ResultNotificationDecider.summarize("b1", listOf(ep("a", COMPLETED), ep("b", PAUSED, n = 2))))
    }

    @Test fun retriedEpisodeAfterSummaryGetsOwnNotification() {
        val s = state.withBatchSummarized("b1")
        val retried = ep("e9", COMPLETED, n = 9, finishedAt = 9_999)
        val n = post(ResultNotificationDecider.onJobFinished(retried, emptyList(), s))
        assertEquals("Downloaded · Severance — S02E09", n.combined)
        val failedAgain = ep("e9", FAILED, n = 9, finishedAt = 10_000, errorCode = "low_disk_space")
        assertEquals(
            "Failed · Severance — S02E09 — Not enough disk space on server",
            post(ResultNotificationDecider.onJobFinished(failedAgain, emptyList(), s)).combined,
        )
        assertTrue(ResultNotificationDecider.onJobFinished(ep("e9", CANCELLED), emptyList(), s) is ResultDecision.Skip)
    }

    @Test fun duplicateDeliveryDoesNotPostTwice() {
        var s = state
        val d1 = ResultNotificationDecider.onJobFinished(movie, emptyList(), s)
        post(d1)
        s = ResultNotificationDecider.record(s, movie, d1)
        val d2 = ResultNotificationDecider.onJobFinished(movie, emptyList(), s)
        assertEquals(ResultDecision.Skip("duplicate event"), d2)
    }

    @Test fun duplicateOfSummarizedBatchJobIsNotPostedPerJob() {
        var s = state
        val last = ep("e1", COMPLETED)
        val d1 = ResultNotificationDecider.onJobFinished(last, emptyList(), s)
        assertTrue(d1 is ResultDecision.SummarizeBatch)
        s = ResultNotificationDecider.record(s, last, d1)
        assertTrue("b1" in s.summarizedBatchIds)
        assertEquals(ResultDecision.Skip("duplicate event"), ResultNotificationDecider.onJobFinished(last, emptyList(), s))
    }

    @Test fun sameJobFinishingAgainAfterRetryIsNotADuplicate() {
        val first = job("m1", FAILED, title = "X", finishedAt = 1_000)
        val s = ResultNotificationDecider.record(state, first, ResultDecision.Skip("x"))
        val again = job("m1", COMPLETED, title = "X", finishedAt = 2_000)
        assertNotNull(post(ResultNotificationDecider.onJobFinished(again, emptyList(), s)))
    }

    @Test fun recordMarksSkipsToo() {
        val s = ResultNotificationDecider.record(state, ep("e1", COMPLETED), ResultDecision.Skip("batch not finished"))
        assertTrue(s.hasNotified(ep("e1", COMPLETED)))
        assertTrue(s.summarizedBatchIds.isEmpty())
    }
}
