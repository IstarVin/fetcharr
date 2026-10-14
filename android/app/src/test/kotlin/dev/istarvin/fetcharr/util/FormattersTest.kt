package dev.istarvin.fetcharr.util

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class FormattersTest {
    @Test
    fun bytes() {
        assertEquals("0 B", Formatters.bytes(0))
        assertEquals("1023 B", Formatters.bytes(1023))
        assertEquals("1.0 KiB", Formatters.bytes(1024))
        assertEquals("1.5 KiB", Formatters.bytes(1536))
        assertEquals("3.1 MiB", Formatters.bytes(3_250_585))
        assertEquals("463.0 MiB", Formatters.bytes(485_491_712))
        assertEquals("720.0 MiB", Formatters.bytes(754_974_720))
        assertEquals("5.0 GiB", Formatters.bytes(5_368_709_120))
        assertEquals("1.0 TiB", Formatters.bytes(1L shl 40))
    }

    @Test
    fun bytesCarriesRoundingIntoNextUnit() {
        assertEquals("1.0 MiB", Formatters.bytes(1024L * 1024 - 1))
    }

    @Test
    fun speed() {
        assertEquals("3.1 MiB/s", Formatters.speed(3_250_585))
        assertEquals("0 B/s", Formatters.speed(0))
    }

    @Test
    fun eta() {
        assertEquals("0:00", Formatters.eta(0))
        assertEquals("0:59", Formatters.eta(59))
        assertEquals("2:12", Formatters.eta(132))
        assertEquals("59:59", Formatters.eta(3599))
        assertEquals("1:00:00", Formatters.eta(3600))
        assertEquals("1:02:03", Formatters.eta(3723))
        assertEquals("27:46:40", Formatters.eta(100_000))
    }

    @Test
    fun nullsAndNegativesAreHidden() {
        assertNull(Formatters.bytes(null))
        assertNull(Formatters.speed(null))
        assertNull(Formatters.eta(null))
        assertNull(Formatters.bytes(-1))
        assertNull(Formatters.speed(-5))
        assertNull(Formatters.eta(-1))
    }
}
