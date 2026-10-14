package dev.istarvin.fetcharr.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import dev.istarvin.fetcharr.data.CategoryRepository
import dev.istarvin.fetcharr.data.network.ConnectionTester
import dev.istarvin.fetcharr.data.network.ServerConfig
import dev.istarvin.fetcharr.data.network.TestConnectionResult
import dev.istarvin.fetcharr.data.network.dto.CategoryDto
import dev.istarvin.fetcharr.data.settings.SettingsRepository
import dev.istarvin.fetcharr.data.settings.UrlNormalizer
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface TestStatus {
    data object Idle : TestStatus

    data object Running : TestStatus

    data class Done(val result: TestConnectionResult) : TestStatus
}

data class SettingsUiState(
    /** False until the saved values have been read into the form. */
    val loaded: Boolean = false,
    val url: String = "",
    val token: String = "",
    val tokenVisible: Boolean = false,
    val urlInvalid: Boolean = false,
    val testStatus: TestStatus = TestStatus.Idle,
    val movieCategories: List<CategoryDto> = emptyList(),
    val tvCategories: List<CategoryDto> = emptyList(),
    val defaultMovieCategoryId: String? = null,
    val defaultTvCategoryId: String? = null,
)

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val settings: SettingsRepository,
    private val categories: CategoryRepository,
    private val connectionTester: ConnectionTester,
) : ViewModel() {

    private data class Form(
        val loaded: Boolean = false,
        val url: String = "",
        val token: String = "",
        val tokenVisible: Boolean = false,
        val urlInvalid: Boolean = false,
        val testStatus: TestStatus = TestStatus.Idle,
    )

    private val form = MutableStateFlow(Form())
    private var testJob: Job? = null

    val uiState: StateFlow<SettingsUiState> = combine(form, settings.settings) { f, s ->
        SettingsUiState(
            loaded = f.loaded,
            url = f.url,
            token = f.token,
            tokenVisible = f.tokenVisible,
            urlInvalid = f.urlInvalid,
            testStatus = f.testStatus,
            movieCategories = s.cachedCategories.filter {
                it.layout == CategoryDto.LAYOUT_MOVIE || it.layout == CategoryDto.LAYOUT_FLAT
            },
            tvCategories = s.cachedCategories.filter { it.layout == CategoryDto.LAYOUT_TV },
            defaultMovieCategoryId = s.defaultMovieCategoryId,
            defaultTvCategoryId = s.defaultTvCategoryId,
        )
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), SettingsUiState())

    init {
        viewModelScope.launch {
            val saved = settings.settings.first()
            form.update { it.copy(loaded = true, url = saved.serverUrl, token = saved.token) }
        }
    }

    fun onUrlChange(value: String) {
        form.update { it.copy(url = value, urlInvalid = false, testStatus = TestStatus.Idle) }
        testJob?.cancel()
    }

    fun onTokenChange(value: String) {
        form.update { it.copy(token = value, testStatus = TestStatus.Idle) }
        testJob?.cancel()
    }

    fun toggleTokenVisible() {
        form.update { it.copy(tokenVisible = !it.tokenVisible) }
    }

    /**
     * Saves the entered values. Returns false (and flags the URL field) when the URL is not blank but invalid.
     * A blank URL clears the saved server. The form then shows the normalized URL.
     */
    fun save(): Boolean {
        val current = form.value
        val url = if (current.url.isBlank()) "" else UrlNormalizer.normalize(current.url)
        if (url == null) {
            form.update { it.copy(urlInvalid = true) }
            return false
        }
        val token = current.token.trim()
        form.update { it.copy(url = url, token = token, urlInvalid = false) }
        viewModelScope.launch { settings.setServer(url, token) }
        return true
    }

    /**
     * Tests the currently entered (normalized) values, saved or not. On success the server's
     * categories are cached too.
     */
    fun testConnection() {
        val current = form.value
        val url = UrlNormalizer.normalize(current.url)
        if (url == null) {
            form.update { it.copy(urlInvalid = true) }
            return
        }
        val config = ServerConfig(url, current.token.trim().takeIf { it.isNotEmpty() })
        testJob?.cancel()
        form.update { it.copy(url = url, urlInvalid = false, testStatus = TestStatus.Running) }
        testJob = viewModelScope.launch {
            val result = connectionTester.test(config)
            if (result is TestConnectionResult.Connected) categories.refresh(config)
            form.update { it.copy(testStatus = TestStatus.Done(result)) }
        }
    }

    fun onDefaultMovieCategory(id: String?) {
        viewModelScope.launch { settings.setDefaultMovieCategoryId(id) }
    }

    fun onDefaultTvCategory(id: String?) {
        viewModelScope.launch { settings.setDefaultTvCategoryId(id) }
    }
}
