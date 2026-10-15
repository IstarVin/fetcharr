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
    @SerialName("ytdlp_version") val ytdlpVersion: String? = null,
    @SerialName("ytdlp_managed") val ytdlpManaged: Boolean = false,
    val concurrency: Int? = null,
    @SerialName("auth_required") val authRequired: Boolean = false,
    val categories: List<CategoryDto> = emptyList(),
    @SerialName("settings_supported") val settingsSupported: Boolean = false,
    @SerialName("setup_required") val setupRequired: Boolean = false,
)

/** `GET /api/health`. */
@Serializable
data class HealthDto(val status: String)

/** `POST /api/system/ytdlp/update`. */
@Serializable
data class YtdlpUpdateResponse(val previous: String? = null, val current: String? = null)
