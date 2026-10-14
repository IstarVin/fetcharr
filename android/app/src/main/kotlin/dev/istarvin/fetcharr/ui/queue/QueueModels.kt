package dev.istarvin.fetcharr.ui.queue

import dev.istarvin.fetcharr.data.network.dto.CategoryDto
import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.network.dto.JobStatus
import dev.istarvin.fetcharr.data.sync.ConnectionState
import dev.istarvin.fetcharr.ui.common.heading
import dev.istarvin.fetcharr.ui.common.isEpisode
import dev.istarvin.fetcharr.util.Formatters
import kotlin.math.roundToInt

// Pure presentation mapping for the Queue screen (app spec §5.3). No Android or Compose types, so it is unit-tested.

enum class QueueSectionKind { Active, Queued, Paused }

enum class JobAction { Pause, Resume, Cancel }

/** What one job card shows. Text is ready to display; null means "hide that line". */
data class JobCardModel(
    val id: String,
    val batchId: String?,
    val status: JobStatus,
    val categoryLabel: String,
    val statusLabel: String,
    /** `Show — S02E03` for episodes, `Title (Year)` for movies, the title otherwise. */
    val heading: String,
    /** The episode title, when there is one. */
    val secondary: String?,
    /** 0..1 for a determinate bar; null when unknown. */
    val progress: Float?,
    /** True for an indeterminate bar (processing). */
    val indeterminate: Boolean,
    /** `64%`, or null when [progress] is unknown. */
    val percentLabel: String?,
    /** `3.1 MiB/s · 1:52 left · 463 MiB / 720 MiB` without the parts the server did not send. */
    val stats: String?,
    val actions: List<JobAction>,
) {
    val showsProgressBar: Boolean get() = indeterminate || progress != null
}

sealed interface QueueItem {
    /** Stable key for the lazy list. */
    val key: String
}

data class JobItem(val job: JobCardModel, val section: QueueSectionKind) : QueueItem {
    override val key: String get() = "${section.name}:job:${job.id}"
}

/** All jobs of one batch in one section (even a single one, per spec §5.3). */
data class BatchGroup(
    val batchId: String,
    val section: QueueSectionKind,
    /** `Severance — Season 2`. */
    val title: String,
    val jobs: List<JobCardModel>,
    /** Combined progress 0..1; null when unknown. */
    val progress: Float?,
) : QueueItem {
    override val key: String get() = "${section.name}:batch:$batchId"
    val episodeCount: Int get() = jobs.size
    val percentLabel: String? get() = progress?.let(::percentOf)
}

data class QueueSection(val kind: QueueSectionKind, val items: List<QueueItem>)

data class QueueBanner(val text: String, val connecting: Boolean)

fun buildQueueSections(jobs: List<JobDto>, categories: List<CategoryDto>): List<QueueSection> {
    val labels = categories.associate { it.id to it.label }
    val layouts = categories.associate { it.id to it.layout }
    fun card(job: JobDto) = job.toCard(labels[job.category] ?: job.category, layouts[job.category])

    val active = jobs.filter { it.status == JobStatus.DOWNLOADING || it.status == JobStatus.PROCESSING }
    val queued = jobs.filter { it.status == JobStatus.PENDING }.sortedBy { it.queuedAt } // stable
    val paused = jobs.filter { it.status == JobStatus.PAUSED }

    return listOf(
        QueueSectionKind.Active to active,
        QueueSectionKind.Queued to queued,
        QueueSectionKind.Paused to paused,
    ).filter { it.second.isNotEmpty() }
        .map { (kind, list) -> QueueSection(kind, groupItems(kind, list, ::card)) }
}

private fun groupItems(kind: QueueSectionKind, jobs: List<JobDto>, card: (JobDto) -> JobCardModel): List<QueueItem> {
    val byBatch = jobs.filter { it.batchId != null }.groupBy { it.batchId!! }
    val emitted = mutableSetOf<String>()
    val items = mutableListOf<QueueItem>()
    for (job in jobs) {
        val batchId = job.batchId
        val members = batchId?.let(byBatch::get)
        if (batchId == null || members == null) {
            items += JobItem(card(job), kind)
        } else if (emitted.add(batchId)) {
            items += BatchGroup(batchId, kind, batchTitle(members), members.map(card), combinedProgress(members))
        }
    }
    return items
}

