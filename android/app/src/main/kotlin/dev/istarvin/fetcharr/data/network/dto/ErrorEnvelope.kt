package dev.istarvin.fetcharr.data.network.dto

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/** `{ "error": { "code": "...", "message": "..." } }` (server §6.1). */
@Serializable
data class ErrorEnvelope(val error: ErrorBody)

@Serializable
data class ErrorBody(
    val code: String,
    val message: String = "",
    /** Optional extra payload some errors may carry (e.g. duplicate details). */
    val details: JsonElement? = null,
)
