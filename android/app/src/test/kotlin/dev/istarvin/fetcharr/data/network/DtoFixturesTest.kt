package dev.istarvin.fetcharr.data.network

import dev.istarvin.fetcharr.data.network.dto.BatchCreateResponse
import dev.istarvin.fetcharr.data.network.dto.CreateBatchRequest
import dev.istarvin.fetcharr.data.network.dto.CreateJobRequest
import dev.istarvin.fetcharr.data.network.dto.ErrorEnvelope
import dev.istarvin.fetcharr.data.network.dto.InfoDto
import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.network.dto.JobListDto
import dev.istarvin.fetcharr.data.network.dto.JobStatus
import dev.istarvin.fetcharr.data.network.dto.Rfc3339
import dev.istarvin.fetcharr.data.network.dto.WsEvent
import dev.istarvin.fetcharr.data.network.dto.YtdlpUpdateResponse
import kotlinx.serialization.SerializationException
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class DtoFixturesTest {
    private val json = FetcharrJson

    private inline fun <reified T> decode(name: String): T = json.decodeFromString(Fixtures.text(name))

    @Test
    fun infoParses() {
        val info = decode<InfoDto>("info.json")
        assertEquals("1.0.0", info.version)
        assertEquals(3, info.categories.size)
        assertEquals("tv", info.categories[1].layout)
        assertEquals(812345678901L, info.categories[0].freeBytes)
        assertTrue(info.authRequired)
    }

    @Test
    fun everyJobFixtureParsesWithMatchingStatus() {
        val expected = mapOf(
            "pending" to JobStatus.PENDING,
            "downloading" to JobStatus.DOWNLOADING,
            "processing" to JobStatus.PROCESSING,
            "paused" to JobStatus.PAUSED,
            "completed" to JobStatus.COMPLETED,
            "failed" to JobStatus.FAILED,
            "cancelled" to JobStatus.CANCELLED,
        )
        for ((name, status) in expected) {
            val job = decode<JobDto>("job-$name.json")
            assertEquals(name, status, job.status)
        }
    }

    @Test
    fun downloadingJobFields() {
        val job = decode<JobDto>("job-downloading.json")
        assertEquals(Rfc3339.parse("2026-10-14T14:30:00.000Z"), job.createdAt)
        assertEquals(Rfc3339.parse("2026-10-14T14:30:05.412Z"), job.startedAt)
        assertEquals(412L, job.startedAt!! % 1000)
        assertNull(job.finishedAt)
        assertEquals(42.7, job.progress!!.percent!!, 0.0001)
        assertEquals(132L, job.progress!!.etaSeconds)
        assertEquals(1, job.subtitles.size)
        assertEquals("en", job.subtitles[0].lang)
        assertEquals(2, job.season)
    }

    @Test
    fun nullsAndErrorsParse() {
        val failed = decode<JobDto>("job-failed.json")
        assertNull(failed.title)
        assertNull(failed.progress)
        assertEquals("download_failed", failed.errorCode)
        assertNotNull(failed.finishedAt)
        val pending = decode<JobDto>("job-pending.json")
        assertNull(pending.progress)
        assertNull(pending.startedAt)
        val completed = decode<JobDto>("job-completed.json")
        assertEquals(1, completed.warnings.size)
        assertNotNull(completed.filePath)
    }

    @Test
    fun jobListParses() {
        val list = decode<JobListDto>("jobs-list.json")
        assertEquals(7, list.jobs.size)
        assertEquals(7, list.total)
        assertEquals(JobStatus.entries.toSet(), list.jobs.map { it.status }.toSet())
    }

    @Test
    fun batchCreateResponseParses() {
        val r = decode<BatchCreateResponse>("batch-create-response.json")
        assertEquals(2, r.jobs.size)
        assertEquals(1, r.skipped.size)
        assertEquals(3, r.skipped[0].episode)
        assertEquals("duplicate", r.skipped[0].reason)
    }

    @Test
    fun ytdlpUpdateParses() {
        val r = decode<YtdlpUpdateResponse>("ytdlp-update.json")
        assertEquals("2026.09.01", r.previous)
        assertEquals("2026.09.30", r.current)
    }

    @Test
    fun errorFixturesParse() {
        for (code in Fixtures.errorCodes) {
            val env = decode<ErrorEnvelope>("error-$code.json")
            assertEquals(code, env.error.code)
            assertTrue(code, env.error.message.isNotBlank())
        }
    }

    @Test
    fun createRequestsParseAndRoundTrip() {
        for (name in listOf("create-job-movie.json", "create-job-tv.json", "create-job-flat.json")) {
            val req = decode<CreateJobRequest>(name)
            val encoded = json.encodeToString(req)
            assertEquals(name, req, json.decodeFromString<CreateJobRequest>(encoded))
            // Encoded form carries exactly the keys of the fixture: no nulls, no extras.
            assertEquals(name, json.parseToJsonElement(Fixtures.text(name)), json.parseToJsonElement(encoded))
        }
        val batch = decode<CreateBatchRequest>("create-batch.json")
        val encoded = json.encodeToString(batch)
        assertEquals(batch, json.decodeFromString<CreateBatchRequest>(encoded))
        assertEquals(json.parseToJsonElement(Fixtures.text("create-batch.json")), json.parseToJsonElement(encoded))
        assertEquals(3, batch.episodes.size)
        assertEquals("session=abc123", batch.episodes[1].headers!!["Cookie"])
    }

    @Test
    fun requestEncodingOmitsNulls() {
        val encoded = json.encodeToString(CreateJobRequest(url = "https://a/b.m3u8", category = "misc"))
        assertFalse(encoded.contains("null"))
        val keys = json.parseToJsonElement(encoded).jsonObject.keys
        assertEquals(setOf("url", "category"), keys)
    }

    @Test
    fun jobRoundTripKeepsMillis() {
        val job = decode<JobDto>("job-downloading.json")
        val again = json.decodeFromString<JobDto>(json.encodeToString(job))
        assertEquals(job, again)
        assertTrue(json.encodeToString(job).contains("\"2026-10-14T14:30:05.412Z\""))
    }

    @Test
    fun timestampsAcceptNoFractionAndOffsets() {
        val base = Rfc3339.parse("2026-10-14T14:30:00.000Z")
        assertEquals(base, Rfc3339.parse("2026-10-14T14:30:00Z"))
        assertEquals(base + 123, Rfc3339.parse("2026-10-14T14:30:00.123Z"))
        assertEquals(base, Rfc3339.parse("2026-10-14T16:30:00+02:00"))
        assertEquals("2026-10-14T14:30:00.000Z", Rfc3339.format(base))
        try {
            Rfc3339.parse("yesterday")
            fail("expected SerializationException")
        } catch (_: SerializationException) {
        }
    }

    @Test
    fun wsEventsParse() {
        val snapshot = WsEvent.parse(json, Fixtures.text("ws-snapshot.json")) as WsEvent.Snapshot
        assertEquals(4, snapshot.jobs.size)
        val added = WsEvent.parse(json, Fixtures.text("ws-job_added.json")) as WsEvent.JobAdded
        assertEquals(JobStatus.PENDING, added.job.status)
        val updated = WsEvent.parse(json, Fixtures.text("ws-job_updated.json")) as WsEvent.JobUpdated
        assertEquals(JobStatus.COMPLETED, updated.job.status)
        val removed = WsEvent.parse(json, Fixtures.text("ws-job_removed.json")) as WsEvent.JobRemoved
        assertEquals(2, removed.jobIds.size)
    }

    @Test
    fun wsUnknownAndMalformed() {
        assertEquals(WsEvent.Unknown("future"), WsEvent.parse(json, """{"type":"future","x":1}"""))
        assertNull(WsEvent.parseOrNull(json, "not json"))
        assertNull(WsEvent.parseOrNull(json, """{"jobs":[]}"""))
        assertNull(WsEvent.parseOrNull(json, """{"type":"job_added"}"""))
        assertNull(WsEvent.parseOrNull(json, "[]"))
    }

    @Test
    fun unknownKeysAreIgnored() {
        val withExtra = Fixtures.text("info.json").replaceFirst("{", """{"new_field": 1,""")
        assertEquals("1.0.0", json.decodeFromString<InfoDto>(withExtra).version)
        assertTrue(json.parseToJsonElement(withExtra) is JsonObject)
    }
}
