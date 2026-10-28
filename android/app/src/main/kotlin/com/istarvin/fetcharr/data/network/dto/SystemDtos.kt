package com.istarvin.fetcharr.data.network.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class CategoryDto(
    val id: String,
    val label: String,
    /** `movie`, `tv` or `flat`. */
    val layout: String,
    val path: String? = null,
    @SerialName("free_bytes") val freeBytes: Long? = null,
) {
    companion object {
        const val LAYOUT_MOVIE = "movie"
        const val LAYOUT_TV = "tv"
        const val LAYOUT_FLAT = "flat"
    }
}

/** `GET /api/info`. */
@Serializable
data class InfoDto(
    val version: String,
    @SerialName("source_replacement_supported") val sourceReplacementSupported: Boolean = false,
    @SerialName("ytdlp_version") val ytdlpVersion: String? = null,
    @SerialName("ytdlp_managed") val ytdlpManaged: Boolean = false,
    val concurrency: Int? = null,
    @SerialName("auth_required") val authRequired: Boolean = false,
    val categories: List<CategoryDto> = emptyList(),
    @SerialName("settings_supported") val settingsSupported: Boolean = false,
    @SerialName("setup_required") val setupRequired: Boolean = false,
    @SerialName("queue_priority_supported") val queuePrioritySupported: Boolean = false,
    @SerialName("history_search_supported") val historySearchSupported: Boolean = false,
    @SerialName("notifications_supported") val notificationsSupported: Boolean = false,
    /** Null without a window (or on older servers). */
    @SerialName("download_window") val downloadWindow: DownloadWindowDto? = null,
)

/** The daily window in which new downloads start (`GET /api/info`). */
@Serializable
data class DownloadWindowDto(
    val start: String,
    val end: String,
    val open: Boolean,
    /** RFC 3339; null while the window is open. */
    @SerialName("opens_at") val opensAt: String? = null,
)

/** `GET /api/health`. */
@Serializable
data class HealthDto(val status: String)

/** `POST /api/system/ytdlp/update`. */
@Serializable
data class YtdlpUpdateResponse(val previous: String? = null, val current: String? = null)

/** `GET /api/system/ytdlp/check`. */
@Serializable
data class YtdlpCheckResponse(
    val current: String,
    val latest: String,
    @SerialName("update_available") val updateAvailable: Boolean,
)
