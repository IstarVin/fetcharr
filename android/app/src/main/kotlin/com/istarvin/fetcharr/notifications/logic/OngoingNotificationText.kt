package com.istarvin.fetcharr.notifications.logic

import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.data.network.dto.ProgressDto
import com.istarvin.fetcharr.data.sync.ConnectionState
import com.istarvin.fetcharr.util.Formatters
import java.math.BigInteger

/** Presentation of the foreground notification. A null [progressPercent] means indeterminate. */
data class OngoingContent(
    val title: String,
    val text: String,
    val subtext: String?,
    val expandedText: String,
    val progressPercent: Int?,
)

object OngoingNotificationText {
    const val NOTHING_ACTIVE = "Waiting for downloads"
    private val whitespace = Regex("[\\s\\p{Z}]+")
    private const val MAX_VISIBLE_JOBS = 2
    private const val MAX_EXPANDED_TITLE_LENGTH = 60

    fun content(
        jobs: List<JobDto>,
        connection: ConnectionState = ConnectionState.Connected,
        snapshotFresh: Boolean = true,
    ): OngoingContent {
        val downloading = jobs.filter { it.status == JobStatus.DOWNLOADING }
        val processing = jobs.filter { it.status == JobStatus.PROCESSING }
        val queued = jobs.count { it.status == JobStatus.PENDING }
        val active = downloading + processing
        val live = connection == ConnectionState.Connected && snapshotFresh
        val summary = text(jobs)
        val title = active.singleOrNull()?.let(::heading) ?: summary
        val status = when {
            connection != ConnectionState.Connected -> "Reconnecting to server"
            !snapshotFresh -> "Updating download status"
            else -> null
        }
        val knownBytes = downloading.mapNotNull { it.progress?.knownBytes() }
        val partialSizes = knownBytes.size < downloading.size
        val percent = if (live) progressPercent(jobs) else null
        val percentText = percent?.let { "$it%${if (partialSizes) " of known sizes" else ""}" }
        val speed = if (live) combinedSpeed(downloading) else null
        val eta = if (live && active.size == 1) {
            downloading.singleOrNull()?.progress?.etaSeconds?.let(Formatters::eta)?.let { "$it left" }
        } else null
        val compact = status ?: listOfNotNull(percentText, speed, eta).joinStats().ifEmpty {
            when {
                downloading.isNotEmpty() -> "Downloading"
                processing.isNotEmpty() -> "Finishing download"
                queued > 0 -> "Waiting to start"
                else -> NOTHING_ACTIVE
            }
        }
        val expanded = buildList {
            if (status != null) add(status)
            if (live) {
                combinedSize(knownBytes)?.let {
                    add("$it transferred${if (partialSizes) " · known sizes" else ""}")
                }
            }
            for (job in active.take(MAX_VISIBLE_JOBS)) {
                add(expandedHeading(job))
                add(when {
                    !live -> "${job.statusLabel()} · last known"
                    job.status == JobStatus.PROCESSING -> "Finishing"
                    else -> job.progress?.stats() ?: "Downloading"
                })
            }
            val remaining = active.size - MAX_VISIBLE_JOBS
            val footer = buildList {
                if (remaining > 0) add("+$remaining more active")
                if (queued > 0) add("$queued queued")
            }.joinStats()
            if (footer.isNotEmpty()) add(footer)
            if (isEmpty()) add(compact)
        }.joinToString("\n")
        return OngoingContent(
            title = title,
            text = compact,
            subtext = if (active.size == 1) {
                buildList {
                    add(active.single().statusLabel())
                    if (queued > 0) add("$queued queued")
                }.joinStats()
            } else null,
            expandedText = expanded,
            progressPercent = percent,
        )
    }

