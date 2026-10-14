package dev.istarvin.fetcharr.data.network.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
enum class JobStatus {
    @SerialName("pending") PENDING,
    @SerialName("downloading") DOWNLOADING,
    @SerialName("processing") PROCESSING,
    @SerialName("paused") PAUSED,
    @SerialName("completed") COMPLETED,
    @SerialName("failed") FAILED,
    @SerialName("cancelled") CANCELLED;

    /** True for `completed`, `failed` and `cancelled`. */
    val isFinished: Boolean get() = this == COMPLETED || this == FAILED || this == CANCELLED

    /** The wire value, e.g. for the `status` query parameter. */
    val wire: String get() = name.lowercase()
}

@Serializable
data class SubtitleDto(
    val url: String,
    val lang: String? = null,
)

/** Any field can be null when yt-dlp doesn't report it. */
@Serializable
data class ProgressDto(
    val percent: Double? = null,
    @SerialName("downloaded_bytes") val downloadedBytes: Long? = null,
    @SerialName("total_bytes") val totalBytes: Long? = null,
    @SerialName("speed_bps") val speedBps: Long? = null,
    @SerialName("eta_seconds") val etaSeconds: Long? = null,
)

/** Server §6.2. Request headers are never included. Timestamps are epoch millis. */
@Serializable
data class JobDto(
    val id: String,
    @SerialName("batch_id") val batchId: String? = null,
    val category: String,
    val url: String,
    val title: String? = null,
    @SerialName("show_name") val showName: String? = null,
    val season: Int? = null,
    val episode: Int? = null,
    val year: Int? = null,
    val subtitles: List<SubtitleDto> = emptyList(),
    val status: JobStatus,
    val progress: ProgressDto? = null,
    @SerialName("file_path") val filePath: String? = null,
    /** `low_disk_space`, `already_exists`, `download_failed` or `processing_failed`. */
    @SerialName("error_code") val errorCode: String? = null,
    @SerialName("error_message") val errorMessage: String? = null,
    val warnings: List<String> = emptyList(),
    @SerialName("created_at") @Serializable(with = EpochMillisSerializer::class) val createdAt: Long,
    @SerialName("queued_at") @Serializable(with = EpochMillisSerializer::class) val queuedAt: Long,
    @SerialName("started_at") @Serializable(with = EpochMillisSerializer::class) val startedAt: Long? = null,
    @SerialName("finished_at") @Serializable(with = EpochMillisSerializer::class) val finishedAt: Long? = null,
)

/** `GET /api/jobs`. The server returns `jobs` and `total`; `limit`/`offset` are tolerated if present. */
@Serializable
data class JobListDto(
    val jobs: List<JobDto>,
    val total: Int,
    val limit: Int? = null,
    val offset: Int? = null,
)

/** Response of the batch pause/resume/cancel/retry endpoints. */
@Serializable
data class BatchJobsResponse(val jobs: List<JobDto>)

/** `DELETE /api/jobs` response. */
@Serializable
data class ClearHistoryResponse(val deleted: Int)

/** Optional body of `POST /api/jobs/{id}/retry`. */
@Serializable
data class RetryRequest(val overwrite: Boolean? = null)

@Serializable
data class SkippedEpisodeDto(val episode: Int, val reason: String)

@Serializable
data class BatchCreateResponse(
    @SerialName("batch_id") val batchId: String,
    val jobs: List<JobDto>,
    val skipped: List<SkippedEpisodeDto> = emptyList(),
)
