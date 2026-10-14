package com.istarvin.fetcharr.ui.queue

import com.istarvin.fetcharr.data.network.FetcharrJson
import com.istarvin.fetcharr.data.network.Fixtures
import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.data.network.dto.ProgressDto
import com.istarvin.fetcharr.data.sync.ConnectionState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class QueueModelsTest {
    private fun fixture(name: String): JobDto = FetcharrJson.decodeFromString(Fixtures.text(name))

    private val downloading = fixture("job-downloading.json") // Severance S02E01, batch B
    private val pending = fixture("job-pending.json") // Severance S02E02, batch B
    private val processing = fixture("job-processing.json") // Dune Part Two (2024), no batch
    private val paused = fixture("job-paused.json") // Conference Talk, misc
    private val batchId = downloading.batchId!!

    private val categories = listOf(
        CategoryDto("tv", "TV Shows", "tv"),
        CategoryDto("movies", "Movies", "movie"),
        CategoryDto("misc", "Misc", "flat"),
    )

    private var counter = 0

    private fun episode(n: Int, status: JobStatus, progress: ProgressDto? = null, queuedAt: Long = n.toLong(), batch: String? = batchId) =
        downloading.copy(
            id = "ep$n-${counter++}",
            batchId = batch,
            episode = n,
            title = "Episode $n",
            status = status,
            progress = progress,
            queuedAt = queuedAt,
        )

    private fun sections(vararg jobs: JobDto) = buildQueueSections(jobs.toList(), categories)

    private fun QueueSection.cards(): List<JobCardModel> = items.flatMap {
        when (it) {
            is JobItem -> listOf(it.job)
            is BatchGroup -> it.jobs
        }
    }

    @Test
    fun sectionsAreActiveQueuedPausedAndEmptyOnesAreOmitted() {
        val all = sections(paused, pending.copy(batchId = null), downloading.copy(batchId = null), processing)
        assertEquals(
            listOf(QueueSectionKind.Active, QueueSectionKind.Queued, QueueSectionKind.Paused),
            all.map { it.kind },
        )
        assertEquals(listOf(JobStatus.DOWNLOADING, JobStatus.PROCESSING), all[0].cards().map { it.status })

        assertEquals(listOf(QueueSectionKind.Queued), sections(pending).map { it.kind })
        assertEquals(listOf(QueueSectionKind.Active, QueueSectionKind.Paused), sections(paused, processing).map { it.kind })
        assertTrue(sections().isEmpty())
    }

    @Test
    fun queuedSectionIsOrderedByQueuedAtAscending() {
        val late = pending.copy(id = "late", batchId = null, queuedAt = 300)
        val early = pending.copy(id = "early", batchId = null, queuedAt = 100)
        val mid = pending.copy(id = "mid", batchId = null, queuedAt = 200)
        val queued = sections(late, early, mid).single()
        assertEquals(listOf("early", "mid", "late"), queued.cards().map { it.id })
    }

    @Test
    fun batchBecomesOneGroupAtTheFirstMembersPosition() {
        val a1 = episode(1, JobStatus.DOWNLOADING)
        val other = processing
        val a2 = episode(2, JobStatus.DOWNLOADING)
        val a3 = episode(3, JobStatus.DOWNLOADING)
        val items = sections(a1, other, a2, a3).single().items
        assertEquals(2, items.size)
        val group = items[0] as BatchGroup
        assertEquals(batchId, group.batchId)
        assertEquals("Severance — Season 2", group.title)
        assertEquals(3, group.episodeCount)
        assertEquals(listOf(a1.id, a2.id, a3.id), group.jobs.map { it.id })
        assertEquals(other.id, (items[1] as JobItem).job.id)
    }

    @Test
    fun aBatchSplitAcrossSectionsGetsOneGroupPerSection() {
        val s = sections(
            episode(1, JobStatus.DOWNLOADING),
            episode(2, JobStatus.PENDING),
            episode(3, JobStatus.PENDING),
            episode(4, JobStatus.DOWNLOADING),
        )
        assertEquals(listOf(QueueSectionKind.Active, QueueSectionKind.Queued), s.map { it.kind })
        val active = s[0].items.single() as BatchGroup
        val queued = s[1].items.single() as BatchGroup
        assertEquals(2, active.episodeCount)
        assertEquals(2, queued.episodeCount)
        assertEquals(batchId, active.batchId)
        assertEquals(batchId, queued.batchId)
        assertTrue(active.key != queued.key)
    }

    @Test
    fun aLoneBatchMemberIsStillAGroup() {
        val group = sections(downloading).single().items.single() as BatchGroup
        assertEquals("Severance — Season 2", group.title)
        assertEquals(1, group.episodeCount)
        assertEquals(listOf(downloading.id), group.jobs.map { it.id })
    }

    @Test
    fun jobsWithoutABatchAreSingleCards() {
        assertTrue(sections(downloading.copy(batchId = null)).single().items.single() is JobItem)
    }

    @Test
    fun groupTitleDropsSeasonWhenSeasonsDifferAndFallsBackToTheTitle() {
        val s1 = episode(1, JobStatus.DOWNLOADING)
        val s3 = episode(2, JobStatus.DOWNLOADING).copy(season = 3)
        assertEquals("Severance", (sections(s1, s3).single().items.single() as BatchGroup).title)
        val bare = listOf(
            processing.copy(id = "x", batchId = "bb"),
            processing.copy(id = "y", batchId = "bb"),
        )
        assertEquals("Dune Part Two", (buildQueueSections(bare, categories).single().items.single() as BatchGroup).title)
    }

    @Test
    fun combinedProgressSumsBytesWhenAllJobsReportThem() {
        val a = episode(1, JobStatus.DOWNLOADING, ProgressDto(percent = 50.0, downloadedBytes = 100, totalBytes = 200))
        val b = episode(2, JobStatus.DOWNLOADING, ProgressDto(percent = 10.0, downloadedBytes = 100, totalBytes = 800))
        val group = sections(a, b).single().items.single() as BatchGroup
        assertEquals(0.2f, group.progress!!, 0.0001f)
        assertEquals("20%", group.percentLabel)
    }

    @Test
    fun combinedProgressFallsBackToAveragePercentThenNull() {
        val a = episode(1, JobStatus.DOWNLOADING, ProgressDto(percent = 20.0))
        val b = episode(2, JobStatus.DOWNLOADING, ProgressDto(percent = 60.0, downloadedBytes = 5, totalBytes = 10))
        assertEquals(0.4f, (sections(a, b).single().items.single() as BatchGroup).progress!!, 0.0001f)

        val c = episode(1, JobStatus.PENDING)
        val d = episode(2, JobStatus.PENDING)
        val group = sections(c, d).single().items.single() as BatchGroup
        assertNull(group.progress)
        assertNull(group.percentLabel)
    }

    @Test
    fun headingsFollowTheLayout() {
        assertEquals("Severance — S02E01", sections(downloading).single().cards().single().heading)
        assertEquals("Dune Part Two (2024)", sections(processing).single().cards().single().heading)
        assertEquals("Conference Talk", sections(paused).single().cards().single().heading)
        // The year shows whenever the server sent one, whatever the layout (spec §5.3).
        assertEquals("Conference Talk (2020)", sections(paused.copy(year = 2020)).single().cards().single().heading)
        // A movie without a year, and a job without a title.
        assertEquals("Dune Part Two", sections(processing.copy(year = null)).single().cards().single().heading)
        assertEquals(processing.url, sections(processing.copy(title = null, year = null)).single().cards().single().heading)
    }

    @Test
    fun secondaryLineIsTheEpisodeTitleOnlyForEpisodes() {
        assertEquals("Hello, Ms. Cobel", sections(downloading).single().cards().single().secondary)
        assertNull(sections(processing).single().cards().single().secondary)
    }

    @Test
    fun categoryLabelComesFromCategoriesWithIdFallback() {
        assertEquals("TV Shows", sections(downloading).single().cards().single().categoryLabel)
        assertEquals("tv", buildQueueSections(listOf(downloading), emptyList()).single().cards().single().categoryLabel)
    }

    @Test
    fun statusLabelsAndProgressPerStatus() {
        val d = sections(downloading).single().cards().single()
        assertEquals("Downloading", d.statusLabel)
        assertEquals(0.427f, d.progress!!, 0.0001f)
        assertEquals("43%", d.percentLabel)
        assertFalse(d.indeterminate)
        assertTrue(d.showsProgressBar)

        val p = sections(processing).single().cards().single()
        assertEquals("Finishing…", p.statusLabel)
        assertTrue(p.indeterminate)
        assertNull(p.progress)
        assertNull(p.stats)

        val q = sections(pending.copy(batchId = null)).single().cards().single()
        assertEquals("Queued", q.statusLabel)
        assertFalse(q.showsProgressBar)
        assertNull(q.stats)

        val s = sections(paused).single().cards().single()
        assertEquals("Paused", s.statusLabel)
        assertEquals(0.184f, s.progress!!, 0.0001f)
    }

    @Test
    fun statsLineHasSpeedEtaAndSize() {
        assertEquals(
            "3.1 MiB/s · 2:12 left · 310.0 MiB / 720.0 MiB",
            sections(downloading).single().cards().single().stats,
        )
    }

    @Test
    fun statsLineOmitsMissingParts() {
        fun stats(p: ProgressDto?) = sections(downloading.copy(progress = p)).single().cards().single().stats
        assertEquals("3.0 MiB/s", stats(ProgressDto(speedBps = 3 * 1024 * 1024)))
        assertEquals("1:05 left", stats(ProgressDto(etaSeconds = 65)))
        assertEquals("1.0 KiB", stats(ProgressDto(downloadedBytes = 1024)))
        assertEquals("1.0 KiB / 2.0 KiB", stats(ProgressDto(downloadedBytes = 1024, totalBytes = 2048)))
        assertEquals("1:05 left · 1.0 KiB", stats(ProgressDto(etaSeconds = 65, downloadedBytes = 1024)))
        assertEquals("1:05 left", stats(ProgressDto(speedBps = 0, etaSeconds = 65)))
        assertNull(stats(ProgressDto(totalBytes = 2048)))
        assertNull(stats(ProgressDto()))
        assertNull(stats(null))
    }

    @Test
    fun pausedJobShowsSizeButNoSpeedOrEta() {
        assertEquals("226.1 MiB / 1.2 GiB", sections(paused).single().cards().single().stats)
    }

    @Test
    fun actionsPerStatus() {
        assertEquals(listOf(JobAction.Pause, JobAction.Cancel), actionsFor(JobStatus.PENDING))
        assertEquals(listOf(JobAction.Pause, JobAction.Cancel), actionsFor(JobStatus.DOWNLOADING))
        assertEquals(listOf(JobAction.Cancel), actionsFor(JobStatus.PROCESSING))
        assertEquals(listOf(JobAction.Resume, JobAction.Cancel), actionsFor(JobStatus.PAUSED))
        assertEquals(emptyList<JobAction>(), actionsFor(JobStatus.COMPLETED))
        assertEquals(listOf(JobAction.Cancel), sections(processing).single().cards().single().actions)
    }

    @Test
    fun bannerTextPerConnectionState() {
        assertNull(connectionBanner(ConnectionState.Connected))
        assertNull(connectionBanner(ConnectionState.NotConfigured))
        assertEquals(QueueBanner("Connecting…", connecting = true), connectionBanner(ConnectionState.Connecting))
        assertEquals(
            QueueBanner("Disconnected · retrying in 4 s", connecting = false),
            connectionBanner(ConnectionState.Disconnected(4)),
        )
        assertEquals(QueueBanner("Disconnected", connecting = false), connectionBanner(ConnectionState.Disconnected(null)))
    }

    @Test
    fun percentLabelsRoundInsteadOfTruncating() {
        for (p in 0..100) {
            assertEquals("$p%", percentOf(p / 100f))
            assertEquals(p, percentInt(p / 100f))
        }
        assertEquals("53%", percentOf(0.53f))
        assertEquals("100%", percentOf(1.2f))
    }
}
