package dev.istarvin.fetcharr.ui.incoming

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import dev.istarvin.fetcharr.data.CategoryRepository
import dev.istarvin.fetcharr.data.network.dto.SubtitleDto
import dev.istarvin.fetcharr.data.settings.AppSettings
import dev.istarvin.fetcharr.data.settings.SettingsRepository
import dev.istarvin.fetcharr.incoming.CategoryOption
import dev.istarvin.fetcharr.incoming.CategoryPreselector
import dev.istarvin.fetcharr.incoming.IncomingRequest
import dev.istarvin.fetcharr.incoming.IncomingSheet
import dev.istarvin.fetcharr.incoming.IncomingState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Everything the Incoming sheet shows. Holds the editable fields for the open sheet (keyed by [sheet]'s id:
 * they are reset whenever the id changes). Request headers live only inside [sheet] (in memory).
 */
data class IncomingUiState(
    val sheet: IncomingSheet? = null,
    val waitingCount: Int = 0,
    val receiving: Boolean = false,
    /** False until DataStore has been read; the sheet stays hidden until then. */
    val settingsLoaded: Boolean = false,
    val serverConfigured: Boolean = false,
    val serverCategories: List<CategoryOption> = emptyList(),
    val categoryId: String? = null,
    val form: ItemForm = ItemForm(),
    val submitting: Boolean = false,
    val error: String? = null,
) {
    /** Two or more episodes: the season layout. One episode is shown with the single-item layout. */
    val isSeasonMode: Boolean get() = (sheet as? IncomingSheet.SeasonSheet)?.let { it.episodes.size >= 2 } == true

    /** Categories offered as chips: all of them, or only `tv` ones in the season layout. */
    val categories: List<CategoryOption>
        get() = if (isSeasonMode) serverCategories.filter { it.layout == CategoryOption.LAYOUT_TV } else serverCategories

    /** The layout whose fields are shown: the selected category's (tv in the season layout). */
    val layout: String?
        get() = if (isSeasonMode) CategoryOption.LAYOUT_TV else serverCategories.firstOrNull { it.id == categoryId }?.layout

    val selectedCount: Int
        get() = when (val s = sheet) {
            null -> 0
            is IncomingSheet.SingleSheet -> 1
            is IncomingSheet.SeasonSheet -> if (isSeasonMode) s.selectedCount else 1
        }

    /** The "<host> · N subtitles" line of the single layout. */
    val sourceLine: String?
        get() = when (val s = sheet) {
            null -> null
            is IncomingSheet.SingleSheet -> SourceLine.of(s.request.url, s.request.subtitles)
            is IncomingSheet.SeasonSheet -> s.episodes.firstOrNull()?.let { SourceLine.of(it.url, it.subtitles) }
        }

    /** Spec 5.1 footer rule. */
    val canDownload: Boolean
        get() {
            if (sheet == null || !serverConfigured || receiving || submitting) return false
            if (categoryId == null || selectedCount == 0) return false
            return if (isSeasonMode) form.isCompleteForSeason() else layout?.let { form.isCompleteFor(it) } == true
        }
}

