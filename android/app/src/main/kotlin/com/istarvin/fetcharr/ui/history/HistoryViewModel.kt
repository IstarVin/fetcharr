package com.istarvin.fetcharr.ui.history

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import com.istarvin.fetcharr.data.CategoryRepository
import com.istarvin.fetcharr.data.history.HistoryRepository
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.FetcharrApiProvider
import com.istarvin.fetcharr.data.network.FetcharrApiSession
import com.istarvin.fetcharr.data.network.ServerConfigProvider
import com.istarvin.fetcharr.data.network.safeApiCall
import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.data.network.dto.JobStatus
import com.istarvin.fetcharr.data.sync.ActionOutcome
import com.istarvin.fetcharr.data.sync.JobActions
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import okhttp3.HttpUrl.Companion.toHttpUrl
import java.time.ZoneId
import javax.inject.Inject

/** A confirmation or detail dialog. At most one is open. */
sealed interface HistoryDialog {
    /** Confirm deleting one job. [deleteFile] is only offered (and only sent) for completed jobs. */
    data class Delete(
        val jobId: String,
        val name: String,
        val canDeleteFile: Boolean,
        val deleteFile: Boolean = false,
    ) : HistoryDialog

    /** The retry hit `already_exists`: ask before sending `{"overwrite": true}`. */
    data class ConfirmReplace(val jobId: String, val name: String) : HistoryDialog

    data class Details(val name: String, val message: String) : HistoryDialog

    /** Confirm clearing all history (files are kept). */
    data object ClearAll : HistoryDialog
}

data class HistoryUiState(
    /** False until the saved settings have been read. */
    val loaded: Boolean = false,
    val hasServer: Boolean = false,
    val groups: List<HistoryGroup> = emptyList(),
    /** True once the first page has arrived, so "empty" can be told from "not loaded yet". */
    val historyLoaded: Boolean = false,
    val isRefreshing: Boolean = false,
    val loadingMore: Boolean = false,
    /** The last load error. Shown inline while nothing has loaded yet. */
    val error: ApiError? = null,
    val dialog: HistoryDialog? = null,
    val preparingPlayback: Set<String> = emptySet(),
) {
    val isEmpty: Boolean get() = historyLoaded && groups.isEmpty()
}

/** One-shot UI events. */
sealed interface HistoryEvent {
    /** @param openSettings offer a "Settings" action (wrong token or no server). */
    data class Snackbar(val message: String, val openSettings: Boolean = false) : HistoryEvent

    /** The screen puts [path] on the clipboard. */
    data class CopyPath(val path: String) : HistoryEvent

    /** Checked again on delivery so a buffered event cannot open a previous server's media. */
    data class OpenPlayer(val url: String, val mimeType: String, val title: String, val session: FetcharrApiSession) : HistoryEvent
}

