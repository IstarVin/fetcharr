package com.istarvin.fetcharr.ui.incoming

import com.istarvin.fetcharr.di.ApplicationScope
import com.istarvin.fetcharr.incoming.IncomingCoordinator
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.receiveAsFlow
import javax.inject.Inject
import javax.inject.Singleton

/** One-shot things the UI must react to exactly once. */
sealed interface HubEvent {
    data class Snackbar(val message: String) : HubEvent

    /** Navigate to the Add screen, pre-filled with [url] when non-null. */
    data class OpenAdd(val url: String?) : HubEvent
}

/**
 * Process-wide entry point for received intents. Owns the single [IncomingCoordinator] (so a request survives
 * activity recreation) and a buffered event channel: events sent before the UI collects (for example from
 * `onCreate`) are delivered once it does. Intent payloads are never logged.
 */
@Singleton
class IncomingHub internal constructor(
    val coordinator: IncomingCoordinator,
    private val router: IntentRouter,
) {
    @Inject
    constructor(@ApplicationScope scope: CoroutineScope) : this(IncomingCoordinator(scope), IntentRouter())

    private val channel = Channel<HubEvent>(Channel.UNLIMITED)

    /** Collect from one place only (the root UI). */
    val events: Flow<HubEvent> = channel.receiveAsFlow()

    private val _addUrls = MutableSharedFlow<String>(extraBufferCapacity = 1)

    /** URLs shared while the Add screen is already open (no replay); the Add screen replaces its URL field. */
    val addUrls: Flow<String> = _addUrls.asSharedFlow()

    fun replaceAddUrl(url: String) {
        _addUrls.tryEmit(url)
    }

    /** Routes the strings extracted from an Android intent (see [IntentRouter.route]). */
    fun handleIntent(action: String?, dataString: String?, extraText: String?, payloadExtra: String?) {
        when (val decision = router.route(action, dataString, extraText, payloadExtra)) {
            is RouteDecision.ShowRequest -> coordinator.onRequest(decision.request)
            RouteDecision.Unreadable -> showMessage(MESSAGE_UNREADABLE)
            is RouteDecision.OpenAdd -> channel.trySend(HubEvent.OpenAdd(decision.url))
            RouteDecision.Ignore -> Unit
        }
    }

    fun showMessage(message: String) {
        channel.trySend(HubEvent.Snackbar(message))
    }

    companion object {
        const val MESSAGE_UNREADABLE = "Couldn't read the download request"
    }
}
