package dev.istarvin.fetcharr.data.network.dto

import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder

/** RFC 3339 helpers (decision D5: UTC, millisecond precision on the wire). */
object Rfc3339 {
    private val formatter: DateTimeFormatter =
        DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'").withZone(ZoneOffset.UTC)

    /** Parses with or without fractional seconds, `Z` or a numeric offset. Returns epoch millis. */
    fun parse(text: String): Long =
        try {
            OffsetDateTime.parse(text).toInstant().toEpochMilli()
        } catch (e: java.time.DateTimeException) {
            throw SerializationException("Invalid RFC 3339 timestamp: $text", e)
        }

    /** Formats epoch millis as `2026-10-14T14:30:00.123Z`. */
    fun format(epochMillis: Long): String = formatter.format(Instant.ofEpochMilli(epochMillis))
}

/** Serializes an RFC 3339 string as epoch milliseconds. Use on `Long` or `Long?` properties. */
object EpochMillisSerializer : KSerializer<Long> {
    override val descriptor: SerialDescriptor =
        PrimitiveSerialDescriptor("dev.istarvin.fetcharr.EpochMillis", PrimitiveKind.STRING)

    override fun deserialize(decoder: Decoder): Long = Rfc3339.parse(decoder.decodeString())

    override fun serialize(encoder: Encoder, value: Long) {
        encoder.encodeString(Rfc3339.format(value))
    }
}
