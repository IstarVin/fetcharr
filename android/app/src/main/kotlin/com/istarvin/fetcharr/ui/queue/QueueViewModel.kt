package com.istarvin.fetcharr.ui.queue

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import com.istarvin.fetcharr.data.CategoryRepository
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.settings.SettingsRepository
import com.istarvin.fetcharr.data.sync.ActionOutcome
import com.istarvin.fetcharr.data.sync.ActiveJobsStore
import com.istarvin.fetcharr.data.sync.ConnectionState
import com.istarvin.fetcharr.data.sync.JobActions
import com.istarvin.fetcharr.data.sync.SyncManager
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import javax.inject.Inject

/** A cancel the user has asked for and still has to confirm. */
sealed interface CancelRequest {
    /** Name shown in the dialog. */
    val name: String

    data class SingleJob(val jobId: String, override val name: String) : CancelRequest

    data class WholeBatch(val batchId: String, override val name: String, val count: Int) : CancelRequest
}

data class QueueUiState(
    /** False until the saved settings have been read. */
    val loaded: Boolean = false,
    val hasServer: Boolean = false,
    val sections: List<QueueSection> = emptyList(),
    /** True once the server has sent the first snapshot, so "empty" can be told from "not loaded yet". */
    val hasSnapshot: Boolean = false,
    val banner: QueueBanner? = null,
    val isRefreshing: Boolean = false,
    val connecting: Boolean = false,
    val pendingCancel: CancelRequest? = null,
)

/** One-shot UI events. */
sealed interface QueueEvent {
    /** @param openSettings offer a "Settings" action (wrong token). */
    data class Snackbar(val message: String, val openSettings: Boolean = false) : QueueEvent
}

@HiltViewModel
class QueueViewModel @Inject constructor(
    settings: SettingsRepository,
    store: ActiveJobsStore,
    private val categoryRepository: CategoryRepository,
    private val syncManager: SyncManager,
    private val actions: JobActions,
) : ViewModel() {

    private val refreshing = MutableStateFlow(false)
    private val pendingCancel = MutableStateFlow<CancelRequest?>(null)
    private val eventChannel = Channel<QueueEvent>(Channel.BUFFERED)
    private val inFlight = mutableSetOf<String>()

    val events: Flow<QueueEvent> = eventChannel.receiveAsFlow()

    private val content = combine(
        settings.settings.map { it.serverUrl.isNotBlank() }.distinctUntilChanged(),
        store.jobs,
        categoryRepository.categories,
    ) { hasServer, jobs, categories -> hasServer to buildQueueSections(jobs, categories) }

    val state: StateFlow<QueueUiState> = combine(
        content,
        store.hasSnapshot,
        syncManager.state,
        refreshing,
        pendingCancel,
    ) { (hasServer, sections), hasSnapshot, connection, isRefreshing, cancel ->
        QueueUiState(
            loaded = true,
            hasServer = hasServer,
            sections = sections,
            hasSnapshot = hasSnapshot,
            banner = connectionBanner(connection),
            isRefreshing = isRefreshing,
            connecting = connection == ConnectionState.Connecting,
            pendingCancel = cancel,
        )
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), QueueUiState())

    fun pause(jobId: String) = run("pause:$jobId") { actions.pause(jobId) }

    fun resume(jobId: String) = run("resume:$jobId") { actions.resume(jobId) }

    fun pauseAll(batchId: String) = run("pauseAll:$batchId") { actions.pauseAll(batchId) }

    fun resumeAll(batchId: String) = run("resumeAll:$batchId") { actions.resumeAll(batchId) }

    /** Asks for confirmation; nothing is sent until [confirmCancel]. */
    fun requestCancel(job: JobCardModel) {
        pendingCancel.value = CancelRequest.SingleJob(job.id, job.heading)
    }

    fun requestCancelAll(group: BatchGroup) {
        pendingCancel.value = CancelRequest.WholeBatch(group.batchId, group.title, group.episodeCount)
    }

    fun dismissCancel() {
        pendingCancel.value = null
    }

    fun confirmCancel() {
        val request = pendingCancel.value ?: return
        pendingCancel.value = null
        when (request) {
            is CancelRequest.SingleJob -> run("cancel:${request.jobId}") { actions.cancel(request.jobId) }
            is CancelRequest.WholeBatch -> run("cancelAll:${request.batchId}") { actions.cancelAll(request.batchId) }
        }
    }

    /** Pull-to-refresh: reconnect the WebSocket and reload categories. */
    fun refresh() {
        if (refreshing.value) return
        refreshing.value = true
        syncManager.reconnectNow()
        viewModelScope.launch { categoryRepository.refresh() }
        viewModelScope.launch {
            // Done once the socket has come back up, or after a short timeout if it never does.
            withTimeoutOrNull(REFRESH_TIMEOUT_MS) {
                var restarted = false
                syncManager.state.first { s ->
                    if (s != ConnectionState.Connected) restarted = true
                    s == ConnectionState.NotConfigured || (restarted && s == ConnectionState.Connected)
                }
            }
            refreshing.value = false
        }
    }

    private fun run(key: String, call: suspend () -> ActionOutcome) {
        if (!inFlight.add(key)) return
        viewModelScope.launch {
            try {
                handle(call())
            } finally {
                inFlight.remove(key)
            }
        }
    }

    private fun handle(outcome: ActionOutcome) {
        val event = when (outcome) {
            ActionOutcome.Ok -> null
            is ActionOutcome.Changed -> QueueEvent.Snackbar(outcome.message)
            is ActionOutcome.Failed ->
                QueueEvent.Snackbar(outcome.message, openSettings = outcome.message == ApiError.Unauthorized.userMessage)
            ActionOutcome.NotConfigured -> QueueEvent.Snackbar("Server not set up", openSettings = true)
        }
        if (event != null) eventChannel.trySend(event)
    }

    private companion object {
        const val REFRESH_TIMEOUT_MS = 5_000L
    }
}
