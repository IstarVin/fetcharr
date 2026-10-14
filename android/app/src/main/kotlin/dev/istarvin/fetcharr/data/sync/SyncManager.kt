package dev.istarvin.fetcharr.data.sync

import dev.istarvin.fetcharr.data.CategoryRepository
import dev.istarvin.fetcharr.data.network.ServerConfig
import dev.istarvin.fetcharr.data.network.ServerConfigProvider
import dev.istarvin.fetcharr.data.network.dto.WsEvent
import dev.istarvin.fetcharr.di.ApplicationScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import javax.inject.Inject
import javax.inject.Singleton

/** What to do right after the WebSocket opens: refresh `/api/info` (categories). A seam for tests. */
fun interface ConnectionRefresher {
    suspend fun refresh()
}

@Singleton
class CategoryConnectionRefresher @Inject constructor(
    private val categories: CategoryRepository,
) : ConnectionRefresher {
    override suspend fun refresh() {
        categories.refresh()
    }
}

/**
 * Owns the WebSocket (app spec §3.3). It stays connected while somebody holds a reference via [acquire]
 * (the UI while in the foreground, later the background service) and a server is configured. It reconnects with
 * [BackoffPolicy] and feeds [ActiveJobsStore].
 *
 * Config choices: when no server is configured the socket closes, [state] is [ConnectionState.NotConfigured] and the
 * store is cleared. When the server URL changes the store is cleared too (the old server's jobs are meaningless);
 * a token-only change keeps the list until the new snapshot replaces it. A dropped connection keeps the list.
 */
@Singleton
class SyncManager @Inject constructor(
    private val connector: WebSocketConnector,
    private val configProvider: ServerConfigProvider,
    private val store: ActiveJobsStore,
    private val refresher: ConnectionRefresher,
    private val json: Json,
    @ApplicationScope private val scope: CoroutineScope,
) {
    private val _state = MutableStateFlow<ConnectionState>(ConnectionState.Disconnected(null))
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    private val _connected = MutableSharedFlow<Unit>(extraBufferCapacity = 1, onBufferOverflow = BufferOverflow.DROP_OLDEST)

    /** Emits each time a connection opens, including reconnects. A5 uses it for the finished-job catch-up. */
    val connected: SharedFlow<Unit> = _connected.asSharedFlow()

    private val holders = MutableStateFlow<Map<String, Int>>(emptyMap())
    private val generation = MutableStateFlow(0)

    /** Only touched from the collector in `init`, whose blocks never overlap. */
    private var lastUrl: String? = null

    init {
        scope.launch {
            combine(
                holders.map { it.isNotEmpty() }.distinctUntilChanged(),
                configProvider.config,
                generation,
            ) { wanted, config, _ -> wanted to config }
                .collectLatest { (wanted, config) -> runFor(wanted, config) }
        }
    }

    /** Asks for the connection to be up. Each call must be paired with a [release] using the same [reason]. */
    fun acquire(reason: String) {
        holders.update { it + (reason to (it[reason] ?: 0) + 1) }
    }

    fun release(reason: String) {
        holders.update { map ->
            val count = map[reason] ?: return@update map
            if (count <= 1) map - reason else map + (reason to count - 1)
        }
    }

    /** Pull-to-refresh: drops the current socket and reconnects at once with a fresh backoff. */
    fun reconnectNow() {
        generation.update { it + 1 }
    }

    private suspend fun runFor(wanted: Boolean, config: ServerConfig?) {
        // Before connecting, so a stale clear can never wipe the new server's snapshot.
        if (config == null || (lastUrl != null && lastUrl != config.baseUrl)) store.clear()
        lastUrl = config?.baseUrl
        when {
            config == null -> _state.value = ConnectionState.NotConfigured
            !wanted -> _state.value = ConnectionState.Disconnected(null)
            else -> connectLoop(config)
        }
    }

    private sealed interface Event {
        data object Opened : Event
        data class Message(val text: String) : Event
        data object Ended : Event
    }

    /** Runs until cancelled (config/wanted/reconnect change). Closes the socket on the way out. */
    private suspend fun connectLoop(config: ServerConfig) = coroutineScope {
        val backoff = BackoffPolicy()
        while (true) {
            _state.value = ConnectionState.Connecting
            val events = Channel<Event>(Channel.UNLIMITED)
            val listener = object : WebSocketConnector.Listener {
                override fun onOpen() {
                    events.trySend(Event.Opened)
                }

                override fun onMessage(text: String) {
                    events.trySend(Event.Message(text))
                }

                override fun onClosed() {
                    events.trySend(Event.Ended)
                }

                override fun onFailure(error: Throwable) {
                    events.trySend(Event.Ended)
                }
            }
            val handle = try {
                connector.connect(config, listener)
            } catch (e: IllegalArgumentException) {
                events.trySend(Event.Ended)
                null
            }
            try {
                for (event in events) {
                    when (event) {
                        Event.Opened -> {
                            backoff.reset()
                            _state.value = ConnectionState.Connected
                            launch { refreshQuietly() }
                            _connected.tryEmit(Unit)
                        }
                        is Event.Message -> WsEvent.parseOrNull(json, event.text)?.let(store::apply)
                        Event.Ended -> break
                    }
                }
            } finally {
                handle?.close()
                events.close()
            }
            waitForRetry(backoff.next())
        }
    }

    /** Shows the countdown to the next attempt. */
    private suspend fun waitForRetry(delayMillis: Long) {
        var remaining = delayMillis
        while (remaining > 0) {
            _state.value = ConnectionState.Disconnected(((remaining + 999) / 1000).toInt())
            val step = minOf(1_000L, remaining)
            delay(step)
            remaining -= step
        }
    }

    private suspend fun refreshQuietly() {
        try {
            refresher.refresh()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // Categories stay as cached; the next connection tries again.
        }
    }
}
