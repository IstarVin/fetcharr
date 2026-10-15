package com.istarvin.fetcharr.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.data.CategoryRepository
import com.istarvin.fetcharr.data.ServerSettingsRepository
import com.istarvin.fetcharr.data.network.ApiError
import com.istarvin.fetcharr.data.network.ApiResult
import com.istarvin.fetcharr.data.network.ServerConfig
import com.istarvin.fetcharr.data.network.dto.DownloadSettingsDto
import com.istarvin.fetcharr.data.network.dto.HistorySettingsDto
import com.istarvin.fetcharr.data.network.dto.MediaServerSettingsDto
import com.istarvin.fetcharr.data.network.dto.ServerCategoryDto
import com.istarvin.fetcharr.data.network.dto.ServerSettingsDto
import com.istarvin.fetcharr.data.network.dto.YtdlpSettingsDto
import com.istarvin.fetcharr.data.settings.SettingsRepository
import com.istarvin.fetcharr.data.settings.UrlNormalizer
import dagger.hilt.android.lifecycle.HiltViewModel
import java.math.BigDecimal
import java.util.UUID
import javax.inject.Inject
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class CategoryDraft(
    val key: String = UUID.randomUUID().toString(),
    val id: String = "",
    val label: String = "",
    val path: String = "",
    val layout: String = "flat",
    val existing: Boolean = false,
)

data class PathMappingDraft(
    val key: String = UUID.randomUUID().toString(),
    val from: String = "",
    val to: String = "",
)

data class IntegrationDraft(
    val id: String = UUID.randomUUID().toString(),
    val type: String = "jellyfin",
    val url: String = "",
    val credential: String = "",
    val hasCredential: Boolean = false,
    val existing: Boolean = false,
    val paths: List<PathMappingDraft> = emptyList(),
)

/** This draft stays in memory only: credentials are never written to SavedStateHandle or DataStore. */
data class ServerSettingsDraft(
    val revision: Long,
    val concurrency: String,
    val mergeFormat: String,
    val minFreeGb: String,
    val retentionDays: String,
    val autoUpdate: Boolean,
    val categories: List<CategoryDraft>,
    val integrations: List<IntegrationDraft>,
) {
    fun validationErrors(): Map<String, Int> = buildMap {
        if ((concurrency.toIntOrNull() ?: 0) < 1) put("concurrency", R.string.server_settings_positive_integer)
        if (mergeFormat.isBlank()) put("mergeFormat", R.string.server_settings_required)
        if (freeBytes() == null) put("minFreeGb", R.string.server_settings_space_invalid)
        if ((retentionDays.toIntOrNull() ?: -1) < 0) put("retentionDays", R.string.server_settings_nonnegative_integer)
        categories.forEachIndexed { index, c ->
            if (!Regex("^[a-z0-9_]+$").matches(c.id) || categories.count { it.id == c.id } > 1) {
                put("category.$index.id", R.string.server_settings_category_id_invalid)
            }
            if (!c.path.startsWith('/') || c.path.contains('\u0000')) put("category.$index.path", R.string.server_settings_path_invalid)
        }
        integrations.forEachIndexed { index, s ->
            if (UrlNormalizer.normalize(s.url) == null) put("integration.$index.url", R.string.settings_server_url_invalid)
            if (!s.hasCredential && s.credential.isBlank()) put("integration.$index.credential", R.string.server_settings_required)
            s.paths.forEachIndexed { pathIndex, p ->
                val absoluteDestination = p.to.startsWith('/') || p.to.startsWith("\\\\") || Regex("^[A-Za-z]:[\\\\/].*").matches(p.to)
                if (!p.from.startsWith('/') || !absoluteDestination || s.paths.count { it.from == p.from } > 1) {
                    put("integration.$index.path.$pathIndex", R.string.server_settings_mapping_invalid)
                }
            }
        }
    }

    private fun freeBytes(): Long? = try {
        minFreeGb.toBigDecimalOrNull()?.movePointRight(9)?.longValueExact()?.takeIf { it >= 0 }
    } catch (_: ArithmeticException) { null }

    fun document() = ServerSettingsDto(
        revision = revision,
        downloads = DownloadSettingsDto(concurrency.toInt(), mergeFormat.trim(), requireNotNull(freeBytes())),
        history = HistorySettingsDto(retentionDays.toInt()),
        ytdlp = YtdlpSettingsDto(autoUpdate),
        categories = categories.map { ServerCategoryDto(it.id, it.label, it.path.trim(), it.layout) },
        mediaServers = integrations.map { s ->
            MediaServerSettingsDto(
                id = s.id, type = s.type, url = requireNotNull(UrlNormalizer.normalize(s.url)),
                pathMap = s.paths.associate { it.from to it.to },
                credential = s.credential.takeIf { it.isNotBlank() }, hasCredential = s.hasCredential,
            )
        },
    )

    companion object {
        fun from(d: ServerSettingsDto) = ServerSettingsDraft(
            revision = d.revision,
            concurrency = d.downloads.concurrency.toString(), mergeFormat = d.downloads.mergeFormat,
            minFreeGb = BigDecimal.valueOf(d.downloads.minFreeSpace).movePointLeft(9).stripTrailingZeros().toPlainString(),
            retentionDays = d.history.retentionDays.toString(), autoUpdate = d.ytdlp.autoUpdate,
            categories = d.categories.map { CategoryDraft(id = it.id, label = it.label, path = it.path, layout = it.layout, existing = true) },
            integrations = d.mediaServers.map { s ->
                IntegrationDraft(
                    id = s.id, type = s.type, url = s.url, hasCredential = s.hasCredential, existing = true,
                    paths = s.pathMap.map { PathMappingDraft(from = it.key, to = it.value) },
                )
            },
        )
    }
}

