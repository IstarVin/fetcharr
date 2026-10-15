package com.istarvin.fetcharr.data.network.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class ServerSettingsDto(
    val revision: Long,
    val downloads: DownloadSettingsDto,
    val history: HistorySettingsDto,
    val ytdlp: YtdlpSettingsDto,
    val categories: List<ServerCategoryDto>,
    @SerialName("media_servers") val mediaServers: List<MediaServerSettingsDto>,
)

@Serializable
data class DownloadSettingsDto(
    val concurrency: Int,
    @SerialName("merge_format") val mergeFormat: String,
    @SerialName("min_free_space") val minFreeSpace: Long,
)

@Serializable
data class HistorySettingsDto(@SerialName("retention_days") val retentionDays: Int)

@Serializable
data class YtdlpSettingsDto(@SerialName("auto_update") val autoUpdate: Boolean)

@Serializable
data class ServerCategoryDto(val id: String, val label: String, val path: String, val layout: String)

@Serializable
data class MediaServerSettingsDto(
    val id: String,
    val type: String,
    val url: String,
    @SerialName("path_map") val pathMap: Map<String, String>,
    val credential: String? = null,
    @SerialName("has_credential") val hasCredential: Boolean = false,
)
