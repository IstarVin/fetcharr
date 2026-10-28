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
    /** Null on servers without push notifications; then it is not sent back either. */
    val notifications: List<NotificationSettingsDto>? = null,
)

/** The nullable fields are absent on older servers and are only sent back when they were received. */
@Serializable
data class DownloadSettingsDto(
    val concurrency: Int,
    @SerialName("merge_format") val mergeFormat: String,
    @SerialName("min_free_space") val minFreeSpace: Long,
    /** Bytes per second per download; 0 = unlimited. */
    @SerialName("rate_limit") val rateLimit: Long? = null,
    /** "HH:MM" server time, or "" for any time. */
    @SerialName("window_start") val windowStart: String? = null,
    @SerialName("window_end") val windowEnd: String? = null,
    @SerialName("auto_retries") val autoRetries: Int? = null,
)

/** A push-notification target. [credential] is write-only: null keeps the saved token, "" removes it. */
@Serializable
data class NotificationSettingsDto(
    val id: String,
    val type: String,
    val url: String,
    @SerialName("on_completed") val onCompleted: Boolean,
    @SerialName("on_failed") val onFailed: Boolean,
    @SerialName("has_credential") val hasCredential: Boolean = false,
    val credential: String? = null,
) {
    companion object {
        const val TYPE_NTFY = "ntfy"
        const val TYPE_WEBHOOK = "webhook"
    }
}

@Serializable
data class NotificationTestRequest(val id: String)

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
