package com.istarvin.fetcharr.ui.add

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import com.istarvin.fetcharr.data.CategoryRepository
import com.istarvin.fetcharr.data.ServerSettingsRepository
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.data.network.dto.ServerCategoryDto
import com.istarvin.fetcharr.data.network.dto.ServerSettingsDto
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
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.UUID
import javax.inject.Inject

data class HeaderRow(val id: Int, val name: String = "", val value: String = "")

data class SubtitleRow(val id: Int, val url: String = "", val lang: String = "")

/** The "New category" dialog: appended to the server's settings document on save. */
data class NewCategoryDraft(
    val label: String = "",
    val path: String = "",
    val layout: String = CategoryOption.LAYOUT_MOVIE,
    val saving: Boolean = false,
    val showErrors: Boolean = false,
    val error: String? = null,
) {
    val labelInvalid: Boolean get() = label.isBlank()
    val pathInvalid: Boolean get() = !path.trim().startsWith('/') || path.contains('\u0000')
}

/** Header values are held here, in memory only, until the screen is left. */
data class AddUiState(
    val url: String = "",
    val settingsLoaded: Boolean = false,
    val serverConfigured: Boolean = false,
    val categories: List<CategoryOption> = emptyList(),
    val categoriesLoading: Boolean = false,
    val categoryError: String? = null,
    val categoryId: String? = null,
    /** False only once the server reported that it can't be configured from the app. */
    val canAddCategory: Boolean = true,
    val newCategory: NewCategoryDraft? = null,
    val form: ItemForm = ItemForm(),
    val headers: List<HeaderRow> = emptyList(),
    val subtitles: List<SubtitleRow> = emptyList(),
    val submitting: Boolean = false,
    val error: String? = null,
    /** The server answered `already_exists`: ask whether to replace the library file. */
    val confirmReplace: Boolean = false,
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
    private val settings: SettingsRepository,
    private val categoryRepository: CategoryRepository,
    private val serverSettings: ServerSettingsRepository,
    private val submit: SubmitUseCase,
    private val submitListener: SubmitListener,
) : ViewModel() {

    // Type-safe navigation stores AddRoute's arguments in the SavedStateHandle under their property names.
    private val _uiState = MutableStateFlow(AddUiState(url = savedStateHandle.get<String>("url").orEmpty()))
    val uiState: StateFlow<AddUiState> = _uiState.asStateFlow()

    private val eventChannel = Channel<AddEvent>(Channel.BUFFERED)
    val events: Flow<AddEvent> = eventChannel.receiveAsFlow()

    private var nextRowId = 1
    private var connection: ServerConfig? = null
    private var categoryGeneration = 0L
    private var categoryRequest: Job? = null
    private var newCategoryRequest: Job? = null

    init {
        viewModelScope.launch {
            hub.addUrls.collect { url -> onUrl(url) }
        }
        viewModelScope.launch {
            settings.settings.collect { app ->
                val nextConnection = app.serverUrl.takeIf { it.isNotBlank() }
                    ?.let { ServerConfig(it, app.token.takeIf(String::isNotBlank)) }
                val connectionChanged = connection != nextConnection
                if (connectionChanged) {
                    connection = nextConnection
                    categoryGeneration++
                    categoryRequest?.cancel()
                    newCategoryRequest?.cancel()
                    _uiState.update { it.copy(newCategory = null) }
                }
                val categories = app.cachedCategories.map { CategoryOption(it.id, it.label, it.layout) }
                _uiState.update { cur ->
                    var next = cur.copy(
                        settingsLoaded = true,
                        serverConfigured = app.serverUrl.isNotBlank(),
                        categories = categories,
                        categoriesLoading = if (connectionChanged) false else cur.categoriesLoading,
                        categoryError = if (connectionChanged || categories.isNotEmpty()) null else cur.categoryError,
                    )
                    if (categories.none { it.id == next.categoryId }) {
                        next = next.copy(categoryId = CategoryPreselector.preselect(
                            MANUAL_REQUEST, categories, app.defaultMovieCategoryId, app.defaultTvCategoryId,
                        ))
                    }
                    next
                }
                if (connectionChanged && nextConnection != null && categories.isEmpty()) refreshCategories()
            }
        }
        viewModelScope.launch {
            categoryRepository.info.collect { info ->
                _uiState.update { it.copy(canAddCategory = info?.settingsSupported != false) }
            }
        }
    }

    /** A retry keeps the current form and is invalidated when the connection changes. */
    fun refreshCategories() {
        val target = connection ?: return
        if (_uiState.value.categoriesLoading) return
        val generation = categoryGeneration
        _uiState.update { it.copy(categoriesLoading = true, categoryError = null) }
        categoryRequest = viewModelScope.launch {
            // The DataStore and its derived config flow can deliver the same write at different times.
            settings.config.first { it == target }
            val result = categoryRepository.refresh(target)
            if (connection != target || categoryGeneration != generation) return@launch
            _uiState.update { current ->
                current.copy(
                    categoriesLoading = false,
                    categoryError = (result as? ApiResult.Failure)?.error?.userMessage,
                )
            }
        }
    }

    // ---- new category ----------------------------------------------------------------------

    fun openNewCategory() = _uiState.update { if (it.newCategory == null) it.copy(newCategory = NewCategoryDraft()) else it }

    fun editNewCategory(transform: (NewCategoryDraft) -> NewCategoryDraft) = _uiState.update { state ->
        val draft = state.newCategory?.takeUnless { it.saving } ?: return@update state
        state.copy(newCategory = transform(draft).copy(error = null))
    }

    /** Ignored while saving: the server may still apply the change. */
    fun dismissNewCategory() = _uiState.update { if (it.newCategory?.saving == true) it else it.copy(newCategory = null) }

    /** Appends the category to the server's settings, then selects it once the picker has it. */
    fun saveNewCategory() {
        val draft = _uiState.value.newCategory ?: return
        val target = connection ?: return
        if (draft.saving) return
        if (draft.labelInvalid || draft.pathInvalid) {
            _uiState.update { it.copy(newCategory = draft.copy(showErrors = true)) }
            return
        }
        val generation = categoryGeneration
        val category = ServerCategoryDto(
            id = UUID.randomUUID().toString().replace("-", ""),
            label = draft.label.trim(),
            path = draft.path.trim(),
            layout = draft.layout,
        )
        _uiState.update { it.copy(newCategory = draft.copy(saving = true, error = null)) }
        newCategoryRequest = viewModelScope.launch {
            val result = appendCategory(target, category)
            if (connection != target || categoryGeneration != generation) return@launch
            when (result) {
                is ApiResult.Failure -> _uiState.update { state ->
                    state.copy(newCategory = state.newCategory?.copy(saving = false, error = result.error.userMessage))
                }
                is ApiResult.Success -> {
                    settings.reconcileServerCategories(target, result.value.categories.map {
                        CategoryDto(it.id, it.label, it.layout, it.path)
                    })
                    _uiState.update { it.copy(newCategory = null, categoryId = category.id, error = null) }
                    // Refreshes free space and capability flags; the save response already updated the picker.
                    categoryRepository.refresh(target)
                }
            }
        }
    }

    /** Reads the latest document so other settings are kept; retries once if it changed in between. */
    private suspend fun appendCategory(target: ServerConfig, category: ServerCategoryDto): ApiResult<ServerSettingsDto> {
        repeat(2) { attempt ->
            val current = when (val loaded = serverSettings.load(target)) {
                is ApiResult.Failure -> return loaded
                is ApiResult.Success -> loaded.value
            }
            val saved = serverSettings.save(target, current.copy(categories = current.categories + category))
            if (attempt == 0 && saved is ApiResult.Failure && saved.error is ApiError.SettingsConflict) return@repeat
            return saved
        }
        error("unreachable")
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

    fun submit() = submit(overwrite = false)

    /** "Replace" in the already-in-library dialog: submit again, replacing the library file. */
    fun replaceExisting() {
        _uiState.update { it.copy(confirmReplace = false) }
        submit(overwrite = true)
    }

    /** "Keep existing" or dismissed: stay on the form, which says why nothing was added. */
    fun keepExisting() = _uiState.update { it.copy(confirmReplace = false, error = SubmitOutcome.MESSAGE_ALREADY_EXISTS) }

    private fun submit(overwrite: Boolean) {
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
                overwrite = overwrite,
            )
            when (outcome) {
                is SubmitOutcome.Success -> {
                    submitListener.onSubmitSucceeded()
                    hub.showMessage(outcome.message)
                    eventChannel.trySend(AddEvent.Done)
                }
                // Add keeps the form on a duplicate so the URL can be fixed, unlike the Incoming sheet.
                SubmitOutcome.DuplicateSingle -> failWith(SubmitOutcome.MESSAGE_DUPLICATE)
                SubmitOutcome.AlreadyExists -> _uiState.update { it.copy(submitting = false, confirmReplace = true) }
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
