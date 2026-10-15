package com.istarvin.fetcharr.data.sync

/** State of the live-update WebSocket (app spec §3.3). */
sealed interface ConnectionState {
    /** No server saved, so there is nothing to connect to. */
    data object NotConfigured : ConnectionState

    data object Connecting : ConnectionState

    data object Connected : ConnectionState

    /**
     * Not connected. [retryInSeconds] is the time left until the next attempt, or null when no retry is
     * scheduled (nobody wants the connection right now).
     */
    data class Disconnected(val retryInSeconds: Int?) : ConnectionState
}