private fun batchTitle(members: List<JobDto>): String {
    val named = members.firstOrNull { !it.showName.isNullOrBlank() }
    val name = named?.showName?.trim() ?: members.firstNotNullOfOrNull { it.title?.takeIf(String::isNotBlank) } ?: "Batch"
    val seasons = members.mapNotNull { it.season }.distinct()
    return if (seasons.size == 1) "$name — Season ${seasons.single()}" else name
}

/** Sum downloaded / sum total when every job reports both, else the average of the known percents; null if none. */
private fun combinedProgress(members: List<JobDto>): Float? {
    val bytes = members.map { it.progress }
    if (bytes.all { it?.downloadedBytes != null && (it.totalBytes ?: 0L) > 0L }) {
        val total = bytes.sumOf { it!!.totalBytes!! }
        val done = bytes.sumOf { it!!.downloadedBytes!! }
        return (done.toDouble() / total).coerceIn(0.0, 1.0).toFloat()
    }
    val fractions = members.mapNotNull { jobFraction(it) }
    if (fractions.isEmpty()) return null
    return fractions.average().toFloat()
}

private fun jobFraction(job: JobDto): Float? {
    val p = job.progress ?: return null
    p.percent?.let { return (it / 100.0).coerceIn(0.0, 1.0).toFloat() }
    val total = p.totalBytes
    val done = p.downloadedBytes
    if (total != null && total > 0 && done != null) return (done.toDouble() / total).coerceIn(0.0, 1.0).toFloat()
    return null
}

/** 0..100, rounded (0.53 shows 53, not 52). */
internal fun percentInt(fraction: Float): Int = (fraction * 100).roundToInt().coerceIn(0, 100)

internal fun percentOf(fraction: Float): String = "${percentInt(fraction)}%"

private fun JobDto.toCard(categoryLabel: String, layout: String?): JobCardModel {
    val indeterminate = status == JobStatus.PROCESSING
    val fraction = if (indeterminate || status == JobStatus.PENDING) null else jobFraction(this)
    return JobCardModel(
        id = id,
        batchId = batchId,
        status = status,
        categoryLabel = categoryLabel,
        statusLabel = when (status) {
            JobStatus.PENDING -> "Queued"
            JobStatus.DOWNLOADING -> "Downloading"
            JobStatus.PROCESSING -> "Finishing…"
            JobStatus.PAUSED -> "Paused"
            JobStatus.COMPLETED -> "Completed"
            JobStatus.FAILED -> "Failed"
            JobStatus.CANCELLED -> "Cancelled"
        },
        heading = heading(layout),
        secondary = if (isEpisode()) title?.trim()?.takeIf { it.isNotEmpty() } else null,
        progress = fraction,
        indeterminate = indeterminate,
        percentLabel = fraction?.let(::percentOf),
        stats = statsLine(),
        actions = actionsFor(status),
    )
}

private fun JobDto.statsLine(): String? {
    if (status != JobStatus.DOWNLOADING && status != JobStatus.PAUSED) return null
    val p = progress ?: return null
    val live = status == JobStatus.DOWNLOADING
    val speed = if (live && (p.speedBps ?: 0L) > 0L) Formatters.speed(p.speedBps) else null
    val eta = if (live) Formatters.eta(p.etaSeconds)?.let { "$it left" } else null
    val done = Formatters.bytes(p.downloadedBytes)
    val total = Formatters.bytes(p.totalBytes)
    val size = if (done != null && total != null) "$done / $total" else done
    return listOfNotNull(speed, eta, size).takeIf { it.isNotEmpty() }?.joinToString(" · ")
}

fun actionsFor(status: JobStatus): List<JobAction> = when (status) {
    JobStatus.PENDING, JobStatus.DOWNLOADING -> listOf(JobAction.Pause, JobAction.Cancel)
    JobStatus.PROCESSING -> listOf(JobAction.Cancel)
    JobStatus.PAUSED -> listOf(JobAction.Resume, JobAction.Cancel)
    JobStatus.COMPLETED, JobStatus.FAILED, JobStatus.CANCELLED -> emptyList()
}

fun connectionBanner(state: ConnectionState): QueueBanner? = when (state) {
    ConnectionState.Connected, ConnectionState.NotConfigured -> null
    ConnectionState.Connecting -> QueueBanner("Connecting…", connecting = true)
    is ConnectionState.Disconnected ->
        QueueBanner(state.retryInSeconds?.let { "Disconnected · retrying in $it s" } ?: "Disconnected", connecting = false)
}
