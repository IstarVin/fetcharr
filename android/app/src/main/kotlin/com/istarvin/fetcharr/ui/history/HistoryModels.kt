package com.istarvin.fetcharr.ui.history

import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.data.network.dto.JobDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.ui.common.heading
import com.istarvin.fetcharr.util.Formatters
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

// Pure presentation mapping for the History screen (app spec §5.4). No Android or Compose types, so it is unit-tested.

enum class HistoryAction { Play, CopyPath, Retry, ReplaceSource, Details, Delete }

/** What one history row shows. */
data class HistoryRowModel(
    val id: String,
    val status: JobStatus,
    /** `finished_at`, or `created_at` when the server sent none. Epoch millis. */
    val finishedAt: Long,
    val categoryLabel: String,
    /** `Show — S02E03` for episodes, `Title (Year)` for movies, the title otherwise. */
    val heading: String,
    /** Completed: the final file path. Failed: a short error. Cancelled: `Cancelled`. */
    val statusLine: String,
    /** Completed with non-empty `warnings`: the row shows a warning icon. */
    val hasWarnings: Boolean,
    val warnings: List<String>,
    /** Completed jobs only. */
    val filePath: String?,
    val errorCode: String?,
    /** The server's full error text, behind "Details" for failed jobs. */
    val errorMessage: String?,
    val actions: List<HistoryAction>,
    /** `1.4 GiB` for a completed job whose size the server reported. */
    val sizeLabel: String? = null,
) {
    /** Text for the Details dialog: the error message (failed) or the warnings (completed), else null. */
    val detailsText: String?
        get() = when {
            status == JobStatus.FAILED -> errorMessage?.trim()?.takeIf { it.isNotEmpty() }
            hasWarnings -> warnings.joinToString("\n")
            else -> null
        }
}

/** A day heading and its rows, newest first. [key] is stable for the lazy list (ISO date). */
data class HistoryGroup(val key: String, val label: String, val items: List<HistoryRowModel>)

const val LABEL_TODAY = "Today"
const val LABEL_YESTERDAY = "Yesterday"

/** The short error shown for a failed job (app spec §5.4). Unknown or missing codes read as `Download failed`. */
fun errorText(errorCode: String?): String = when (errorCode) {
    "low_disk_space" -> "Not enough disk space on server"
    "already_exists" -> "File already exists in library"
    "download_failed" -> "Download failed"
    "processing_failed" -> "Couldn't save file"
    else -> "Download failed"
}

/**
 * Maps a finished job to its row. [layout] is the job's category layout, when known.
 *
 * Actions: completed gets Play and Copy path (when the server sent a path) and Delete, plus Details when there are warnings
 * to read; failed gets Retry, Details (when there is an error message) and Delete; cancelled gets Retry and Delete.
 */
fun JobDto.toHistoryRow(categoryLabel: String, layout: String?): HistoryRowModel {
    val path = filePath?.takeIf { it.isNotBlank() }
    val message = errorMessage?.takeIf { it.isNotBlank() }
    val warned = status == JobStatus.COMPLETED && warnings.isNotEmpty()
    val actions = when (status) {
        JobStatus.COMPLETED -> listOfNotNull(
            HistoryAction.Play.takeIf { path != null },
            HistoryAction.CopyPath.takeIf { path != null },
            HistoryAction.Details.takeIf { warned },
            HistoryAction.Delete,
        )
        JobStatus.FAILED -> listOfNotNull(
            HistoryAction.Retry,
            HistoryAction.Details.takeIf { message != null },
            HistoryAction.Delete,
        )
        JobStatus.CANCELLED -> listOf(HistoryAction.Retry, HistoryAction.Delete)
        else -> emptyList() // active jobs never appear in history
    }
    return HistoryRowModel(
        id = id,
        status = status,
        finishedAt = finishedAt ?: createdAt,
        categoryLabel = categoryLabel,
        heading = heading(layout),
        statusLine = when (status) {
            JobStatus.COMPLETED -> path ?: "Completed"
            JobStatus.FAILED -> errorText(errorCode)
            JobStatus.CANCELLED -> "Cancelled"
            else -> ""
        },
        hasWarnings = warned,
        warnings = if (warned) warnings else emptyList(),
        filePath = if (status == JobStatus.COMPLETED) path else null,
        errorCode = errorCode,
        errorMessage = message,
        actions = actions,
        sizeLabel = if (status == JobStatus.COMPLETED) {
            Formatters.bytes(progress?.totalBytes?.takeIf { it > 0 } ?: progress?.downloadedBytes?.takeIf { it > 0 })
        } else {
            null
        },
    )
}

/** Rows for [jobs] (already newest first), grouped by day. */
fun buildHistoryGroups(
    jobs: List<JobDto>,
    categories: List<CategoryDto>,
    now: Long,
    zone: ZoneId,
    locale: Locale = Locale.getDefault(),
): List<HistoryGroup> {
    val byId = categories.associateBy { it.id }
    val rows = jobs.map { job ->
        val category = byId[job.category]
        job.toHistoryRow(category?.label ?: job.category, category?.layout)
    }
    return groupByDay(rows, now, zone, locale)
}

/**
 * Groups [items] (kept in the given order) by the local calendar day of `finishedAt` in [zone]. A day boundary is
 * local midnight. Labels: `Today`, `Yesterday`, then `Mon, 12 Oct` for the current year of [now] and
 * `Mon, 12 Oct 2025` for other years, in [locale].
 */
fun groupByDay(
    items: List<HistoryRowModel>,
    now: Long,
    zone: ZoneId,
    locale: Locale = Locale.getDefault(),
): List<HistoryGroup> {
    val today = Instant.ofEpochMilli(now).atZone(zone).toLocalDate()
    val sameYear = DateTimeFormatter.ofPattern("EEE, d MMM", locale)
    val otherYear = DateTimeFormatter.ofPattern("EEE, d MMM yyyy", locale)
    val days = LinkedHashMap<LocalDate, MutableList<HistoryRowModel>>()
    for (item in items) {
        val day = Instant.ofEpochMilli(item.finishedAt).atZone(zone).toLocalDate()
        days.getOrPut(day) { mutableListOf() } += item
    }
    return days.map { (day, rows) ->
        val label = when (day) {
            today -> LABEL_TODAY
            today.minusDays(1) -> LABEL_YESTERDAY
            else -> (if (day.year == today.year) sameYear else otherYear).format(day)
        }
        HistoryGroup(key = day.toString(), label = label, items = rows)
    }
}
