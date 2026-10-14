package com.istarvin.fetcharr.ui.add

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import com.istarvin.fetcharr.data.CategoryRepository
import com.istarvin.fetcharr.data.network.dto.SubtitleDto
import com.istarvin.fetcharr.data.settings.SettingsRepository
import com.istarvin.fetcharr.incoming.CategoryOption
import com.istarvin.fetcharr.incoming.CategoryPreselector
import com.istarvin.fetcharr.incoming.IncomingRequest
import com.istarvin.fetcharr.notifications.SubmitListener
import com.istarvin.fetcharr.ui.incoming.IncomingHub
import com.istarvin.fetcharr.ui.incoming.ItemForm
import com.istarvin.fetcharr.ui.incoming.SubmitOutcome
import com.istarvin.fetcharr.ui.incoming.SubmitUseCase
import com.istarvin.fetcharr.ui.incoming.isHttpUrl
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class HeaderRow(val id: Int, val name: String = "", val value: String = "")

data class SubtitleRow(val id: Int, val url: String = "", val lang: String = "")

/** Header values are held here, in memory only, until the screen is left. */
data class AddUiState(
    val url: String = "",
    val settingsLoaded: Boolean = false,
    val serverConfigured: Boolean = false,
    val categories: List<CategoryOption> = emptyList(),
    val categoryId: String? = null,
    val form: ItemForm = ItemForm(),
    val headers: List<HeaderRow> = emptyList(),
    val subtitles: List<SubtitleRow> = emptyList(),
    val submitting: Boolean = false,
    val error: String? = null,
) {
    val urlValid: Boolean get() = isHttpUrl(url)

    /** Shown as a field error once the user typed something. */
    val urlInvalid: Boolean get() = url.isNotBlank() && !urlValid

    val layout: String? get() = categories.firstOrNull { it.id == categoryId }?.layout

    /** Subtitle rows with a URL that isn't http(s). Rows with a blank URL are ignored. */
    val subtitleInvalid: Boolean get() = subtitles.any { it.url.isNotBlank() && !isHttpUrl(it.url) }

    val canSubmit: Boolean
        get() = serverConfigured && !submitting && urlValid && !subtitleInvalid &&
            layout?.let { form.isCompleteFor(it) } == true
}

sealed interface AddEvent {
    /** The job was created; pop back (the snackbar goes through [IncomingHub]). */
    data object Done : AddEvent
}

@HiltViewModel
class AddViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val hub: IncomingHub,
    settings: SettingsRepository,
    private val categoryRepository: CategoryRepository,
    private val submit: SubmitUseCase,
    private val submitListener: SubmitListener,
) : ViewModel() {

    // Type-safe navigation stores AddRoute's arguments in the SavedStateHandle under their property names.
    private val _uiState = MutableStateFlow(AddUiState(url = savedStateHandle.get<String>("url").orEmpty()))
    val uiState: StateFlow<AddUiState> = _uiState.asStateFlow()

    private val eventChannel = Channel<AddEvent>(Channel.BUFFERED)
    val events: Flow<AddEvent> = eventChannel.receiveAsFlow()

    private var nextRowId = 1
    private var refreshed = false

    init {
        viewModelScope.launch {
            hub.addUrls.collect { url -> onUrl(url) }
        }
        viewModelScope.launch {
            settings.settings.collect { app ->
                val categories = app.cachedCategories.map { CategoryOption(it.id, it.label, it.layout) }
                _uiState.update { cur ->
                    var next = cur.copy(
                        settingsLoaded = true,
                        serverConfigured = app.serverUrl.isNotBlank(),
                        categories = categories,
                    )
                    if (categories.none { it.id == next.categoryId }) {
                        next = next.copy(categoryId = CategoryPreselector.preselect(
                            MANUAL_REQUEST, categories, app.defaultMovieCategoryId, app.defaultTvCategoryId,
                        ))
                    }
                    next
                }
                if (app.serverUrl.isNotBlank() && categories.isEmpty() && !refreshed) {
                    refreshed = true
                    launch { categoryRepository.refresh() }
                }
            }
        }
    }

    // ---- edits -----------------------------------------------------------------------------

    private fun edit(transform: (AddUiState) -> AddUiState) = _uiState.update { transform(it).copy(error = null) }

    private fun editForm(transform: (ItemForm) -> ItemForm) = edit { it.copy(form = transform(it.form)) }

    fun onUrl(v: String) = edit { it.copy(url = v) }

    fun selectCategory(id: String) = edit { it.copy(categoryId = id) }

    fun onTitle(v: String) = editForm { it.copy(title = v) }

    fun onYear(v: String) = editForm { it.copy(year = v.filter(Char::isDigit).take(4)) }

    fun onShow(v: String) = editForm { it.copy(show = v) }

    fun onSeason(v: String) = editForm { it.copy(season = v.filter(Char::isDigit).take(4)) }

    fun onEpisode(v: String) = editForm { it.copy(episode = v.filter(Char::isDigit).take(4)) }

    fun onEpisodeTitle(v: String) = editForm { it.copy(episodeTitle = v) }

    fun addHeader() = edit { it.copy(headers = it.headers + HeaderRow(nextRowId++)) }

    fun updateHeader(id: Int, name: String? = null, value: String? = null) = edit { state ->
        state.copy(headers = state.headers.map {
            if (it.id == id) it.copy(name = name ?: it.name, value = value ?: it.value) else it
        })
    }

    fun removeHeader(id: Int) = edit { state -> state.copy(headers = state.headers.filterNot { it.id == id }) }

    fun addSubtitle() = edit { it.copy(subtitles = it.subtitles + SubtitleRow(nextRowId++)) }

    fun updateSubtitle(id: Int, url: String? = null, lang: String? = null) = edit { state ->
        state.copy(subtitles = state.subtitles.map {
            if (it.id == id) it.copy(url = url ?: it.url, lang = lang ?: it.lang) else it
        })
    }

    fun removeSubtitle(id: Int) = edit { state -> state.copy(subtitles = state.subtitles.filterNot { it.id == id }) }

    // ---- submit ----------------------------------------------------------------------------

    fun submit() {
        val state = _uiState.value
        if (!state.canSubmit) return
        val categoryId = state.categoryId ?: return
        _uiState.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            val outcome = submit.submitJob(
                url = state.url,
                category = categoryId,
                layout = state.layout.orEmpty(),
                form = state.form,
                headers = state.headers.filter { it.name.isNotBlank() }.associate { it.name.trim() to it.value },
                subtitles = state.subtitles.filter { it.url.isNotBlank() }.map { SubtitleDto(it.url, it.lang) },
            )
            when (outcome) {
                is SubmitOutcome.Success -> {
                    submitListener.onSubmitSucceeded()
                    hub.showMessage(outcome.message)
                    eventChannel.trySend(AddEvent.Done)
                }
                // Add keeps the form on a duplicate so the URL can be fixed, unlike the Incoming sheet.
                SubmitOutcome.DuplicateSingle -> failWith(SubmitOutcome.MESSAGE_DUPLICATE)
                is SubmitOutcome.Error -> failWith(outcome.message)
                SubmitOutcome.NotConfigured -> failWith(SubmitOutcome.MESSAGE_NOT_CONFIGURED)
            }
        }
    }

    private fun failWith(message: String) = _uiState.update { it.copy(submitting = false, error = message) }

    private companion object {
        /** A plain, non-episodic request: preselection falls to the default movie category, then the first fitting. */
        val MANUAL_REQUEST = IncomingRequest.Single(null, "", "", null, null, null, null, emptyMap(), emptyList())
    }
}