@HiltViewModel
class IncomingViewModel @Inject constructor(
    private val hub: IncomingHub,
    settings: SettingsRepository,
    private val categoryRepository: CategoryRepository,
    private val submit: SubmitUseCase,
) : ViewModel() {

    private val _uiState = MutableStateFlow(IncomingUiState())
    val uiState: StateFlow<IncomingUiState> = _uiState.asStateFlow()

    /** Snackbar / navigation events for the root UI. */
    val events = hub.events

    private var refreshedForSheet = 0L

    init {
        viewModelScope.launch {
            combine(hub.coordinator.state, settings.settings) { incoming, app -> incoming to app }
                .collect { (incoming, app) -> sync(incoming, app) }
        }
    }

    // ---- user edits ------------------------------------------------------------------------

    fun selectCategory(id: String) = edit { it.copy(categoryId = id) }

    fun onTitle(v: String) = editForm { it.copy(title = v) }

    fun onYear(v: String) = editForm { it.copy(year = v.filter(Char::isDigit).take(4)) }

    fun onShow(v: String) = editForm { it.copy(show = v) }

    fun onSeason(v: String) = editForm { it.copy(season = v.filter(Char::isDigit).take(4)) }

    fun onEpisode(v: String) = editForm { it.copy(episode = v.filter(Char::isDigit).take(4)) }

    fun onEpisodeTitle(v: String) = editForm { it.copy(episodeTitle = v) }

    fun toggleEpisode(episode: Int) = hub.coordinator.toggleEpisode(episode)

    fun setAllSelected(selected: Boolean) = hub.coordinator.setAllSelected(selected)

    /** Cancel button or swipe down: discards the request and opens the next queued one. */
    fun cancel() = hub.coordinator.close()

    private fun edit(transform: (IncomingUiState) -> IncomingUiState) =
        _uiState.update { transform(it).copy(error = null) }

    private fun editForm(transform: (ItemForm) -> ItemForm) = edit { it.copy(form = transform(it.form)) }

    // ---- submit ----------------------------------------------------------------------------

    fun download() {
        val state = _uiState.value
        if (!state.canDownload) return
        val sheet = state.sheet ?: return
        val categoryId = state.categoryId ?: return
        _uiState.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            val outcome = when (sheet) {
                is IncomingSheet.SingleSheet -> {
                    val r = sheet.request
                    submit.submitJob(
                        r.url, categoryId, state.layout.orEmpty(), state.form, r.headers,
                        r.subtitles.map { SubtitleDto(it.url, it.lang) },
                    )
                }
                is IncomingSheet.SeasonSheet ->
                    if (state.isSeasonMode) {
                        submit.submitSeason(categoryId, state.form, sheet.episodes)
                    } else {
                        val ep = sheet.episodes.first()
                        submit.submitJob(
                            ep.url, categoryId, state.layout.orEmpty(), state.form, ep.headers,
                            ep.subtitles.map { SubtitleDto(it.url, it.lang) },
                        )
                    }
            }
            when (outcome) {
                is SubmitOutcome.Success -> finish(sheet.id, outcome.message)
                SubmitOutcome.DuplicateSingle -> finish(sheet.id, SubmitOutcome.MESSAGE_DUPLICATE)
                is SubmitOutcome.Error -> fail(sheet.id, outcome.message)
                SubmitOutcome.NotConfigured -> fail(sheet.id, SubmitOutcome.MESSAGE_NOT_CONFIGURED)
            }
        }
    }

    private fun finish(sheetId: Long, message: String) {
        hub.showMessage(message)
        // Only close the sheet we submitted; the user may have cancelled it meanwhile and a new one opened.
        if (hub.coordinator.state.value.sheet?.id == sheetId) hub.coordinator.close()
    }

    private fun fail(sheetId: Long, message: String) {
        _uiState.update { if (it.sheet?.id == sheetId) it.copy(submitting = false, error = message) else it }
    }

    // ---- coordinator + settings -> state -----------------------------------------------------

    private fun sync(incoming: IncomingState, app: AppSettings) {
        val categories = app.cachedCategories.map { CategoryOption(it.id, it.label, it.layout) }
        val sheet = incoming.sheet
        _uiState.update { cur ->
            var next = cur.copy(
                sheet = sheet,
                waitingCount = incoming.waitingCount,
                receiving = incoming.receiving,
                settingsLoaded = true,
                serverConfigured = app.serverUrl.isNotBlank(),
                serverCategories = categories,
            )
            if (sheet == null) {
                return@update next.copy(categoryId = null, form = ItemForm(), submitting = false, error = null)
            }
            if (cur.sheet?.id != sheet.id) {
                next = next.copy(categoryId = null, form = initialForm(sheet), submitting = false, error = null)
            } else if (sheet is IncomingSheet.SeasonSheet && next.form.year.isBlank() && sheet.year != null) {
                // A later episode of the same season carried the year the first one lacked.
                next = next.copy(form = next.form.copy(year = sheet.year.toString()))
            }
            // Preselect once per sheet; re-run only while nothing (valid) is chosen, e.g. categories arrive late
            // or the sheet grew into the season layout, which only offers tv categories.
            if (next.categories.none { it.id == next.categoryId }) {
                next = next.copy(
                    categoryId = CategoryPreselector.preselect(
                        sheet.preselectionRequest(), next.categories, app.defaultMovieCategoryId, app.defaultTvCategoryId,
                    ),
                )
            }
            next
        }
        if (sheet != null && app.serverUrl.isNotBlank() && categories.isEmpty() && refreshedForSheet != sheet.id) {
            refreshedForSheet = sheet.id
            viewModelScope.launch { categoryRepository.refresh() } // failure is fine: chips stay empty
        }
    }

    private fun initialForm(sheet: IncomingSheet): ItemForm = when (sheet) {
        is IncomingSheet.SingleSheet -> sheet.request.let {
            ItemForm(title = it.title.orEmpty(), year = it.year?.toString().orEmpty())
        }
        is IncomingSheet.SeasonSheet -> {
            val first = sheet.episodes.firstOrNull()
            ItemForm(
                // Title is only used if the user switches a lone episode to a movie/flat category.
                title = first?.title ?: sheet.showName,
                year = sheet.year?.toString().orEmpty(),
                show = sheet.showName,
                season = sheet.season.toString(),
                episode = first?.episode?.toString().orEmpty(),
                episodeTitle = first?.title.orEmpty(),
            )
        }
    }

    /** A request equivalent to [this] sheet, for [CategoryPreselector] (only hint and episodic-ness matter). */
    private fun IncomingSheet.preselectionRequest(): IncomingRequest = when (this) {
        is IncomingSheet.SingleSheet -> request
        is IncomingSheet.SeasonSheet -> IncomingRequest.Single(
            categoryHint = categoryHint, url = "", title = null, year = year,
            showName = showName, season = season, episode = 0, headers = emptyMap(), subtitles = emptyList(),
        )
    }
}
