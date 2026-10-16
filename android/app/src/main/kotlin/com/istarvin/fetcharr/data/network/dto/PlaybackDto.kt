package com.istarvin.fetcharr.data.network.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** A temporary, job-scoped URL, relative to the API base URL. */
@Serializable
data class PlaybackDto(
    val url: String,
    @SerialName("mime_type") val mimeType: String,
    @SerialName("expires_at") val expiresAt: String,
)
