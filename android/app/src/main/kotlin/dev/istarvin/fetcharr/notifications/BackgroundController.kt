package dev.istarvin.fetcharr.notifications

import dev.istarvin.fetcharr.data.network.dto.JobDto
import dev.istarvin.fetcharr.data.sync.ActiveJobsStore
import dev.istarvin.fetcharr.data.sync.ConnectionState
import dev.istarvin.fetcharr.data.sync.SyncManager
import dev.istarvin.fetcharr.di.ApplicationScope
import dev.istarvin.fetcharr.notifications.logic.ForegroundEvent
import dev.istarvin.fetcharr.notifications.logic.ForegroundPolicy
import dev.istarvin.fetcharr.notifications.logic.ForegroundState
import dev.istarvin.fetcharr.notifications.logic.ServiceCommand
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Drives the foreground service with [ForegroundPolicy]. All events go through one queue and one consumer, so the
 * policy state is only touched from a single coroutine. Android calls go through [ServiceStarter] and
 * [StoppedNoticePoster].
 *
 * While the service should run it sends a [ForegroundEvent.Tick] every [TICK_MS], which lets the policy notice that
 * the server has been unreachable for 15 minutes.
 */
@Singleton
class BackgroundController internal constructor(
    private val store: ActiveJobsStore,
    private val connection: StateFlow<ConnectionState>,
    private val starter: ServiceStarter,
    private val notices: StoppedNoticePoster,
    private val time: TimeSource,
    private val scope: CoroutineScope,
) : SubmitListener {

    @Inject
    constructor(
        store: ActiveJobsStore,
        syncManager: SyncManager,
        starter: ServiceStarter,
        notices: StoppedNoticePoster,
        time: TimeSource,
        @ApplicationScope scope: CoroutineScope,
    ) : this(store, syncManager.state, starter, notices, time, scope)

    private val events = Channel<ForegroundEvent>(Channel.UNLIMITED)
    private val _submitSucceeded = MutableSharedFlow<Unit>(extraBufferCapacity = 1, onBufferOverflow = BufferOverflow.DROP_OLDEST)

    /** Emits after every successful submit, for the one-time notification permission prompt. */
    val submitSucceeded: SharedFlow<Unit> = _submitSucceeded.asSharedFlow()

    private val _shouldRun = MutableStateFlow(false)

    /**
     * Whether the service should be up. The service reads this after it has called `startForeground` and stops
     * itself when it turns false. The controller never calls `stopService`: stopping a service that was started with
     * `startForegroundService` before it called `startForeground` crashes the app.
     */
    val shouldRun: StateFlow<Boolean> = _shouldRun.asStateFlow()

    private var started = false
    private var state = ForegroundState()
    private var ticker: Job? = null

    /** Call once from `Application.onCreate`. */
    fun start() {
        if (started) return
        started = true
        scope.launch {
            for (event in events) handle(event)
        }
        scope.launch {
            // Jobs only count once the first snapshot has arrived: before it the empty list is "unknown", not "idle".
            // When the snapshot is lost again (server changed or removed) the list is reported as empty.
            var sawSnapshot = false
            combine(store.hasSnapshot, store.jobs) { has, jobs -> has to jobs }.collect { (has, jobs) ->
                when {
                    has -> {
                        sawSnapshot = true
                        events.trySend(ForegroundEvent.ActiveJobsChanged(jobs))
                    }
                    sawSnapshot -> {
                        sawSnapshot = false
                        events.trySend(ForegroundEvent.ActiveJobsChanged(emptyList<JobDto>()))
                    }
                }
            }
        }
        scope.launch {
            connection.collect { connectionState ->
                // Not connected while somebody wants a connection counts as unreachable. "Nobody wants it" (no
                // holder, or no server) says nothing about the server and stops the clock.
                val event = when (connectionState) {
                    ConnectionState.Connected -> ForegroundEvent.ConnectionChanged(true, time.nowMs())
                    ConnectionState.Connecting -> ForegroundEvent.ConnectionChanged(false, time.nowMs())
                    is ConnectionState.Disconnected ->
                        if (connectionState.retryInSeconds == null) {
                            ForegroundEvent.ConnectionNotWanted
                        } else {
                            ForegroundEvent.ConnectionChanged(false, time.nowMs())
                        }
                    ConnectionState.NotConfigured -> ForegroundEvent.ConnectionNotWanted
                }
                events.trySend(event)
            }
        }
    }

    override fun onSubmitSucceeded() {
        events.trySend(ForegroundEvent.SubmitSucceeded(time.nowMs()))
        _submitSucceeded.tryEmit(Unit)
    }

    /** `MainActivity.onStart`: the user is looking at the app, so a notice-stop no longer blocks restarts. */
    fun onAppForegrounded() {
        events.trySend(ForegroundEvent.AppForegrounded)
    }

    /** `Service.onTimeout`, or a refused foreground start. */
    fun onSystemTimeout() {
        events.trySend(ForegroundEvent.SystemTimeout)
    }

    private fun handle(event: ForegroundEvent) {
        val (next, command) = ForegroundPolicy.reduce(state, event)
        state = next
        when (command) {
            ServiceCommand.Start -> {
                _shouldRun.value = true
                if (starter.start()) {
                    startTicker()
                } else {
                    // The system refused (background start restriction). Not running, so a later event retries.
                    _shouldRun.value = false
                    state = state.copy(running = false)
                }
            }
            ServiceCommand.KeepRunning -> _shouldRun.value = true
            ServiceCommand.Idle -> Unit
            is ServiceCommand.Stop -> {
                stopTicker()
                _shouldRun.value = false
                if (command.postStoppedNotice) notices.postStoppedNotice(ForegroundPolicy.STOPPED_NOTICE)
            }
        }
    }

    private fun startTicker() {
        if (ticker?.isActive == true) return
        ticker = scope.launch {
            while (isActive) {
                delay(TICK_MS)
                events.trySend(ForegroundEvent.Tick(time.nowMs()))
            }
        }
    }

    private fun stopTicker() {
        ticker?.cancel()
        ticker = null
    }

    companion object {
        const val TICK_MS = 30_000L
    }
}