data class ServerSettingsUiState(
    val serverUrl: String? = null,
    val loading: Boolean = false,
    val unsupported: Boolean = false,
    val managedYtdlp: Boolean = false,
    val baseline: ServerSettingsDraft? = null,
    val draft: ServerSettingsDraft? = null,
    val saving: Boolean = false,
    val error: String? = null,
    val fieldErrors: Map<String, Int> = emptyMap(),
    val conflict: Boolean = false,
    val saved: Boolean = false,
) {
    val dirty: Boolean get() = draft != baseline
}

@HiltViewModel
class ServerSettingsViewModel @Inject constructor(
    private val settings: SettingsRepository,
    private val repository: ServerSettingsRepository,
    private val categories: CategoryRepository,
) : ViewModel() {
    private val state = MutableStateFlow(ServerSettingsUiState())
    val uiState = state.asStateFlow()
    private var connection: ServerConfig? = null
    private var generation = 0L
    private var request: Job? = null

    init {
        viewModelScope.launch {
            settings.config.collect { next ->
                connection = next
                generation++
                request?.cancel()
                state.value = ServerSettingsUiState(serverUrl = next?.baseUrl)
                if (next != null) reload()
            }
        }
    }

    fun reload() {
        val target = connection ?: return
        val session = ++generation
        request?.cancel()
        state.value = ServerSettingsUiState(serverUrl = target.baseUrl, loading = true)
        request = viewModelScope.launch {
            when (val info = repository.info(target)) {
                is ApiResult.Failure -> if (isCurrent(target, session)) state.update { it.copy(loading = false, error = info.error.userMessage) }
                is ApiResult.Success -> {
                    if (!isCurrent(target, session)) return@launch
                    if (!info.value.settingsSupported) {
                        state.update { it.copy(loading = false, unsupported = true) }
                        return@launch
                    }
                    when (val result = repository.load(target)) {
                        is ApiResult.Failure -> if (isCurrent(target, session)) state.update { it.copy(loading = false, error = result.error.userMessage) }
                        is ApiResult.Success -> if (isCurrent(target, session)) {
                            val draft = ServerSettingsDraft.from(result.value)
                            state.update { it.copy(loading = false, managedYtdlp = info.value.ytdlpManaged, baseline = draft, draft = draft) }
                        }
                    }
                }
            }
        }
    }

    fun edit(transform: (ServerSettingsDraft) -> ServerSettingsDraft) {
        state.update { current ->
            if (current.saving || current.draft == null) current
            else current.copy(draft = transform(current.draft), fieldErrors = emptyMap(), error = current.error.takeIf { current.conflict }, saved = false)
        }
    }

    fun save() {
        val current = state.value
        val draft = current.draft ?: return
        val target = connection ?: return
        if (current.saving || current.conflict || !current.dirty) return
        val errors = draft.validationErrors()
        if (errors.isNotEmpty()) { state.update { it.copy(fieldErrors = errors) }; return }
        val session = generation
        state.update { it.copy(saving = true, error = null, saved = false) }
        request = viewModelScope.launch {
            when (val result = repository.save(target, draft.document())) {
                is ApiResult.Failure -> if (isCurrent(target, session)) {
                    state.update { it.copy(saving = false, error = result.error.userMessage, conflict = result.error is ApiError.SettingsConflict) }
                }
                is ApiResult.Success -> if (isCurrent(target, session)) {
                    val savedDraft = ServerSettingsDraft.from(result.value)
                    state.update { it.copy(saving = false, baseline = savedDraft, draft = savedDraft, saved = true) }
                    // The save response is enough to reconcile pickers even if a follow-up info request fails.
                    settings.reconcileServerCategories(target, result.value.categories.map {
                        com.istarvin.fetcharr.data.network.dto.CategoryDto(it.id, it.label, it.layout, it.path)
                    })
                    when (val info = repository.info(target)) {
                        is ApiResult.Success -> if (isCurrent(target, session)) categories.acceptServerInfo(target, info.value)
                        is ApiResult.Failure -> Unit
                    }
                }
            }
        }
    }

    private fun isCurrent(target: ServerConfig, session: Long): Boolean =
        connection == target && settings.config.value == target && generation == session
}
