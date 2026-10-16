package com.istarvin.fetcharr.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.Key
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.Movie
import androidx.compose.material.icons.outlined.NetworkCheck
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material.icons.outlined.SystemUpdateAlt
import androidx.compose.material.icons.outlined.Tune
import androidx.compose.material.icons.outlined.Tv
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material.icons.outlined.VisibilityOff
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MediumTopAppBar
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.istarvin.fetcharr.BuildConfig
import com.istarvin.fetcharr.R
import com.istarvin.fetcharr.data.network.TestConnectionResult
import com.istarvin.fetcharr.data.network.dto.CategoryDto
import com.istarvin.fetcharr.ui.LocalSnackbarHostState
import com.istarvin.fetcharr.ui.common.AppCard
import com.istarvin.fetcharr.ui.common.Pill
import com.istarvin.fetcharr.ui.common.ScreenFrame
import com.istarvin.fetcharr.ui.common.SectionLabel
import com.istarvin.fetcharr.ui.common.SettingsRow
import com.istarvin.fetcharr.ui.common.screenGutter
import com.istarvin.fetcharr.ui.theme.statusColors
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(viewModel: SettingsViewModel = hiltViewModel(), onServerSettings: () -> Unit = {}) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val snackbar = LocalSnackbarHostState.current
    val scope = rememberCoroutineScope()
    val savedMessage = stringResource(R.string.settings_saved)
    val scrollBehavior = TopAppBarDefaults.exitUntilCollapsedScrollBehavior()

    Scaffold(
        modifier = Modifier.nestedScroll(scrollBehavior.nestedScrollConnection),
        topBar = {
            MediumTopAppBar(
                title = { Text(stringResource(R.string.nav_settings)) },
                scrollBehavior = scrollBehavior,
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface,
                    scrolledContainerColor = MaterialTheme.colorScheme.surfaceContainer,
                ),
            )
        },
    ) { padding ->
        if (!state.loaded) return@Scaffold
        val horizontalPadding = screenGutter()
        ScreenFrame(Modifier.padding(padding).imePadding()) {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = horizontalPadding, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                SectionLabel(stringResource(R.string.settings_group_server))
                AppCard {
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        OutlinedTextField(
                            value = state.url,
                            onValueChange = viewModel::onUrlChange,
                            modifier = Modifier.fillMaxWidth(),
                            label = { Text(stringResource(R.string.settings_server_url)) },
                            placeholder = { Text(stringResource(R.string.settings_server_url_hint)) },
                            leadingIcon = { Icon(Icons.Outlined.Link, contentDescription = null) },
                            singleLine = true,
                            isError = state.urlInvalid,
                            supportingText = if (state.urlInvalid) {
                                { Text(stringResource(R.string.settings_server_url_invalid)) }
                            } else {
                                null
                            },
                            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                        )
                        OutlinedTextField(
                            value = state.token,
                            onValueChange = viewModel::onTokenChange,
                            modifier = Modifier.fillMaxWidth(),
                            label = { Text(stringResource(R.string.settings_token)) },
                            leadingIcon = { Icon(Icons.Outlined.Key, contentDescription = null) },
                            singleLine = true,
                            visualTransformation = if (state.tokenVisible) VisualTransformation.None else PasswordVisualTransformation(),
                            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                            trailingIcon = {
                                IconButton(onClick = viewModel::toggleTokenVisible) {
                                    Icon(
                                        imageVector = if (state.tokenVisible) Icons.Outlined.VisibilityOff else Icons.Outlined.Visibility,
                                        contentDescription = stringResource(
                                            if (state.tokenVisible) R.string.settings_token_hide else R.string.settings_token_show,
                                        ),
                                    )
                                }
                            },
                        )
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            Button(onClick = {
                                if (viewModel.save()) scope.launch { snackbar.showSnackbar(savedMessage) }
                            }, modifier = Modifier.heightIn(min = 48.dp)) {
                                Icon(Icons.Outlined.Save, contentDescription = null, modifier = Modifier.size(18.dp))
                                Text(stringResource(R.string.settings_save), Modifier.padding(start = 8.dp))
                            }
                            OutlinedButton(
                                onClick = viewModel::testConnection,
                                enabled = state.testStatus != TestStatus.Running,
                                modifier = Modifier.heightIn(min = 48.dp),
                            ) {
                                if (state.testStatus == TestStatus.Running) {
                                    CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                                } else {
                                    Icon(Icons.Outlined.NetworkCheck, contentDescription = null, modifier = Modifier.size(18.dp))
                                }
                                Text(
                                    stringResource(
                                        if (state.testStatus == TestStatus.Running) R.string.settings_testing else R.string.settings_test,
                                    ),
                                    Modifier.padding(start = 8.dp),
                                )
                            }
                        }
                        TestResultText(state.testStatus)
                    }
                }

                AppCard(onClick = onServerSettings, enabled = state.hasSavedServer) {
                    SettingsRow(
                        icon = Icons.Outlined.Tune,
                        title = stringResource(R.string.server_settings_title),
                        subtitle = stringResource(R.string.settings_server_settings_summary),
                        enabled = state.hasSavedServer,
                        trailing = { Icon(Icons.AutoMirrored.Outlined.KeyboardArrowRight, contentDescription = null) },
                    )
                }

                SectionLabel(stringResource(R.string.settings_group_defaults))
                AppCard {
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        CategoryDropdown(
                            label = R.string.settings_default_movie,
                            icon = Icons.Outlined.Movie,
                            categories = state.movieCategories,
                            selectedId = state.defaultMovieCategoryId,
                            onSelect = viewModel::onDefaultMovieCategory,
                        )
                        CategoryDropdown(
                            label = R.string.settings_default_tv,
                            icon = Icons.Outlined.Tv,
                            categories = state.tvCategories,
                            selectedId = state.defaultTvCategoryId,
                            onSelect = viewModel::onDefaultTvCategory,
                        )
                        if (state.movieCategories.isEmpty() && state.tvCategories.isEmpty()) {
                            Text(
                                stringResource(R.string.settings_defaults_hint),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }

                if (state.serverVersion != null) YtdlpSection(state, onUpdate = viewModel::updateYtdlp)

                SectionLabel(stringResource(R.string.settings_group_about))
                val uriHandler = LocalUriHandler.current
                val sourceUrl = stringResource(R.string.settings_source_code_url)
                AppCard {
                    SettingsRow(Icons.Outlined.Info, stringResource(R.string.settings_app_version), subtitle = BuildConfig.VERSION_NAME)
                    HorizontalDivider(Modifier.padding(start = 72.dp), color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f))
                    SettingsRow(
                        Icons.Outlined.Dns,
                        stringResource(R.string.settings_server_version),
                        subtitle = state.serverVersion ?: stringResource(R.string.settings_no_value),
                    )
                }
                AppCard(onClick = { uriHandler.openUri(sourceUrl) }) {
                    SettingsRow(
                        Icons.Outlined.Code,
                        stringResource(R.string.settings_source_code),
                        trailing = { Icon(Icons.AutoMirrored.Outlined.OpenInNew, contentDescription = null) },
                    )
                }
                Spacer(Modifier.height(8.dp))
            }
        }
    }
}

