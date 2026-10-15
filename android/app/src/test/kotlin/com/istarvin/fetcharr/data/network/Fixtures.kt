package com.istarvin.fetcharr.data.network

internal object Fixtures {
    fun text(name: String): String {
        val stream = checkNotNull(Fixtures::class.java.classLoader!!.getResourceAsStream(name)) {
            "Fixture $name is not on the test classpath"
        }
        return stream.use { it.readBytes().toString(Charsets.UTF_8) }
    }

    val errorCodes = listOf(
        "invalid_request", "unauthorized", "not_found", "invalid_state",
        "duplicate", "ytdlp_unmanaged", "ytdlp_busy",
    )
}
