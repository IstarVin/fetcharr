package dev.istarvin.fetcharr.data.sync

import app.cash.turbine.test
import dev.istarvin.fetcharr.data.network.FetcharrJson
import dev.istarvin.fetcharr.data.network.Fixtures
import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.network.dto.JobStatus
import dev.istarvin.fetcharr.data.network.dto.WsEvent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ActiveJobsStoreTest {
    private val store = ActiveJobsStore()

    private fun ws(name: String): WsEvent = WsEvent.parse(FetcharrJson, Fixtures.text(name))

    private fun job(name: String): JobDto = FetcharrJson.decodeFromString(Fixtures.text(name))

    private val snapshotIds = listOf(
        "7c2e9d40-3b1a-4e57-8d26-91f4a0c6b5e2",
        "0f6c1a6e-6f5e-4a8a-9a0e-2b7f8f4f1c11",
        "3a9b5f12-8c4d-4b6e-a1f7-6d0e2c93b845",
        "e5d81b07-2f3c-4a94-b0c8-14a7f6e9d3a2",
    )

    @Test
    fun startsEmptyWithoutSnapshot() {
        assertTrue(store.jobs.value.isEmpty())
        assertFalse(store.hasSnapshot.value)
    }

    @Test
    fun snapshotReplacesTheList() {
        store.upsert(job("job-failed.json").copy(status = JobStatus.PAUSED))
        store.apply(ws("ws-snapshot.json"))
        assertEquals(snapshotIds, store.jobs.value.map { it.id })
        assertTrue(store.hasSnapshot.value)
        store.apply(WsEvent.Snapshot(emptyList()))
        assertTrue(store.jobs.value.isEmpty())
        assertTrue(store.hasSnapshot.value)
    }

    @Test
    fun snapshotDropsFinishedJobs() {
        store.apply(WsEvent.Snapshot(listOf(job("job-pending.json"), job("job-completed.json"))))
        assertEquals(listOf("7c2e9d40-3b1a-4e57-8d26-91f4a0c6b5e2"), store.jobs.value.map { it.id })
    }

    @Test
    fun jobAddedAppendsAndReplacesExistingId() {
        store.apply(ws("ws-snapshot.json"))
        val added = job("job-failed.json").copy(id = "new-job", status = JobStatus.PENDING)
        store.apply(WsEvent.JobAdded(added))
        assertEquals(snapshotIds + "new-job", store.jobs.value.map { it.id })

        // ws-job_added.json has an id the snapshot already contains: replaced in place, not duplicated.
        store.apply(ws("ws-job_added.json"))
        assertEquals(snapshotIds + "new-job", store.jobs.value.map { it.id })
    }

    @Test
    fun jobUpdatedPatchesInPlace() {
        store.apply(ws("ws-snapshot.json"))
        val updated = job("job-downloading.json").copy(
            progress = job("job-downloading.json").progress?.copy(percent = 99.0),
        )
        store.apply(WsEvent.JobUpdated(updated))
        assertEquals(snapshotIds, store.jobs.value.map { it.id })
        assertEquals(99.0, store.jobs.value[1].progress?.percent)
    }

    @Test
    fun jobUpdatedToFinishedRemovesAndEmitsOnce() = runTest {
        store.apply(WsEvent.Snapshot(snapshotIds.map { job("job-pending.json").copy(id = it) } + job("job-completed.json").copy(status = JobStatus.PROCESSING)))
        val completedId = "91b4c6e8-0d3f-4a25-8e7b-c5a2f1d60937"
        assertTrue(store.jobs.value.any { it.id == completedId })
        store.finished.test {
            store.apply(ws("ws-job_updated.json"))
            val finished = awaitItem()
            assertEquals(completedId, finished.id)
            assertEquals(JobStatus.COMPLETED, finished.status)
            assertFalse(store.jobs.value.any { it.id == completedId })
            // The REST refresh repeating the same finish must not emit again.
            store.upsert(job("job-completed.json"))
            expectNoEvents()
        }
    }

    @Test
    fun finishedJobNotInListStillEmits() = runTest {
        store.apply(ws("ws-snapshot.json"))
        store.finished.test {
            store.apply(ws("ws-job_updated.json"))
            assertEquals("91b4c6e8-0d3f-4a25-8e7b-c5a2f1d60937", awaitItem().id)
        }
        assertEquals(snapshotIds, store.jobs.value.map { it.id })
    }

    @Test
    fun retriedJobCanFinishAgain() = runTest {
        val failed = job("job-failed.json")
        store.finished.test {
            store.upsert(failed)
            assertEquals(failed.id, awaitItem().id)
            store.upsert(failed.copy(status = JobStatus.PENDING))
            assertEquals(1, store.jobs.value.size)
            store.upsert(failed)
            assertEquals(failed.id, awaitItem().id)
        }
    }

    @Test
    fun jobRemovedDropsIds() {
        val failedId = "5f0a2d93-b7e1-4c68-9a34-e8c1d7b2f046"
        store.apply(WsEvent.Snapshot(listOf(job("job-pending.json"), job("job-failed.json").copy(status = JobStatus.PAUSED))))
        store.apply(ws("ws-job_removed.json"))
        assertEquals(listOf("7c2e9d40-3b1a-4e57-8d26-91f4a0c6b5e2"), store.jobs.value.map { it.id })
        assertFalse(store.jobs.value.any { it.id == failedId })
    }

    @Test
    fun unknownEventAndClear() {
        store.apply(ws("ws-snapshot.json"))
        store.apply(WsEvent.Unknown("future"))
        assertEquals(4, store.jobs.value.size)
        store.clear()
        assertTrue(store.jobs.value.isEmpty())
        assertFalse(store.hasSnapshot.value)
    }
}
