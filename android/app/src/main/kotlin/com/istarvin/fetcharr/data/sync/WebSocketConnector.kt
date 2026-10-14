package com.istarvin.fetcharr.data.sync

import com.istarvin.fetcharr.data.network.ServerConfig
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.TimeUnit
import javax.inject.Inject
import javax.inject.Singleton

/** Opens the Fetcharr WebSocket. An abstraction so [SyncManager] can be tested without a network. */
interface WebSocketConnector {
    /** Starts connecting and returns at once. Results arrive on [listener], from any thread. */
    fun connect(config: ServerConfig, listener: Listener): Handle

    interface Listener {
        fun onOpen()

        fun onMessage(text: String)

        /** The socket closed (by either side). Not called after [Handle.close]. */
        fun onClosed()

        /** The connection could not be made, or broke. */
        fun onFailure(error: Throwable)
    }

    fun interface Handle {
        /** Closes the socket. Safe to call more than once. */
        fun close()
    }
}

/** `<baseUrl>/api/ws` with `Authorization: Bearer <token>` when a token is set (app spec §3.1). */
@Singleton
class OkHttpWebSocketConnector @Inject constructor(baseClient: OkHttpClient) : WebSocketConnector {
    // No read timeout: the socket is idle between events. Pings detect dead connections.
    private val client: OkHttpClient = baseClient.newBuilder()
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .pingInterval(PING_INTERVAL_SECONDS, TimeUnit.SECONDS)
        .build()

    override fun connect(config: ServerConfig, listener: WebSocketConnector.Listener): WebSocketConnector.Handle {
        val request = try {
            Request.Builder()
                .url(config.baseUrl.trim().trimEnd('/') + "/api/ws")
                .apply {
                    val token = config.token?.trim()
                    if (!token.isNullOrEmpty()) header("Authorization", "Bearer $token")
                }
                .build()
        } catch (e: IllegalArgumentException) {
            listener.onFailure(e)
            return WebSocketConnector.Handle { }
        }
        val socket = client.newWebSocket(
            request,
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) = listener.onOpen()

                override fun onMessage(webSocket: WebSocket, text: String) = listener.onMessage(text)

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, null)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = listener.onClosed()

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = listener.onFailure(t)
            },
        )
        return WebSocketConnector.Handle { socket.close(NORMAL_CLOSURE, null) }
    }

    private companion object {
        const val PING_INTERVAL_SECONDS = 20L
        const val NORMAL_CLOSURE = 1000
    }
}