@HiltViewModel
class HistoryViewModel @Inject constructor(
    configProvider: ServerConfigProvider,
    private val history: HistoryRepository,
    private val actions: JobActions,
    categoryRepository: CategoryRepository,
    private val apiProvider: FetcharrApiProvider,
) : ViewModel() {

    private val categories: StateFlow<List<CategoryDto>> =
        categoryRepository.categories.stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

    private val refreshing = MutableStateFlow(false)
    private val dialog = MutableStateFlow<HistoryDialog?>(null)
    private val preparingPlayback = MutableStateFlow<Set<String>>(emptySet())
    private val eventChannel = Channel<HistoryEvent>(Channel.BUFFERED)
    private val inFlight = mutableSetOf<String>()

    val events: Flow<HistoryEvent> = eventChannel.receiveAsFlow()

    private val content = combine(
        configProvider.config.map { it != null }.distinctUntilChanged(),
        history.state,
        categories,
    ) { hasServer, state, categories ->
        Triple(hasServer, state, buildHistoryGroups(state.items, categories, System.currentTimeMillis(), ZoneId.systemDefault()))
    }

    val state: StateFlow<HistoryUiState> = combine(content, refreshing, dialog, preparingPlayback) { (hasServer, repo, groups), isRefreshing, open, preparing ->
        HistoryUiState(
            loaded = true,
            hasServer = hasServer,
            groups = groups,
            historyLoaded = repo.loaded,
            isRefreshing = isRefreshing,
            loadingMore = repo.loadingMore,
            error = repo.error,
            dialog = open,
            preparingPlayback = preparing,
        )
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), HistoryUiState())

    init {
        // First visit, and again whenever the server is set up or changed (the repository drops the old server's rows).
        viewModelScope.launch {
            configProvider.config.map { it?.baseUrl }.distinctUntilChanged().collect { url ->
                if (url != null && !history.state.value.loaded) load(showErrors = false)
            }
        }
    }

    /** Pull-to-refresh (or the inline Retry button): reloads the first page. */
    fun refresh() {
        if (refreshing.value) return
        refreshing.value = true
        viewModelScope.launch {
            try {
                load(showErrors = true)
            } finally {
                refreshing.value = false
            }
        }
    }

    /** Called when the list is scrolled near its end. */
    fun loadMore() {
        viewModelScope.launch { history.loadMore()?.let { snackbar(it) } }
    }

    // --- Row actions -------------------------------------------------------------------------------------------

    fun play(jobId: String) {
        val job = rowFor(jobId)?.takeIf { HistoryAction.Play in it.actions } ?: return
        val session = apiProvider.captureSession() ?: run {
            eventChannel.trySend(HistoryEvent.Snackbar("Server not set up", openSettings = true))
            return
        }
        if (!inFlight.add("play:$jobId")) return
        preparingPlayback.update { it + jobId }
        viewModelScope.launch {
            try {
                val result = safeApiCall {
                    val playback = session.api.createPlayback(jobId)
                    val base = (session.config.baseUrl.trimEnd('/') + "/api/").toHttpUrl()
                    val url = requireNotNull(base.resolve(playback.url))
                    val expected = base.newBuilder().addPathSegment("jobs").addPathSegment(jobId).addPathSegment("media").build()
                    require(url.scheme == base.scheme && url.host == base.host && url.port == base.port && url.encodedPath == expected.encodedPath)
                    val mime = playback.mimeType.takeIf { it.startsWith("video/") || it.startsWith("audio/") } ?: "video/*"
                    HistoryEvent.OpenPlayer(url.toString(), mime, job.heading, session)
                }
                apiProvider.withCurrentSession(session) {
                    when (result) {
                        is ApiResult.Success -> eventChannel.trySend(result.value)
                        is ApiResult.Failure -> snackbar(result.error)
                    }
                }
            } finally {
                inFlight.remove("play:$jobId")
                preparingPlayback.update { it - jobId }
            }
        }
    }

    fun isCurrentPlayback(event: HistoryEvent.OpenPlayer): Boolean = apiProvider.isCurrent(event.session)

    /** Retries straight away, except for `already_exists`, which asks about replacing the file first. */
    fun retry(jobId: String) {
        val job = rowFor(jobId) ?: return
        if (job.status == JobStatus.FAILED && job.errorCode == ALREADY_EXISTS) {
            dialog.value = HistoryDialog.ConfirmReplace(jobId, job.heading)
        } else {
            runRetry(jobId, overwrite = false)
        }
    }

    fun confirmReplace() {
        val open = dialog.value as? HistoryDialog.ConfirmReplace ?: return
        dialog.value = null
        runRetry(open.jobId, overwrite = true)
    }

    fun requestDelete(jobId: String) {
        val job = rowFor(jobId) ?: return
        dialog.value = HistoryDialog.Delete(jobId, job.heading, canDeleteFile = job.status == JobStatus.COMPLETED)
    }

    fun setDeleteFile(deleteFile: Boolean) {
        dialog.value = (dialog.value as? HistoryDialog.Delete)?.takeIf { it.canDeleteFile }?.copy(deleteFile = deleteFile) ?: return
    }

    fun confirmDelete() {
        val open = dialog.value as? HistoryDialog.Delete ?: return
        dialog.value = null
        val deleteFile = open.canDeleteFile && open.deleteFile
        run("delete:${open.jobId}") {
            val outcome = actions.delete(open.jobId, deleteFile)
            if (outcome == ActionOutcome.Ok) history.remove(open.jobId)
            outcome
        }
    }

    fun copyPath(jobId: String) {
        val path = rowFor(jobId)?.filePath?.takeIf { it.isNotBlank() } ?: return
        eventChannel.trySend(HistoryEvent.CopyPath(path))
    }

    fun showDetails(jobId: String) {
        val job = rowFor(jobId) ?: return
        val message = job.detailsText ?: return
        dialog.value = HistoryDialog.Details(job.heading, message)
    }

    // --- Clear history -----------------------------------------------------------------------------------------

    fun requestClear() {
        dialog.value = HistoryDialog.ClearAll
    }

    fun confirmClear() {
        if (dialog.value != HistoryDialog.ClearAll) return
        dialog.value = null
        run("clear") {
            val outcome = actions.clearHistory()
            if (outcome == ActionOutcome.Ok) {
                history.clear()
                history.refresh() // pick up anything finished meanwhile; a failure here is not worth a message
            }
            outcome
        }
    }

    fun dismissDialog() {
        dialog.value = null
    }

    // --- Internals ---------------------------------------------------------------------------------------------

    /** The row for [jobId] from the loaded history, or null if it is gone. */
    private fun rowFor(jobId: String): HistoryRowModel? {
        val job = history.state.value.items.firstOrNull { it.id == jobId } ?: return null
        val category = categories.value.firstOrNull { it.id == job.category }
        return job.toHistoryRow(category?.label ?: job.category, category?.layout)
    }

    private suspend fun load(showErrors: Boolean) {
        val error = history.refresh()
        if (error != null && showErrors && history.state.value.loaded) snackbar(error)
    }

    private fun runRetry(jobId: String, overwrite: Boolean) {
        run("retry:$jobId") {
            val outcome = actions.retry(jobId, overwrite)
            // Retried: the job is active again. Changed: it moved on without us, so the list is stale either way.
            if (outcome == ActionOutcome.Ok || outcome is ActionOutcome.Changed) history.remove(jobId)
            outcome
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

    private suspend fun handle(outcome: ActionOutcome) {
        when (outcome) {
            ActionOutcome.Ok -> Unit
            is ActionOutcome.Changed -> {
                eventChannel.trySend(HistoryEvent.Snackbar(outcome.message))
                history.refresh()
            }
            is ActionOutcome.Failed ->
                eventChannel.trySend(HistoryEvent.Snackbar(outcome.message, openSettings = outcome.message == ApiError.Unauthorized.userMessage))
            ActionOutcome.NotConfigured -> eventChannel.trySend(HistoryEvent.Snackbar("Server not set up", openSettings = true))
        }
    }

    private fun snackbar(error: ApiError) {
        eventChannel.trySend(HistoryEvent.Snackbar(error.userMessage, openSettings = error == ApiError.Unauthorized))
    }

    private companion object {
        const val ALREADY_EXISTS = "already_exists"
    }
}
