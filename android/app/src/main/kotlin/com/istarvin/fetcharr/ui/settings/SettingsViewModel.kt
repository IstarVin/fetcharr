package com.istarvin.fetcharr.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import com.istarvin.fetcharr.data.CategoryRepository
import com.istarvin.fetcharr.data.SystemRepository
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.ConnectionTester
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.TestConnectionResult
import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.data.settings.SettingsRepository
import com.istarvin.fetcharr.data.settings.UrlNormalizer
import javax.inject.Inject
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

sealed interface TestStatus {
    data object Idle : TestStatus

    data object Running : TestStatus

    data class Done(val result: TestConnectionResult) : TestStatus
}

sealed interface YtdlpUpdateStatus {
    data object Idle : YtdlpUpdateStatus

    data object Checking : YtdlpUpdateStatus

    data class Available(val current: String, val latest: String) : YtdlpUpdateStatus

    data class Latest(val version: String) : YtdlpUpdateStatus

    data object Running : YtdlpUpdateStatus

    /** [version] is the version now installed (null if the server didn't say). */
    data class Updated(val version: String?) : YtdlpUpdateStatus

    data class Failed(val error: ApiError, val latest: String? = null) : YtdlpUpdateStatus
}

data class SettingsUiState(
    val hasSavedServer: Boolean = false,
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
    /** From the last successful `/api/info`; null until one arrives. */
    val serverVersion: String? = null,
    val ytdlpVersion: String? = null,
    /** False when the server didn't report yt-dlp as managed (or info is unknown): update controls are hidden. */
    val ytdlpManaged: Boolean = false,
    val ytdlpUpdate: YtdlpUpdateStatus = YtdlpUpdateStatus.Idle,
)

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val settings: SettingsRepository,
    private val categories: CategoryRepository,
    private val system: SystemRepository,
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
    private val ytdlpUpdate = MutableStateFlow<YtdlpUpdateStatus>(YtdlpUpdateStatus.Idle)
    private var testJob: Job? = null
    private var ytdlpJob: Job? = null

    val uiState: StateFlow<SettingsUiState> = combine(form, settings.settings, categories.info, ytdlpUpdate) { f, s, info, update ->
        SettingsUiState(
            hasSavedServer = s.serverUrl.isNotBlank(),
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
            serverVersion = info?.version,
            ytdlpVersion = when (update) {
                is YtdlpUpdateStatus.Updated -> update.version ?: info?.ytdlpVersion
                is YtdlpUpdateStatus.Latest -> update.version
                is YtdlpUpdateStatus.Available -> update.current
                else -> info?.ytdlpVersion
            },
            ytdlpManaged = info?.ytdlpManaged == true && (update as? YtdlpUpdateStatus.Failed)?.error != ApiError.YtdlpUnmanaged,
            ytdlpUpdate = update,
        )
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), SettingsUiState())

    init {
        viewModelScope.launch {
            settings.config.collect {
                ytdlpJob?.cancel()
                ytdlpUpdate.value = YtdlpUpdateStatus.Idle
            }
        }
        // Make sure the About/yt-dlp rows have data even when the sync connection hasn't fetched /api/info yet.
        viewModelScope.launch { if (categories.info.value == null) categories.refresh() }
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
     * categories are cached too when the tested connection is the saved server.
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

    fun checkYtdlpUpdate() {
        if (ytdlpUpdate.value == YtdlpUpdateStatus.Checking || ytdlpUpdate.value == YtdlpUpdateStatus.Running) return
        ytdlpUpdate.value = YtdlpUpdateStatus.Checking
        ytdlpJob = viewModelScope.launch {
            ytdlpUpdate.value = when (val result = system.checkYtdlpUpdate()) {
                is ApiResult.Success -> if (result.value.updateAvailable) {
                    YtdlpUpdateStatus.Available(result.value.current, result.value.latest)
                } else {
                    YtdlpUpdateStatus.Latest(result.value.current)
                }
                is ApiResult.Failure -> YtdlpUpdateStatus.Failed(result.error)
            }
        }
    }

    /** `POST /api/system/ytdlp/update`, then refreshes `/api/info` so the new version shows. */
    fun updateYtdlp() {
        if (ytdlpUpdate.value == YtdlpUpdateStatus.Running || ytdlpUpdate.value == YtdlpUpdateStatus.Checking) return
        val latest = when (val status = ytdlpUpdate.value) {
            is YtdlpUpdateStatus.Available -> status.latest
            is YtdlpUpdateStatus.Failed -> status.latest
            else -> null
        }
        ytdlpUpdate.value = YtdlpUpdateStatus.Running
        ytdlpJob = viewModelScope.launch {
            when (val result = system.updateYtdlp()) {
                is ApiResult.Success -> {
                    categories.refresh()
                    ytdlpUpdate.value = if (!result.value.current.isNullOrBlank()) {
                        YtdlpUpdateStatus.Latest(result.value.current)
                    } else {
                        YtdlpUpdateStatus.Updated(null)
                    }
                }
                is ApiResult.Failure -> ytdlpUpdate.value = YtdlpUpdateStatus.Failed(result.error, latest)
            }
        }
    }
}
