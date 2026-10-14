package com.istarvin.fetcharr.data.network.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * `POST /api/jobs`. Null fields are omitted when encoded (the shared Json uses explicitNulls = false).
 * [headers] are request headers for the media fetch; they must never be logged.
 */
@Serializable
data class CreateJobRequest(
    val url: String,
    val category: String,
    val title: String? = null,
    @SerialName("show_name") val showName: String? = null,
    val season: Int? = null,
    val episode: Int? = null,
    val year: Int? = null,
    val headers: Map<String, String>? = null,
    val subtitles: List<SubtitleDto>? = null,
)

@Serializable
data class BatchEpisodeRequest(
    val episode: Int,
    val title: String? = null,
    val url: String,
    val subtitles: List<SubtitleDto>? = null,
    val headers: Map<String, String>? = null,
)

/** `POST /api/jobs/batch`. Episode-level headers are merged over batch-level headers by the server. */
@Serializable
data class CreateBatchRequest(
    val category: String,
    @SerialName("show_name") val showName: String,
    val season: Int,
    val year: Int? = null,
    val headers: Map<String, String>? = null,
    val episodes: List<BatchEpisodeRequest>,
)
