package dev.istarvin.fetcharr.data.network.dto

import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/** Server §7. Server to client only. */
sealed interface WsEvent {
    /** Every job not in completed/failed/cancelled. Sent right after connecting. */
    data class Snapshot(val jobs: List<JobDto>) : WsEvent

    data class JobAdded(val job: JobDto) : WsEvent

    /** Full job object. */
    data class JobUpdated(val job: JobDto) : WsEvent

    data class JobRemoved(val jobIds: List<String>) : WsEvent

    /** A well-formed message with a `type` this client does not know. Ignore it. */
    data class Unknown(val type: String) : WsEvent

    companion object {
        /** @throws SerializationException when [text] is not a JSON object with a string `type`, or the payload is malformed. */
        fun parse(json: Json, text: String): WsEvent {
            val obj: JsonObject = try {
                json.parseToJsonElement(text).jsonObject
            } catch (e: IllegalArgumentException) {
                throw SerializationException("WebSocket message is not a JSON object", e)
            }
            val type = try {
                obj["type"]?.jsonPrimitive?.takeIf { it.isString }?.content
            } catch (e: IllegalArgumentException) {
                null
            } ?: throw SerializationException("WebSocket message has no type")
            return when (type) {
                "snapshot" -> Snapshot(json.decodeFromJsonElement(obj.require("jobs")))
                "job_added" -> JobAdded(json.decodeFromJsonElement(obj.require("job")))
                "job_updated" -> JobUpdated(json.decodeFromJsonElement(obj.require("job")))
                "job_removed" -> JobRemoved(json.decodeFromJsonElement(obj.require("job_ids")))
                else -> Unknown(type)
            }
        }

        /** Like [parse] but returns null instead of throwing. */
        fun parseOrNull(json: Json, text: String): WsEvent? =
            try {
                parse(json, text)
            } catch (e: SerializationException) {
                null
            }

        private fun JsonObject.require(key: String) =
            this[key] ?: throw SerializationException("WebSocket message is missing '$key'")
    }
}
