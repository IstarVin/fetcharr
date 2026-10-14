package dev.istarvin.fetcharr.notifications.logic

import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.network.dto.JobStatus
import dev.istarvin.fetcharr.data.network.dto.ProgressDto

internal fun job(
    id: String,
    status: JobStatus = JobStatus.COMPLETED,
    batchId: String? = null,
    title: String? = null,
    showName: String? = null,
    season: Int? = null,
    episode: Int? = null,
    year: Int? = null,
    errorCode: String? = null,
    finishedAt: Long? = if (status.isFinished) 1_000L else null,
    progress: ProgressDto? = null,
    url: String = "https://cdn.example/path/video.m3u8",
) = JobDto(
    id = id, batchId = batchId, category = "tv", url = url, title = title, showName = showName,
    season = season, episode = episode, year = year, status = status, progress = progress,
    errorCode = errorCode, createdAt = 0, queuedAt = 0, finishedAt = finishedAt,
)

internal fun ep(id: String, status: JobStatus, batchId: String? = "b1", n: Int = 1, finishedAt: Long? = null, errorCode: String? = null) =
    job(id, status, batchId, showName = "Severance", season = 2, episode = n, errorCode = errorCode,
        finishedAt = finishedAt ?: if (status.isFinished) 1_000L + n else null)
