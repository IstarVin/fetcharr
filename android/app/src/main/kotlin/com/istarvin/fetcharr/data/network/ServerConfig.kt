package com.istarvin.fetcharr.data.network

import kotlinx.coroutines.flow.StateFlow

/**
 * Where the Fetcharr server lives.
 *
 * @property baseUrl like `http://host:8080`, no trailing slash, no `/api`.
 * @property token Bearer token, or null/blank when the server needs none.
 */
data class ServerConfig(val baseUrl: String, val token: String?) {
    /** The token is never printed. */
    override fun toString(): String =
        "ServerConfig(baseUrl=$baseUrl, token=${if (token.isNullOrBlank()) "none" else "***"})"
}

/** Source of the saved server settings. Implemented by the settings repository. */
interface ServerConfigProvider {
    /** Null until a server URL has been saved. */
    val config: StateFlow<ServerConfig?>
}
