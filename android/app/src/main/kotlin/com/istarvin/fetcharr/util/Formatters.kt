package com.istarvin.fetcharr.util

import java.util.Locale

/** Display formatting for server numbers (app spec §3.4). Null in, null out: missing values are hidden. */
object Formatters {
    private val units = arrayOf("B", "KiB", "MiB", "GiB", "TiB")

    /** Binary units with one decimal, e.g. `3.1 MiB`. Whole bytes below 1 KiB, e.g. `512 B`. */
    fun bytes(value: Long?): String? {
        if (value == null || value < 0) return null
        if (value < 1024) return "$value B"
        var v = value.toDouble()
        var unit = 0
        while (v >= 1024 && unit < units.lastIndex) {
            v /= 1024
            unit++
        }
        // 1023.96 KiB would round to "1024.0 KiB"; carry it to the next unit.
        if (Math.round(v * 10) >= 10240 && unit < units.lastIndex) {
            v /= 1024
            unit++
        }
        return String.format(Locale.ROOT, "%.1f %s", v, units[unit])
    }

    /** `3.1 MiB/s`. */
    fun speed(bytesPerSecond: Long?): String? = bytes(bytesPerSecond)?.let { "$it/s" }

    /** `h:mm:ss` from one hour up, otherwise `m:ss`. */
    fun eta(seconds: Long?): String? {
        if (seconds == null || seconds < 0) return null
        val h = seconds / 3600
        val m = (seconds % 3600) / 60
        val s = seconds % 60
        return if (h > 0) {
            String.format(Locale.ROOT, "%d:%02d:%02d", h, m, s)
        } else {
            String.format(Locale.ROOT, "%d:%02d", m, s)
        }
    }
}