/** yt-dlp version + "Update now". The button is hidden when the server doesn't manage yt-dlp. */
@Composable
private fun YtdlpSection(state: SettingsUiState, onUpdate: () -> Unit) {
    val update = state.ytdlpUpdate
    val version = state.ytdlpVersion
        ?: (update as? YtdlpUpdateStatus.Updated)?.version
    SectionLabel(stringResource(R.string.settings_group_ytdlp))
    AppCard {
        SettingsRow(
            Icons.Outlined.Terminal,
            stringResource(R.string.settings_ytdlp_version),
            subtitle = version ?: stringResource(R.string.settings_ytdlp_unknown),
            trailing = if (state.ytdlpManaged) {
                {
                    val running = update == YtdlpUpdateStatus.Running
                    val updatingDesc = stringResource(R.string.settings_ytdlp_updating_desc)
                    FilledTonalButton(onClick = onUpdate, enabled = !running, modifier = Modifier.heightIn(min = 40.dp)) {
                        if (running) {
                            CircularProgressIndicator(
                                modifier = Modifier
                                    .size(16.dp)
                                    .semantics { contentDescription = updatingDesc },
                                strokeWidth = 2.dp,
                            )
                        } else {
                            Icon(Icons.Outlined.SystemUpdateAlt, contentDescription = null, modifier = Modifier.size(18.dp))
                        }
                        Text(
                            stringResource(if (running) R.string.settings_ytdlp_updating else R.string.settings_ytdlp_update),
                            Modifier.padding(start = 8.dp),
                        )
                    }
                }
            } else {
                null
            },
        )
        when (update) {
            is YtdlpUpdateStatus.Updated -> Text(
                update.version?.let { stringResource(R.string.settings_ytdlp_updated, it) }
                    ?: stringResource(R.string.settings_ytdlp_updated_unknown),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.padding(start = 72.dp, end = 16.dp, bottom = 12.dp),
            )
            is YtdlpUpdateStatus.Failed -> Text(
                update.error.userMessage,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(start = 72.dp, end = 16.dp, bottom = 12.dp),
            )
            else -> Unit
        }
    }
}

@Composable
private fun TestResultText(status: TestStatus) {
    if (status !is TestStatus.Done) return
    val result = status.result
    val text = when (result) {
        is TestConnectionResult.Connected -> stringResource(R.string.settings_test_connected, result.version, result.latencyMs)
        TestConnectionResult.Unreachable -> stringResource(R.string.settings_test_unreachable)
        TestConnectionResult.WrongToken -> stringResource(R.string.settings_test_wrong_token)
        TestConnectionResult.NotFetcharr -> stringResource(R.string.settings_test_not_fetcharr)
    }
    val ok = result is TestConnectionResult.Connected
    val colors = MaterialTheme.colorScheme
    val extra = MaterialTheme.statusColors
    Pill(
        text,
        icon = if (ok) Icons.Outlined.CheckCircle else Icons.Outlined.ErrorOutline,
        container = if (ok) extra.successContainer else colors.errorContainer,
        content = if (ok) extra.onSuccessContainer else colors.onErrorContainer,
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CategoryDropdown(
    label: Int,
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    categories: List<CategoryDto>,
    selectedId: String?,
    onSelect: (String?) -> Unit,
) {
    var expanded by remember { mutableStateOf(false) }
    val none = stringResource(R.string.settings_default_none)
    val selectedText = selectedId?.let { id -> categories.firstOrNull { it.id == id }?.label ?: id } ?: none
    ExposedDropdownMenuBox(expanded = expanded, onExpandedChange = { expanded = it }) {
        OutlinedTextField(
            value = selectedText,
            onValueChange = {},
            readOnly = true,
            singleLine = true,
            label = { Text(stringResource(label)) },
            leadingIcon = { Icon(icon, contentDescription = null) },
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = expanded) },
            modifier = Modifier
                .fillMaxWidth()
                .menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable),
        )
        ExposedDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            DropdownMenuItem(
                text = { Text(none) },
                onClick = {
                    onSelect(null)
                    expanded = false
                },
            )
            categories.forEach { category ->
                DropdownMenuItem(
                    text = { Text(category.label) },
                    onClick = {
                        onSelect(category.id)
                        expanded = false
                    },
                )
            }
        }
    }
}