    /** Counts exclude paused and finished jobs. */
    fun text(jobs: List<JobDto>): String {
        val downloading = jobs.count { it.status == JobStatus.DOWNLOADING }
        val processing = jobs.count { it.status == JobStatus.PROCESSING }
        val queued = jobs.count { it.status == JobStatus.PENDING }
        return buildList {
            if (downloading > 0) add("Downloading $downloading")
            if (processing > 0) add("Finishing $processing")
            if (queued > 0) add("$queued queued")
        }.joinStats().ifEmpty { NOTHING_ACTIVE }
    }

    /** Byte-weighted progress over downloading jobs with known, valid byte counts; rounded down. */
    fun progressPercent(jobs: List<JobDto>): Int? {
        val known = jobs.filter { it.status == JobStatus.DOWNLOADING }.mapNotNull { it.progress?.knownBytes() }
        return bytePercent(known)
    }

    private fun bytePercent(known: List<ByteCounts>): Int? {
        if (known.isEmpty()) return null
        // Preserve exact rounding down without overflowing when adding or multiplying large byte counts.
        val downloaded = known.sumOf { BigInteger.valueOf(it.downloaded) }
        val total = known.sumOf { BigInteger.valueOf(it.total) }
        return downloaded.multiply(BigInteger.valueOf(100)).divide(total).toInt()
    }

    private data class ByteCounts(val downloaded: Long, val total: Long)

    private fun ProgressDto.knownBytes(): ByteCounts? {
        val done = downloadedBytes?.takeIf { it >= 0 } ?: return null
        val total = totalBytes?.takeIf { it > 0 } ?: return null
        return ByteCounts(done.coerceAtMost(total), total)
    }

    private fun combinedSize(bytes: List<ByteCounts>): String? {
        if (bytes.isEmpty()) return null
        val done = bytes.map { it.downloaded }.sumOrNull() ?: return null
        val total = bytes.map { it.total }.sumOrNull() ?: return null
        return "${Formatters.bytes(done)} / ${Formatters.bytes(total)}"
    }

    private fun combinedSpeed(jobs: List<JobDto>): String? {
        val speeds = jobs.mapNotNull { it.progress?.speedBps?.takeIf { speed -> speed >= 0 } }
        if (speeds.isEmpty()) return null
        val sum = speeds.sumOrNull() ?: return null
        return Formatters.speed(sum)?.let { "$it${if (speeds.size < jobs.size) " reported" else ""}" }
    }

    /** An unrepresentable sum is unavailable, rather than a wrapped or fabricated metric. */
    private fun List<Long>.sumOrNull(): Long? {
        var total = 0L
        for (value in this) {
            if (value > Long.MAX_VALUE - total) return null
            total += value
        }
        return total
    }

    private fun ProgressDto.stats(): String {
        val counts = knownBytes()
        val percentage = counts?.let { bytePercent(listOf(it)) }
            ?: percent?.takeIf { it.isFinite() && it in 0.0..100.0 }?.toInt()
        val done = Formatters.bytes(counts?.downloaded ?: downloadedBytes)
        val total = Formatters.bytes(totalBytes?.takeIf { it > 0 })
        val size = when {
            done != null && total != null -> "$done / $total"
            done != null -> "$done downloaded"
            total != null -> "$total total"
            else -> null
        }
        return listOfNotNull(
            percentage?.let { "$it%" },
            Formatters.speed(speedBps),
            Formatters.eta(etaSeconds)?.let { "$it left" },
            size,
        ).joinStats().ifEmpty { "Downloading" }
    }

    private fun heading(job: JobDto): String = JobTitles.heading(job).replace(whitespace, " ").trim()

    private fun expandedHeading(job: JobDto): String {
        val title = heading(job)
        if (title.codePointCount(0, title.length) <= MAX_EXPANDED_TITLE_LENGTH) return title
        return title.substring(0, title.offsetByCodePoints(0, MAX_EXPANDED_TITLE_LENGTH - 1)).trimEnd() + "…"
    }

    private fun JobDto.statusLabel(): String = if (status == JobStatus.PROCESSING) "Finishing" else "Downloading"

    private fun List<String>.joinStats(): String = joinToString(" · ")
}
