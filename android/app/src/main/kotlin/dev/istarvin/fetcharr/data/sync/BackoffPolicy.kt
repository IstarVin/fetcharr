package dev.istarvin.fetcharr.data.sync

/**
 * Reconnect delays: 1 s, doubling, capped at 30 s (app spec §3.3). Call [reset] after a successful connection.
 * Not thread-safe; [SyncManager] touches it from one coroutine only.
 */
class BackoffPolicy(
    private val initialMillis: Long = 1_000L,
    private val maxMillis: Long = 30_000L,
) {
    private var nextMillis = initialMillis

    /** The delay to wait before the next attempt; advances the schedule. */
    fun next(): Long {
        val current = nextMillis
        nextMillis = (nextMillis * 2).coerceAtMost(maxMillis)
        return current
    }

    fun reset() {
        nextMillis = initialMillis
    }
}
